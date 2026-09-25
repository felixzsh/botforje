package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// scopeConfigBody is the shape GET /api/v1/scopes/{scope}/config answers. The
// config stays raw because spec.Secret decodes from a string it cannot re-encode
// symmetrically; the fields a test cares about are read one by one.
type scopeConfigBody struct {
	Scope         string          `json:"scope"`
	Version       string          `json:"version"`
	ActiveVersion string          `json:"active_version"`
	Status        string          `json:"status"`
	Config        json.RawMessage `json:"config"`
}

func (b scopeConfigBody) field(t *testing.T, name string) string {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b.Config, &fields); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	raw, ok := fields[name]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return value
}

func (b scopeConfigBody) has(t *testing.T, name, key string) bool {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b.Config, &fields); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	raw, ok := fields[name]
	if !ok {
		return false
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	_, ok = entries[key]
	return ok
}

func TestScopeConfigServesTheSavedDocument(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/config", testOwnerToken)
	body := decodeScopeConfig(t, recorder)

	if body.Scope != "support" {
		t.Fatalf("scope = %q, want support", body.Scope)
	}
	if body.Version == "" || body.Version != body.ActiveVersion {
		t.Fatalf("version = %q, active = %q, want one version that is active",
			body.Version, body.ActiveVersion)
	}
	if body.Status != "active" {
		t.Fatalf("status = %q, want active", body.Status)
	}
	if !body.has(t, "llms", "primary") {
		t.Fatal("the saved scope did not carry its llms")
	}
}

func TestScopeConfigReportsAPendingChange(t *testing.T) {
	server := testServer(t)
	// The files hold a new label the running instance has not adopted yet.
	saved := server.savedConfig().Scopes["support"]
	saved.DisplayName = "Sales"
	server.saved.Replace(&config.Config{Scopes: map[string]spec.Scope{
		"support": saved, "ventas": server.savedConfig().Scopes["ventas"],
	}})

	body := decodeScopeConfig(t,
		get(t, server.Handler(), "/api/v1/scopes/support/config", testOwnerToken))
	if body.Status != "pending" {
		t.Fatalf("status = %q, want pending", body.Status)
	}
	if body.Version == body.ActiveVersion {
		t.Fatal("a pending change kept one version for saved and active")
	}
	if body.field(t, "display_name") != "Sales" {
		t.Fatalf("display_name = %q, want the saved label", body.field(t, "display_name"))
	}
}

func TestScopeConfigMasksSecretsAndKeepsAbsentLabels(t *testing.T) {
	recorder := get(t, testServer(t).Handler(),
		"/api/v1/scopes/support/config", testOwnerToken)

	var raw struct {
		Config struct {
			DisplayName string `json:"display_name"`
			LLMs        map[string]struct {
				APIKey string `json:"api_key"`
			} `json:"llms"`
			Actions map[string]struct {
				Steps []struct {
					Request struct {
						Headers map[string]string `json:"headers"`
					} `json:"request"`
				} `json:"steps"`
			} `json:"actions"`
		} `json:"config"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := raw.Config.LLMs["primary"].APIKey; got != "***" {
		t.Fatalf("api_key = %q, want the mask", got)
	}
	header := raw.Config.Actions["ping"].Steps[0].Request.Headers["Authorization"]
	if header != "Bearer ${API_TOKEN}" {
		t.Fatalf("header = %q, want the reference as written", header)
	}
	// A scope with no display_name keeps it out of the document, so a
	// presentation fallback never becomes saved YAML.
	if raw.Config.DisplayName != "" {
		t.Fatalf("display_name = %q, want absent", raw.Config.DisplayName)
	}
	if strings.Contains(recorder.Body.String(), "sk-live-secret") {
		t.Fatal("the response leaked a secret value")
	}
}

func TestScopeConfigAuthority(t *testing.T) {
	t.Run("a scoped token reads its own scope", func(t *testing.T) {
		recorder := get(t, testServer(t).Handler(),
			"/api/v1/scopes/support/config", testAccessToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
	})

	t.Run("another scope is unknown, not forbidden", func(t *testing.T) {
		recorder := get(t, testServer(t).Handler(),
			"/api/v1/scopes/ventas/config", testAccessToken)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})

	t.Run("an unknown scope answers 404", func(t *testing.T) {
		recorder := get(t, testServer(t).Handler(),
			"/api/v1/scopes/nope/config", testOwnerToken)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
}

func TestScopeConfigReportsAScopeTheFilesNoLongerHold(t *testing.T) {
	server := testServer(t)
	server.saved.Replace(&config.Config{Scopes: map[string]spec.Scope{
		"ventas": server.savedConfig().Scopes["ventas"],
	}})
	body := decodeScopeConfig(t,
		get(t, server.Handler(), "/api/v1/scopes/support/config", testOwnerToken))
	if body.Status != "unsaved" {
		t.Fatalf("status = %q, want unsaved", body.Status)
	}
	if !body.has(t, "bots", "demo") {
		t.Fatal("the running scope was not served")
	}
}

// readScopeConfig answers a scope read and hands back the decoded body.
func readScopeConfig(
	t *testing.T, server *Server, path, token string,
) scopeConfigBody {
	t.Helper()
	return decodeScopeConfig(t, get(t, server.Handler(), path, token))
}

func decodeScopeConfig(t *testing.T, recorder *httptest.ResponseRecorder) scopeConfigBody {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body scopeConfigBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}
