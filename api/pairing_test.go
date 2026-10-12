package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
)

func pairCall(
	t *testing.T, server *Server, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func TestPairingEndpoints(t *testing.T) {
	server := testServer(t)
	path := "/api/v1/scopes/ventas/bots/other/pairing"

	started := pairCall(t, server, http.MethodPost, path, `{"full_history":true}`)
	if started.Code != http.StatusAccepted {
		t.Fatalf("start = %d, body = %s", started.Code, started.Body)
	}
	var view struct {
		Scope  string `json:"scope"`
		Bot    string `json:"bot"`
		Method string `json:"method"`
		State  string `json:"state"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.Scope != "ventas" || view.Bot != "other" ||
		view.Method != "qr" || view.State != "waiting" || view.Code != "qr-code" {
		t.Fatalf("started = %#v", view)
	}

	paired := pairCall(t, server, http.MethodPost,
		"/api/v1/scopes/support/bots/demo/pairing", "")
	if paired.Code != http.StatusConflict {
		t.Fatalf("already paired = %d, want 409", paired.Code)
	}

	malformed := pairCall(t, server, http.MethodPost, path, "not json")
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed = %d, want 400", malformed.Code)
	}

	state := get(t, server.Handler(), path, testOwnerToken)
	if state.Code != http.StatusOK {
		t.Fatalf("state = %d, want 200", state.Code)
	}
	if !strings.Contains(state.Body.String(), `"state":"waiting"`) {
		t.Fatalf("state body = %s", state.Body)
	}

	unknown := pairCall(t, server, http.MethodPost,
		"/api/v1/scopes/ventas/bots/ghost/pairing", "")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown bot = %d, want 404", unknown.Code)
	}

	cancelled := pairCall(t, server, http.MethodDelete, path, "")
	if cancelled.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d, want 204", cancelled.Code)
	}
}

// TestPairingViewReportsActivationSeparately: a committed binding whose bot
// did not start reads succeeded with its activation failure beside it,
// never hidden inside the setup outcome (ADR 0053).
func TestPairingViewReportsActivationSeparately(t *testing.T) {
	view := pairingViewOf(&botflota.PairingSession{
		Scope: "dev", Bot: "alpha", Method: "qr", State: botflota.PairSucceeded,
		ActivationError: "channel failed to start",
		Result:          botflota.PairingResult{},
	})
	if view.State != "paired" || view.ActivationError == "" {
		t.Fatalf("view = %#v, want succeeded with its activation failure", view)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"activation_error"`) {
		t.Fatalf("view %s carries no activation_error field", raw)
	}
	plain := pairingViewOf(&botflota.PairingSession{
		Scope: "dev", Bot: "alpha", Method: "qr", State: botflota.PairSucceeded,
	})
	if plain.ActivationError != "" {
		t.Fatalf("view = %#v, want no activation field when it started", plain)
	}
	raw, err = json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "activation_error") {
		t.Fatalf("view %s leaks an empty activation field", raw)
	}
}
