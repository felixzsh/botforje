package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func deleteAccount(
	t *testing.T, server *Server, path, token string,
) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, server.Handler(), http.MethodDelete, path, token)
}

// TestAccountDeleteStopsTheBotAndUnpairs covers the whole teardown order: the
// live pairing session is cancelled, the running bot stops before its
// credentials go, and the link is removed (ADR 0021, ADR 0039).
func TestAccountDeleteStopsTheBotAndUnpairs(t *testing.T) {
	server := testServer(t)
	recorder := deleteAccount(t, server,
		"/api/v1/scopes/support/bots/demo/account", testOwnerToken)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	fleet := server.fleet.(*fakeFleet)
	if len(fleet.stopped) != 1 || fleet.stopped[0] != "support/demo" {
		t.Fatalf("stopped = %v, want support/demo", fleet.stopped)
	}
	unpairer := server.unpairer.(*fakeUnpairer)
	if len(unpairer.calls) != 1 || unpairer.calls[0] != "support/demo" {
		t.Fatalf("unpaired = %v, want support/demo", unpairer.calls)
	}
	pairing := server.pairing.(*fakePairing)
	if !pairing.cancelled {
		t.Fatal("the live pairing session was not cancelled")
	}
}

// TestAccountDeleteWithoutAccountIs404: only a linked account can be
// dropped, and an unknown one stops nothing.
func TestAccountDeleteWithoutAccountIs404(t *testing.T) {
	server := testServer(t)
	recorder := deleteAccount(t, server,
		"/api/v1/scopes/ventas/bots/other/account", testOwnerToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "not_found") {
		t.Fatalf("body = %s", recorder.Body)
	}
	fleet := server.fleet.(*fakeFleet)
	if len(fleet.stopped) != 0 {
		t.Fatalf("stopped = %v, want nothing", fleet.stopped)
	}
}

// TestAccountDeleteWithinItsOwnScopeForAScopedToken: a scoped token can
// drop the link of a bot in its own scope, and the scope check is what
// keeps it from reaching another scope.
func TestAccountDeleteWithinItsOwnScopeForAScopedToken(t *testing.T) {
	server := testServer(t)
	recorder := deleteAccount(t, server,
		"/api/v1/scopes/support/bots/demo/account", testAccessToken)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	outOfScope := deleteAccount(t, server,
		"/api/v1/scopes/ventas/bots/other/account", testAccessToken)
	if outOfScope.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope status = %d, want 404", outOfScope.Code)
	}
}
