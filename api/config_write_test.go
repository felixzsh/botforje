package api

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// publishServer is a server whose writer points at real files, so a publication
// can be checked on disk.
type publishServer struct {
	*Server
	main string
}

func newPublishServer(t *testing.T) publishServer {
	t.Helper()
	server := testServer(t)
	main := server.writer.MainPath()
	if _, err := os.Stat(main); err != nil {
		t.Fatalf("the writer has no config file: %v", err)
	}
	return publishServer{Server: server, main: main}
}

func putScope(
	t *testing.T, server *Server, scope, token string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut,
		"/api/v1/scopes/"+scope+"/config", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func publishScopes(
	t *testing.T, server *Server, token string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/config/publications", bytes.NewReader(encoded))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

type writeResults struct {
	Scopes []scopeWriteResult `json:"scopes"`
}

func decodeWriteResults(t *testing.T, recorder *httptest.ResponseRecorder) writeResults {
	t.Helper()
	var body writeResults
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v: %s", err, recorder.Body.String())
	}
	return body
}

// relabelled is a whole scope with a new label, the way an editor that read the
// scope and changed one field would send it back.
func relabelled(t *testing.T, scope spec.Scope, label string) spec.Scope {
	t.Helper()
	next := spec.Scope{
		DisplayName: label,
		LLMs:        maps.Clone(scope.LLMs),
		Actions:     maps.Clone(scope.Actions),
		Graphs:      maps.Clone(scope.Graphs),
		Bots:        maps.Clone(scope.Bots),
	}
	return next
}

func savedScope(t *testing.T, main, name string) spec.Scope {
	t.Helper()
	cfg, err := (config.Loader{}).Load(main)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	scope, ok := cfg.Scopes[name]
	if !ok {
		t.Fatalf("scope %q is not in the files", name)
	}
	return scope
}

func TestPublishRefusesADuplicateLabel(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)

	t.Run("a rename onto another entry's label", func(t *testing.T) {
		next := relabelled(t, current, "")
		next.Bots = map[string]spec.Bot{
			"demo":  {Graph: "main"},
			"other": {DisplayName: "demo", Graph: "main"},
		}
		recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
			"version": version, "config": next,
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "already used by") {
			t.Fatalf("body = %s, want the collision reported", recorder.Body.String())
		}
	})

	t.Run("a label equal to another entry's key", func(t *testing.T) {
		next := relabelled(t, current, "")
		next.Bots = map[string]spec.Bot{
			"desk":  {Graph: "main"},
			"other": {DisplayName: "  DESK ", Graph: "main"},
		}
		recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
			"version": version, "config": next,
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
	})

	// Nothing was written by either refusal.
	if got := savedScope(t, server.main, "support").Bots["demo"].Graph; got != "main" {
		t.Fatalf("demo graph = %q, want the file untouched", got)
	}
}

func TestPublishAcceptsADistinctLabel(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)
	next := relabelled(t, current, "Help desk")
	next.Bots = map[string]spec.Bot{
		"demo":  {Graph: "main"},
		"other": {DisplayName: "Sales desk", Graph: "main"},
	}
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version, "config": next,
	})
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := savedScope(t, server.main, "support").Bots["other"].DisplayName; got != "Sales desk" {
		t.Fatalf("display_name = %q, want it saved", got)
	}
}

func TestPublishRefusesWhileTheTreeDoesNotLoad(t *testing.T) {
	server := newPublishServer(t)
	// A manual edit that does not parse blocks publication until it is fixed.
	original, err := os.ReadFile(server.main)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	broken := strings.Replace(string(original),
		"address: 127.0.0.1", "address: [not, a, string]", 1)
	if err := os.WriteFile(server.main, []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("the owner gets the reason", func(t *testing.T) {
		recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
			"version": "v-any", "config": spec.Scope{},
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), server.main) {
			t.Fatalf("body = %s, want the file named", recorder.Body.String())
		}
	})

	t.Run("a scoped caller learns nothing about the file", func(t *testing.T) {
		recorder := putScope(t, server.Server, "support", testAccessToken, map[string]any{
			"version": "v-any", "config": spec.Scope{},
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		if strings.Contains(body, "unmarshal") || strings.Contains(body, server.main) {
			t.Fatalf("body = %s, want no file or parse detail disclosure", body)
		}
	})
}

func TestPublishOneScope(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, err := config.ScopeVersion(current)
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}

	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Customer support"),
	})
	if recorder.Code != http.StatusAccepted && recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 or 202: %s", recorder.Code, recorder.Body.String())
	}
	results := decodeWriteResults(t, recorder)
	if len(results.Scopes) != 1 || results.Scopes[0].Scope != "support" {
		t.Fatalf("results = %+v, want the published scope", results.Scopes)
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "Customer support" {
		t.Fatalf("display_name = %q, want the published label", got)
	}
	// The running instance has not adopted it yet, so the report says pending.
	if results.Scopes[0].Status != "pending" {
		t.Fatalf("status = %q, want pending", results.Scopes[0].Status)
	}
	if results.Scopes[0].SavedVersion == results.Scopes[0].ActiveVersion {
		t.Fatal("a saved change reported one version for both sides")
	}
}

func TestPublishKeepsASecretItNeverShowed(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, err := config.ScopeVersion(current)
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	// The editor read the scope, saw the mask, and sent the document back.
	body := readScopeConfig(t, server.Server, "/api/v1/scopes/support/config",
		testOwnerToken)
	var sent map[string]any
	if err := json.Unmarshal(body.Config, &sent); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if key := sent["llms"].(map[string]any)["primary"].(map[string]any)["api_key"]; key != "***" {
		t.Fatalf("api_key read as %v, want the mask", key)
	}
	sent["display_name"] = "Support"
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  sent,
	})
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	got := savedScope(t, server.main, "support").LLMs["primary"].APIKey.Reveal()
	if got != "sk-live-secret" {
		t.Fatalf("api_key = %q, want the saved credential", got)
	}
}

func TestPublishRefusesWithoutAVersion(t *testing.T) {
	server := newPublishServer(t)
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"config": spec.Scope{DisplayName: "Support"},
	})
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("status = %d, want 428: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPublishRefusesAStaleVersion(t *testing.T) {
	server := newPublishServer(t)
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": "v-stale",
		"config":  spec.Scope{DisplayName: "Support"},
	})
	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412: %s", recorder.Code, recorder.Body.String())
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "" {
		t.Fatalf("display_name = %q, want the file untouched", got)
	}
}

func TestPublishRefusesAnInvalidCandidate(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, err := config.ScopeVersion(current)
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	before := savedScope(t, server.main, "support")

	// A bot pointing at a graph the scope does not define.
	next := spec.Scope{Bots: map[string]spec.Bot{"demo": {Graph: "missing"}}}
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version, "config": next,
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "missing") {
		t.Fatalf("body = %s, want the offending object named", recorder.Body.String())
	}
	after := savedScope(t, server.main, "support")
	if after.DisplayName != before.DisplayName ||
		after.LLMs["primary"].APIKey.Reveal() != before.LLMs["primary"].APIKey.Reveal() {
		t.Fatal("a refused candidate changed the files")
	}
}

func TestPublishRefusesToCreateAScope(t *testing.T) {
	server := newPublishServer(t)
	recorder := putScope(t, server.Server, "nuevo", testOwnerToken, map[string]any{
		"version": "v-any",
		"config":  spec.Scope{},
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPublishAuthority(t *testing.T) {
	t.Run("a scoped token writes its own scope", func(t *testing.T) {
		server := newPublishServer(t)
		current := savedScope(t, server.main, "support")
		version, _ := config.ScopeVersion(current)
		recorder := putScope(t, server.Server, "support", testAccessToken, map[string]any{
			"version": version,
			"config":  relabelled(t, savedScope(t, server.main, "support"), "Help desk"),
		})
		if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
			t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("another scope is unknown, not forbidden", func(t *testing.T) {
		server := newPublishServer(t)
		recorder := putScope(t, server.Server, "ventas", testAccessToken, map[string]any{
			"version": "v-any", "config": spec.Scope{},
		})
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestPublishSeveralScopesAtOnce(t *testing.T) {
	server := newPublishServer(t)
	support, err := config.ScopeVersion(savedScope(t, server.main, "support"))
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	ventas, err := config.ScopeVersion(savedScope(t, server.main, "ventas"))
	if err != nil {
		t.Fatalf("ScopeVersion: %v", err)
	}
	recorder := publishScopes(t, server.Server, testOwnerToken, map[string]any{
		"scopes": map[string]any{
			"support": map[string]any{
				"version": support,
				"config": relabelled(t,
					savedScope(t, server.main, "support"), "Support desk"),
			},
			"ventas": map[string]any{
				"version": ventas,
				"config":  relabelled(t, savedScope(t, server.main, "ventas"), "Sales"),
			},
		},
	})
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "Support desk" {
		t.Fatalf("support display_name = %q", got)
	}
	if got := savedScope(t, server.main, "ventas").DisplayName; got != "Sales" {
		t.Fatalf("ventas display_name = %q", got)
	}
}

func TestPublishOneStaleScopeSavesNothing(t *testing.T) {
	server := newPublishServer(t)
	support, _ := config.ScopeVersion(savedScope(t, server.main, "support"))
	recorder := publishScopes(t, server.Server, testOwnerToken, map[string]any{
		"scopes": map[string]any{
			"support": map[string]any{
				"version": support,
				"config": relabelled(t,
					savedScope(t, server.main, "support"), "Support desk"),
			},
			"ventas": map[string]any{
				"version": "v-stale",
				"config":  relabelled(t, savedScope(t, server.main, "ventas"), "Sales"),
			},
		},
	})
	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412: %s", recorder.Code, recorder.Body.String())
	}
	if got := savedScope(t, server.main, "support").DisplayName; got != "" {
		t.Fatalf("support display_name = %q, want the whole batch refused", got)
	}
	if got := savedScope(t, server.main, "ventas").DisplayName; got != "" {
		t.Fatalf("ventas display_name = %q, want the whole batch refused", got)
	}
}

func TestPublishRefusesAForbiddenScopeInTheBatch(t *testing.T) {
	server := newPublishServer(t)
	support, _ := config.ScopeVersion(savedScope(t, server.main, "support"))
	recorder := publishScopes(t, server.Server, testAccessToken, map[string]any{
		"scopes": map[string]any{
			"support": map[string]any{
				"version": support,
				"config": relabelled(t,
					savedScope(t, server.main, "support"), "Support desk"),
			},
			"ventas": map[string]any{
				"version": "v-any",
				"config":  relabelled(t, savedScope(t, server.main, "ventas"), "Sales"),
			},
		},
	})
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
	// Nothing about the authorized scope changed either.
	if got := savedScope(t, server.main, "support").DisplayName; got != "" {
		t.Fatalf("support display_name = %q, want nothing written", got)
	}
}

func TestPublishRejectsAnEmptyBatchAndUnknownFields(t *testing.T) {
	server := newPublishServer(t)
	if recorder := publishScopes(t, server.Server, testOwnerToken,
		map[string]any{"scopes": map[string]any{}}); recorder.Code !=
		http.StatusUnprocessableEntity {
		t.Fatalf("empty batch status = %d, want 422", recorder.Code)
	}
	if recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": "v-x", "config": map[string]any{}, "extra": true,
	}); recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", recorder.Code)
	}
}

func TestPublishLeavesNoScratchFilesBehind(t *testing.T) {
	server := newPublishServer(t)
	current := savedScope(t, server.main, "support")
	version, _ := config.ScopeVersion(current)
	recorder := putScope(t, server.Server, "support", testOwnerToken, map[string]any{
		"version": version,
		"config":  relabelled(t, current, "Support desk"),
	})
	if recorder.Code != http.StatusOK && recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	entries, err := os.ReadDir(filepath.Dir(server.main))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "botflota-") {
			t.Fatalf("a scratch file survived: %s", entry.Name())
		}
	}
}

func TestPublishRejectsAMalformedBody(t *testing.T) {
	server := newPublishServer(t)
	request := httptest.NewRequest(http.MethodPut,
		"/api/v1/scopes/support/config", strings.NewReader("{not json"))
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

// TestPublishHidesProblemsOutsideTheCallersScopes holds the publication
// contract: one candidate is validated as a whole, but a scoped caller is told
// only about its own scopes and the owner reads every diagnostic
// (docs/current-plan.md).
func TestPublishHidesProblemsOutsideTheCallersScopes(t *testing.T) {
	server := testServerWithConfig(t, &fakeFleet{}, func(cfg *config.Config) {
		// A manual edit left ventas broken; the caller holds support only.
		cfg.Scopes["ventas"] = spec.Scope{
			Graphs: map[string]spec.Graph{"main": testGraph},
			Bots:   map[string]spec.Bot{"other": {Graph: "missing"}},
		}
	})
	pub := publishServer{Server: server, main: server.writer.MainPath()}
	current := savedScope(t, pub.main, "support")
	version, _ := config.ScopeVersion(current)

	t.Run("the owner reads the whole diagnostic", func(t *testing.T) {
		recorder := putScope(t, pub.Server, "support", testOwnerToken, map[string]any{
			"version": version,
			"config":  relabelled(t, current, "Customer support"),
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "ventas") {
			t.Fatalf("body = %s, want the problem named for the owner",
				recorder.Body.String())
		}
	})

	t.Run("a scoped caller learns nothing outside its scopes", func(t *testing.T) {
		recorder := putScope(t, pub.Server, "support", testAccessToken, map[string]any{
			"version": version,
			"config":  relabelled(t, current, "Customer support"),
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		body := recorder.Body.String()
		for _, secret := range []string{"ventas", "missing", pub.main} {
			if strings.Contains(body, secret) {
				t.Fatalf("body = %s, want no detail from outside its scopes", body)
			}
		}
	})

	t.Run("its own scope is still explained", func(t *testing.T) {
		next := relabelled(t, current, "Customer support")
		next.Bots = map[string]spec.Bot{"demo": {Graph: "gone"}}
		recorder := putScope(t, pub.Server, "support", testAccessToken, map[string]any{
			"version": version, "config": next,
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", recorder.Code, recorder.Body.String())
		}
		if !strings.Contains(recorder.Body.String(), "support") {
			t.Fatalf("body = %s, want its own problem reported", recorder.Body.String())
		}
	})
}
