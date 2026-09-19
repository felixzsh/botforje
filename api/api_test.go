package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

const testAdminKey = "test-admin-key"

type fakeFleet struct{ statuses []fleet.BotStatus }

func (f fakeFleet) Status() []fleet.BotStatus { return f.statuses }

type fakeAccounts struct {
	accounts map[string]botflota.LinkedAccount
}

func (f fakeAccounts) Get(
	_ context.Context, scope, bot string,
) (*botflota.LinkedAccount, error) {
	account, ok := f.accounts[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &account, nil
}

func (f fakeAccounts) List(context.Context) ([]botflota.LinkedAccount, error) {
	return nil, nil
}

func (f fakeAccounts) Put(context.Context, *botflota.LinkedAccount) error { return nil }

func (f fakeAccounts) Delete(context.Context, string, string) error { return nil }

type fakePairings struct {
	states map[string]botflota.PairingState
}

func (f fakePairings) Get(
	_ context.Context, scope, bot string,
) (*botflota.PairingStatus, error) {
	state, ok := f.states[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &botflota.PairingStatus{Scope: scope, Bot: bot, State: state}, nil
}

func (f fakePairings) Set(context.Context, *botflota.PairingStatus) error { return nil }

type fakeKeyStore struct{ key *botflota.AdminKey }

func (f *fakeKeyStore) Get(context.Context) (*botflota.AdminKey, error) {
	if f.key == nil {
		return nil, botflota.ErrNotFound
	}
	return f.key, nil
}

func (f *fakeKeyStore) Put(_ context.Context, key *botflota.AdminKey) error {
	f.key = key
	return nil
}

func hashOf(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

func testServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{
		Address: "127.0.0.1",
		Port:    3000,
		Scopes: map[string]spec.Scope{
			"support": {
				LLMs: map[string]spec.LLM{
					"primary": {
						Provider: "openai", Model: "gpt-4o-mini",
						APIKey: spec.Secret("sk-live-secret"),
					},
				},
				Bots: map[string]spec.Bot{"demo": {}},
			},
		},
	}
	accounts := fakeAccounts{accounts: map[string]botflota.LinkedAccount{
		"support/demo": {
			Scope: "support", Bot: "demo", JID: "5215500000000@s.whatsapp.net",
		},
	}}
	pairings := fakePairings{states: map[string]botflota.PairingState{
		"support/demo": botflota.PairingPaired,
	}}
	server, err := New(Options{
		Config: cfg,
		Fleet: fakeFleet{statuses: []fleet.BotStatus{
			{Scope: "support", Bot: "demo", State: "connected", OutboxDepth: 2},
		}},
		Accounts: accounts,
		Pairings: pairings,
		Auth:     &Auth{hash: hashOf(testAdminKey)},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

func get(t *testing.T, handler http.Handler, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthMiddleware(t *testing.T) {
	server := testServer(t)
	cases := []struct {
		name string
		key  string
		want int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong key", "wrong", http.StatusUnauthorized},
		{"admin key", testAdminKey, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := get(t, server.Handler(), "/api/v1/status", tc.key)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
	}
}

func TestStatusEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/status", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		UptimeSeconds int `json:"uptime_seconds"`
		Bots          []struct {
			Scope  string `json:"scope"`
			Bot    string `json:"bot"`
			State  string `json:"state"`
			Outbox int    `json:"outbox"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 1 {
		t.Fatalf("bots = %#v, want one", body.Bots)
	}
	bot := body.Bots[0]
	if bot.Scope != "support" || bot.Bot != "demo" ||
		bot.State != "connected" || bot.Outbox != 2 {
		t.Fatalf("bot = %#v", bot)
	}
	if body.UptimeSeconds < 0 {
		t.Fatalf("uptime_seconds = %d", body.UptimeSeconds)
	}
}

func TestBotsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/bots", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Bots []struct {
			Scope   string `json:"scope"`
			Bot     string `json:"bot"`
			JID     string `json:"jid"`
			Pairing string `json:"pairing"`
			State   string `json:"state"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 1 {
		t.Fatalf("bots = %#v, want one", body.Bots)
	}
	bot := body.Bots[0]
	if bot.Scope != "support" || bot.Bot != "demo" ||
		bot.JID != "5215500000000@s.whatsapp.net" ||
		bot.Pairing != string(botflota.PairingPaired) || bot.State != "connected" {
		t.Fatalf("bot = %#v", bot)
	}
}

func TestConfigEndpointMasksSecrets(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/config", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "sk-live-secret") {
		t.Fatal("config response leaked the API key")
	}
	if !strings.Contains(body, `"***"`) {
		t.Fatalf("masked secret missing from %s", body)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	server := testServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	request.Header.Set("Authorization", "Bearer "+testAdminKey)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

func TestEnsureAuthCreatesKeyFile(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := &fakeKeyStore{}

	auth, created, err := EnsureAuth(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	if !created || store.key == nil {
		t.Fatal("EnsureAuth did not create a key")
	}
	path := filepath.Join(dataDir, KeyFile)
	clear, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if hash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(hash, store.key.Hash) {
		t.Fatal("stored hash does not match the key file")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key file mode = %o, want 600", mode)
	}

	again, created, err := EnsureAuth(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureAuth again: %v", err)
	}
	if created {
		t.Fatal("EnsureAuth created a second key")
	}
	if !bytes.Equal(again.hash, auth.hash) {
		t.Fatal("EnsureAuth did not reuse the stored key")
	}
}

func TestRotateReplacesKey(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := &fakeKeyStore{}
	first, _, err := EnsureAuth(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	second, err := Rotate(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if bytes.Equal(first.hash, second.hash) {
		t.Fatal("Rotate kept the same key")
	}
	clear, err := os.ReadFile(filepath.Join(dataDir, KeyFile))
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if hash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(hash, second.hash) {
		t.Fatal("key file does not match the rotated key")
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"config", Options{}},
		{"fleet", Options{Config: &config.Config{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.opts); err == nil {
				t.Fatal("New accepted incomplete options")
			}
		})
	}
}
