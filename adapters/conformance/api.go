package conformance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/api"
	"github.com/felixzsh/botflota/config"
)

// apiFixture is the real API server over the fixture's live fleet and stores,
// with an owner principal so every scoped route is reachable.
type apiFixture struct {
	t       *testing.T
	handler http.Handler
}

func newAPIFixture(t *testing.T, s Subject, f *fixture) *apiFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "botflota.yml")
	if err := f.cfg.WriteFile(path); err != nil {
		t.Fatalf("conformance: write config: %v", err)
	}
	writer, err := config.NewWriter(path, mustCatalog(t, s.Install(f.db.Bindings())))
	if err != nil {
		t.Fatalf("conformance: config writer: %v", err)
	}
	server, err := api.New(api.Options{
		Config: config.NewSource(f.cfg), Saved: config.NewSource(f.cfg),
		Writer: writer, Fleet: f.fleet, Setups: f.fleet,
		Bindings: f.db.Bindings(), Peers: f.db.Peers(),
		Directory: f.db.Directory(), Chats: f.db.Chats(),
		Sessions: f.db.Sessions(), Messages: f.db.Messages(),
		Sends: f.db.Sends(), Channels: mustCatalog(t, s.Install(f.db.Bindings())),
		History: f.db.History(), Tokens: f.db.AccessTokens(),
		Resolver: ownerResolver{}, Log: botflota.DiscardLogger(),
	})
	if err != nil {
		t.Fatalf("conformance: build API server: %v", err)
	}
	return &apiFixture{t: t, handler: server.Handler()}
}

// ownerResolver grants the suite's requests the instance owner, so the checks
// exercise handlers rather than the credential layer.
type ownerResolver struct{}

func (ownerResolver) Resolve(*http.Request) (*api.Principal, error) {
	return &api.Principal{Kind: api.KindOwner, Key: scopeName}, nil
}

func (a *apiFixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	return a.do(t, http.MethodGet, path, "")
}

func (a *apiFixture) post(
	t *testing.T, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	return a.do(t, http.MethodPost, path, body)
}

func (a *apiFixture) do(
	t *testing.T, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequestWithContext(
		context.Background(), method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	a.handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("conformance: decode response: %v: %s", err, data)
	}
}
