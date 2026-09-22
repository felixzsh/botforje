package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
