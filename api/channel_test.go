package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
)

// requireUnsupportedOperation asserts the explicit unsupported outcome an
// absent optional operation answers: 501 with the shared code, never an
// internal error (ADR 0052).
func requireUnsupportedOperation(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", status, body)
	}
	if !strings.Contains(body, `"code":"unsupported_operation"`) {
		t.Fatalf("body = %s, want unsupported_operation", body)
	}
}

// TestChannelDiscovery exposes the installed definition one bot runs on:
// provider, adapter, mode, and declared capabilities in the public
// vocabulary, without credentials or backend locators (ADR 0052, ADR 0054).
func TestChannelDiscovery(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Channels = testCatalog(t) }, nil)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/channel", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Provider     string `json:"provider"`
		Adapter      string `json:"adapter"`
		Mode         string `json:"mode"`
		Capabilities struct {
			Send         []botflota.MessageKind `json:"send"`
			Receive      []botflota.MessageKind `json:"receive"`
			Features     []string               `json:"features"`
			CardVariants []string               `json:"card_variants"`
		} `json:"capabilities"`
	}
	decodeSetupResponse(t, recorder.Body.Bytes(), &body)
	if body.Provider != "whatsapp" || body.Adapter != "fake" ||
		body.Mode != "native" {
		t.Fatalf("identity = %#v, want the installed definition", body)
	}
	if len(body.Capabilities.Send) == 0 || body.Capabilities.Send[0] != botflota.KindText {
		t.Fatalf("send = %v, want declared text support first", body.Capabilities.Send)
	}
	raw := recorder.Body.String()
	for _, key := range []string{
		`"provider"`, `"adapter"`, `"mode"`, `"capabilities"`,
		`"send"`, `"receive"`, `"features"`, `"card_variants"`,
	} {
		if !strings.Contains(raw, key) {
			t.Fatalf("body %s does not use the public name %s", raw, key)
		}
	}
	for _, leaked := range []string{"sk-live", "secret", "token", "locator"} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("body %s leaks %q", raw, leaked)
		}
	}
}

// TestChannelDiscoveryKeepsDeclarationsWhileStopped covers the separation:
// a bot the fleet does not report keeps its declared capabilities without
// invented current permission (ADR 0052).
func TestChannelDiscoveryKeepsDeclarationsWhileStopped(t *testing.T) {
	running := testServerWithOptions(t,
		&fakeFleet{statuses: []botflota.BotStatus{
			{Scope: "support", Bot: "demo", State: "connected"},
		}},
		func(opts *Options) { opts.Channels = testCatalog(t) }, nil)
	stopped := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Channels = testCatalog(t) }, nil)
	path := "/api/v1/scopes/support/bots/demo/channel"
	up := get(t, running.Handler(), path, testOwnerToken)
	down := get(t, stopped.Handler(), path, testOwnerToken)
	if up.Code != http.StatusOK || down.Code != http.StatusOK {
		t.Fatalf("status = %d/%d, want 200/200", up.Code, down.Code)
	}
	if up.Body.String() != down.Body.String() {
		t.Fatalf("running %s != stopped %s, want identical declarations",
			up.Body, down.Body)
	}
}

func TestChannelDiscoveryResolution(t *testing.T) {
	server := testServerWithOptions(t, &fakeFleet{},
		func(opts *Options) { opts.Channels = testCatalog(t) }, nil)
	handler := server.Handler()
	recorder := get(t, handler,
		"/api/v1/scopes/support/bots/ghost/channel", testOwnerToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown bot status = %d, want 404", recorder.Code)
	}
	recorder = get(t, handler,
		"/api/v1/scopes/ventas/bots/other/channel", testAccessToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope status = %d, want 404", recorder.Code)
	}
}

// TestChannelDiscoveryUnavailableWithoutTheCatalog answers the missing
// optional explicitly, never as an internal error (ADR 0052).
func TestChannelDiscoveryUnavailableWithoutTheCatalog(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/bots/demo/channel", testOwnerToken)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Error.Code != "unsupported_operation" {
		t.Fatalf("body = %s, want unsupported_operation", recorder.Body)
	}
}
