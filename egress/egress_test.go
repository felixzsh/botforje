package egress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeDial is the dialer the policy tests run over: it records what the policy
// asked for and answers from a queue of canned responses, so no test opens a
// socket.
type fakeDial struct {
	mu        sync.Mutex
	dialed    []string
	responses []string
}

func (d *fakeDial) dial(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	d.dialed = append(d.dialed, address)
	var response string
	if len(d.responses) > 0 {
		response = d.responses[0]
		d.responses = d.responses[1:]
	}
	d.mu.Unlock()
	if response == "" {
		return nil, fmt.Errorf("fake dial: no response for %s", address)
	}
	client, server := net.Pipe()
	go serveOne(server, response)
	return client, nil
}

// addresses lists what the policy dialed, in order.
func (d *fakeDial) addresses() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.dialed...)
}

// serveOne reads one request head and writes one canned response over conn.
func serveOne(conn net.Conn, response string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = io.WriteString(conn, response)
}

// fakeResolver answers LookupIPAddr from a fixed table and records the names it
// was asked for.
type fakeResolver struct {
	answers map[string][]string
	asked   []string
}

func (r *fakeResolver) lookup(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.asked = append(r.asked, host)
	addresses, ok := r.answers[host]
	if !ok {
		return nil, fmt.Errorf("fake resolver: no answer for %s", host)
	}
	resolved := make([]net.IPAddr, 0, len(addresses))
	for _, address := range addresses {
		ip := net.ParseIP(address)
		if ip == nil {
			return nil, fmt.Errorf("fake resolver: bad address %q", address)
		}
		resolved = append(resolved, net.IPAddr{IP: ip})
	}
	return resolved, nil
}

func TestParseMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    Mode
		wantErr bool
	}{
		{name: "empty is open", want: Open},
		{name: "open", value: "open", want: Open},
		{name: "public only", value: "public_only", want: PublicOnly},
		{name: "unknown", value: "private", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, err := ParseMode(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %v, want an error", tc.value, mode)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q): %v", tc.value, err)
			}
			if mode != tc.want {
				t.Fatalf("ParseMode(%q) = %q, want %q", tc.value, mode, tc.want)
			}
		})
	}
}

func TestGuardedDialRefusesBlockedDestinations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		address  string
		resolved map[string][]string
	}{
		{name: "ipv4 loopback literal", address: "127.0.0.1:80"},
		{name: "ipv6 loopback literal", address: "[::1]:80"},
		{name: "ipv4 private literal", address: "10.1.2.3:80"},
		{name: "ipv4 private class b literal", address: "172.16.4.5:80"},
		{name: "ipv4 private class c literal", address: "192.168.1.10:80"},
		{name: "ipv6 unique local literal", address: "[fd00::1]:80"},
		{name: "ipv4 link local literal", address: "169.254.1.1:80"},
		{name: "metadata literal", address: "169.254.169.254:80"},
		{name: "ipv6 link local literal", address: "[fe80::1]:80"},
		{name: "ipv4 unspecified literal", address: "0.0.0.0:80"},
		{name: "ipv6 unspecified literal", address: "[::]:80"},
		{name: "ipv4 multicast literal", address: "224.0.0.1:80"},
		{name: "ipv6 multicast literal", address: "[ff02::1]:80"},
		{
			name:    "name resolving to a private address",
			address: "internal.example.test:80",
			resolved: map[string][]string{
				"internal.example.test": {"10.0.0.5"},
			},
		},
		{
			name:    "name resolving to public and private addresses",
			address: "mixed.example.test:80",
			resolved: map[string][]string{
				"mixed.example.test": {"93.184.216.34", "10.0.0.5"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dial := &fakeDial{}
			resolver := &fakeResolver{answers: tc.resolved}
			conn, err := guardedDial(dial.dial, resolver.lookup)(
				context.Background(), "tcp", tc.address)
			if conn != nil {
				conn.Close()
			}
			if !errors.Is(err, ErrBlocked) {
				t.Fatalf("dial(%s) error = %v, want %v", tc.address, err, ErrBlocked)
			}
			if got := dial.addresses(); len(got) != 0 {
				t.Fatalf("blocked destination dialed %v", got)
			}
		})
	}
}

func TestGuardedDialDialsTheCheckedAddress(t *testing.T) {
	resolver := &fakeResolver{answers: map[string][]string{
		"public.example.test": {"93.184.216.34"},
	}}
	for _, tc := range []struct {
		name    string
		address string
		want    string
	}{
		{name: "public literal", address: "93.184.216.34:443", want: "93.184.216.34:443"},
		{name: "public name", address: "public.example.test:443",
			want: "93.184.216.34:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dial := &fakeDial{responses: []string{httpResponse("200 OK", "ok")}}
			conn, err := guardedDial(dial.dial, resolver.lookup)(
				context.Background(), "tcp", tc.address)
			if err != nil {
				t.Fatalf("dial(%s): %v", tc.address, err)
			}
			conn.Close()
			if got := dial.addresses(); len(got) != 1 || got[0] != tc.want {
				t.Fatalf("dialed %v, want [%s]", got, tc.want)
			}
		})
	}
}

func TestClientAllowsPublicDestination(t *testing.T) {
	dial := &fakeDial{responses: []string{httpResponse("200 OK", "hello")}}
	client := newClient(PublicOnly, dial.dial, publicResolver(t).lookup)
	response, err := client.Get("http://public.example.test/greeting")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want %q", body, "hello")
	}
	if got := dial.addresses(); len(got) != 1 || got[0] != "93.184.216.34:80" {
		t.Fatalf("dialed %v", got)
	}
}

func TestClientRefusesRedirectToBlockedDestination(t *testing.T) {
	redirect := "HTTP/1.1 302 Found\r\n" +
		"Location: http://internal.example.test/admin/reset\r\n" +
		"Content-Length: 0\r\n\r\n"
	dial := &fakeDial{responses: []string{redirect}}
	client := newClient(PublicOnly, dial.dial, publicResolver(t).lookup)
	response, err := client.Get("http://public.example.test/start")
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("Get error = %v, want %v", err, ErrBlocked)
	}
	if got := dial.addresses(); len(got) != 1 {
		t.Fatalf("dialed %v, want only the public destination", got)
	}
}

func TestBlockedDestinationErrorNamesNoTopology(t *testing.T) {
	dial := &fakeDial{}
	client := newClient(PublicOnly, dial.dial, publicResolver(t).lookup)
	_, err := client.Get("http://internal.example.test/admin/reset")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("Get error = %v, want %v", err, ErrBlocked)
	}
	message := err.Error()
	for _, leaked := range []string{"10.0.0.5", "10.", "127.", "169.254", "172.16",
		"192.168", "fd00", "fe80", "scope", "support"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("error %q names internal topology %q", message, leaked)
		}
	}
	if !strings.Contains(message, ErrBlocked.Error()) {
		t.Fatalf("error %q does not report the refused destination", message)
	}
}

func TestOpenModeFiltersNothing(t *testing.T) {
	dial := &fakeDial{responses: []string{httpResponse("200 OK", "local")}}
	client := newClient(Open, dial.dial, publicResolver(t).lookup)
	response, err := client.Get("http://127.0.0.1:9000/admin/reset")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := dial.addresses(); len(got) != 1 || got[0] != "127.0.0.1:9000" {
		t.Fatalf("dialed %v, want [127.0.0.1:9000]", got)
	}
}

// publicResolver resolves the public example name and the internal one, so a
// redirect test reaches the policy's decision instead of a resolver failure.
func publicResolver(t *testing.T) *fakeResolver {
	t.Helper()
	return &fakeResolver{answers: map[string][]string{
		"public.example.test":   {"93.184.216.34"},
		"internal.example.test": {"10.0.0.5"},
	}}
}

func httpResponse(status, body string) string {
	return fmt.Sprintf("HTTP/1.1 %s\r\nContent-Length: %d\r\n\r\n%s",
		status, len(body), body)
}
