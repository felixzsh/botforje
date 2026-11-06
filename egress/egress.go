// Package egress builds the outbound HTTP client an instance runs with. The
// owner's egress policy (ADR 0048) applies where the connection is made, so a
// literal address, a name that resolves internally, and every redirect are
// checked the same way.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// Mode is an instance's outbound HTTP policy.
type Mode string

const (
	// Open reaches any destination and filters nothing; the default.
	Open Mode = "open"
	// PublicOnly reaches only a public destination.
	PublicOnly Mode = "public_only"
)

// ErrBlocked reports a destination the policy refused. It carries no address,
// so an author learns that the call was refused without learning the network.
var ErrBlocked = errors.New("egress: destination is not public")

// ParseMode reads the configured value, where empty means Open.
func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case "", Open:
		return Open, nil
	case PublicOnly:
		return PublicOnly, nil
	}
	return "", fmt.Errorf("egress: unknown mode %q", value)
}

// Client returns a new HTTP client for mode. Open keeps the standard client's
// behavior unchanged.
func Client(mode Mode) *http.Client {
	if mode != PublicOnly {
		return &http.Client{}
	}
	dial := http.DefaultTransport.(*http.Transport).DialContext
	return newClient(mode, dial, net.DefaultResolver.LookupIPAddr)
}

// newClient is Client over an injected dialer and resolver, so the policy is
// testable without the network.
func newClient(mode Mode, dial dialContext, lookup lookupIP) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A proxy would take the connection and hide the destination from the
	// check, so PublicOnly dials every destination itself.
	transport.Proxy = nil
	if mode != PublicOnly {
		transport.DialContext = dial
		return &http.Client{Transport: transport}
	}
	transport.DialContext = guardedDial(dial, lookup)
	return &http.Client{Transport: transport}
}

type dialContext func(ctx context.Context, network, address string) (net.Conn, error)
type lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)

// guardedDial returns a dialer that refuses a blocked destination before it
// connects. Every address the name resolves to is checked, and the connection
// goes to one of those addresses, so the check covers the address actually
// dialed rather than the URL text. A redirect arrives here as an ordinary new
// connection and is refused the same way.
func guardedDial(dial dialContext, lookup lookupIP) dialContext {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := resolve(ctx, lookup, host, network)
		if err != nil {
			return nil, err
		}
		if len(addresses) == 0 {
			return nil, ErrBlocked
		}
		for _, address := range addresses {
			if blocked(address.IP) {
				return nil, ErrBlocked
			}
		}
		var lastErr error
		for _, address := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// resolve returns the addresses to dial for host: the literal itself when the
// URL names one, otherwise what the resolver answers, narrowed to the network
// the dialer was asked for.
func resolve(
	ctx context.Context, lookup lookupIP, host, network string,
) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var usable []net.IPAddr
	for _, address := range addresses {
		switch network {
		case "tcp4":
			if address.IP.To4() == nil {
				continue
			}
		case "tcp6":
			if address.IP.To4() != nil {
				continue
			}
		}
		usable = append(usable, address)
	}
	return usable, nil
}

// blocked reports whether the policy refuses ip. It names the address classes
// of ADR 0048: loopback, private and unique-local, link-local (which includes
// the link-local metadata address), unspecified, and multicast.
func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified()
}
