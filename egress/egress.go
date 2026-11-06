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
	"net/netip"
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
	if value == "" {
		return Open, nil
	}
	for _, mode := range Modes() {
		if Mode(value) == mode {
			return mode, nil
		}
	}
	return "", fmt.Errorf("egress: unknown mode %q", value)
}

// Modes lists every policy the setting accepts, so a caller that validates the
// vocabulary can pin itself to this list instead of repeating it.
func Modes() []Mode { return []Mode{Open, PublicOnly} }

// Client returns a new HTTP client for mode. Open keeps the standard client's
// behavior unchanged; PublicOnly is built on the standard transport, and a
// transport this package cannot guard is reported instead of guessed at.
func Client(mode Mode) (*http.Client, error) {
	if mode != PublicOnly {
		return &http.Client{}, nil
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	// net/http prefers a TLS dialer over DialContext for an https connection,
	// so a transport that carries one would skip the guarded dial.
	if !ok || transport == nil || transport.DialContext == nil ||
		transport.DialTLSContext != nil || transport.DialTLS != nil {
		return nil, fmt.Errorf("egress: cannot guard the default transport %T",
			http.DefaultTransport)
	}
	dial := transport.DialContext
	return clientFor(mode, transport, dial, net.DefaultResolver.LookupIPAddr), nil
}

// clientFor is Client over an injected base transport, dialer, and resolver,
// so the policy is testable without the network.
func clientFor(
	mode Mode, base *http.Transport, dial dialContext, lookup lookupIP,
) *http.Client {
	transport := base.Clone()
	if mode != PublicOnly {
		transport.DialContext = dial
		return &http.Client{Transport: transport}
	}
	// A proxy would take the connection and hide the destination from the
	// check, so PublicOnly dials every destination itself.
	transport.Proxy = nil
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

// blocked reports whether the policy refuses ip. Only ordinary global unicast
// is public, and every range of the IANA special-purpose address registries is
// refused, so this proves the ADR's destination property instead of trusting
// an incomplete class list.
func blocked(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() {
		return true
	}
	if addr.Is4() {
		return within(addr, specialV4)
	}
	return blockedV6(addr)
}

// blockedV6 decides an IPv6 destination, first decoding the IPv4 address a
// transition form carries so it is judged by the destination it reaches.
func blockedV6(addr netip.Addr) bool {
	raw := addr.As16()
	switch {
	case nat64WellKnown.Contains(addr), nat64LocalUse.Contains(addr):
		return blocked(net.IP(raw[12:16]))
	case sixToFour.Contains(addr):
		return blocked(net.IP(raw[2:6]))
	}
	return within(addr, specialV6)
}

func within(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// cidr4 and cidr6 build a prefix from a program literal, so the registries
// below cannot fail at startup the way a parsed string could.
func cidr4(a, b, c, d byte, bits int) netip.Prefix {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{a, b, c, d}), bits)
}

func cidr6(bits int, head ...byte) netip.Prefix {
	var raw [16]byte
	copy(raw[:], head)
	return netip.PrefixFrom(netip.AddrFrom16(raw), bits)
}

// specialV4 lists the IPv4 special-purpose ranges: this network, private and
// shared address space, loopback, link-local, IETF assignments, documentation
// and benchmarking blocks, multicast, and the reserved block that carries
// limited broadcast.
var specialV4 = []netip.Prefix{
	cidr4(0, 0, 0, 0, 8),
	cidr4(10, 0, 0, 0, 8),
	cidr4(100, 64, 0, 0, 10),
	cidr4(127, 0, 0, 0, 8),
	cidr4(169, 254, 0, 0, 16),
	cidr4(172, 16, 0, 0, 12),
	cidr4(192, 0, 0, 0, 24),
	cidr4(192, 0, 2, 0, 24),
	cidr4(192, 31, 196, 0, 24),
	cidr4(192, 52, 193, 0, 24),
	cidr4(192, 88, 99, 0, 24),
	cidr4(192, 168, 0, 0, 16),
	cidr4(192, 175, 48, 0, 24),
	cidr4(198, 18, 0, 0, 15),
	cidr4(198, 51, 100, 0, 24),
	cidr4(203, 0, 113, 0, 24),
	cidr4(224, 0, 0, 0, 4),
	cidr4(240, 0, 0, 0, 4),
}

// The transition ranges that carry an IPv4 destination; they are decoded
// rather than refused as envelopes.
var (
	nat64WellKnown = cidr6(96, 0x00, 0x64, 0xff, 0x9b)
	nat64LocalUse  = cidr6(48, 0x00, 0x64, 0xff, 0x9b, 0x00, 0x01)
	sixToFour      = cidr6(16, 0x20, 0x02)
)

// specialV6 lists the remaining IPv6 special-purpose ranges: unspecified,
// loopback, the deprecated IPv4-compatible form, discard-only, IETF
// assignments (which include Teredo, benchmarking, and ORCHID), documentation,
// segment routing, unique local, link-local (which carries the metadata
// address), multicast, and the AS112 delegation.
var specialV6 = []netip.Prefix{
	cidr6(96),
	cidr6(64, 0x01, 0x00),
	cidr6(23, 0x20, 0x01),
	cidr6(32, 0x20, 0x01, 0x0d, 0xb8),
	cidr6(20, 0x3f, 0xff),
	cidr6(16, 0x5f, 0x00),
	cidr6(7, 0xfc),
	cidr6(10, 0xfe, 0x80),
	cidr6(10, 0xfe, 0xc0),
	cidr6(8, 0xff),
	cidr6(48, 0x26, 0x20, 0x00, 0x4f, 0x80, 0x00),
}
