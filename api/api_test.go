package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/fleet"
	"github.com/felixzsh/botflota/spec"
)

const (
	testAdminKey   = "test-admin-key"
	testScopeKey   = "test-scope-key"
	testDeadKey    = "test-revoked-key"
	testExpiredKey = "test-expired-key"
)

var testTime = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

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

// fakeKeys is the token store: it matches hashes like the real store, assigns
// ids, and records revocation.
type fakeKeys struct {
	keys   []botflota.APIKey
	nextID int64
}

func (f *fakeKeys) Create(_ context.Context, key *botflota.APIKey) error {
	f.nextID++
	key.ID = f.nextID
	f.keys = append(f.keys, *key)
	return nil
}

func (f *fakeKeys) FindByHash(
	_ context.Context, hash []byte,
) (*botflota.APIKey, error) {
	for _, key := range f.keys {
		if bytes.Equal(key.Hash, hash) {
			found := key
			return &found, nil
		}
	}
	return nil, botflota.ErrNotFound
}

func (f *fakeKeys) List(context.Context) ([]botflota.APIKey, error) {
	keys := make([]botflota.APIKey, 0, len(f.keys))
	for _, key := range f.keys {
		key.Hash = nil
		keys = append(keys, key)
	}
	return keys, nil
}

func (f *fakeKeys) Revoke(_ context.Context, id int64, at time.Time) error {
	for i := range f.keys {
		if f.keys[i].ID == id {
			if f.keys[i].RevokedAt.IsZero() {
				f.keys[i].RevokedAt = at
			}
			return nil
		}
	}
	return botflota.ErrNotFound
}

type fakeSenders struct{ senders map[string]botflota.Sender }

func (f fakeSenders) Get(_ context.Context, scope, phone string) (*botflota.Sender, error) {
	sender, ok := f.senders[scope+"/"+phone]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &sender, nil
}

func (f fakeSenders) Upsert(
	context.Context, string, string, string, time.Time,
) (*botflota.Sender, bool, error) {
	return nil, false, nil
}

type fakeChats struct {
	chats map[string]botflota.ChatSummary
}

func (f fakeChats) Ensure(
	context.Context, string, string, int64, time.Time,
) (*botflota.Chat, error) {
	return nil, nil
}

func (f fakeChats) ForSender(
	_ context.Context, scope, bot string, senderID int64,
) (*botflota.Chat, error) {
	for _, summary := range f.chats {
		if summary.Scope == scope && summary.Bot == bot && summary.SenderID == senderID {
			chat := summary.Chat
			return &chat, nil
		}
	}
	return nil, botflota.ErrNotFound
}

func (f fakeChats) Recent(
	_ context.Context, scope, bot string, _ int,
) ([]botflota.ChatSummary, error) {
	var chats []botflota.ChatSummary
	for _, summary := range f.chats {
		if summary.Scope == scope && (bot == "" || summary.Bot == bot) {
			chats = append(chats, summary)
		}
	}
	slices.SortFunc(chats, func(a, b botflota.ChatSummary) int {
		return int(a.ID - b.ID)
	})
	return chats, nil
}

func (f fakeChats) RecordMessage(context.Context, int64, time.Time) error { return nil }

func (f fakeChats) SaveGuards(
	context.Context, int64, map[string]botflota.GuardState,
) error {
	return nil
}

type fakeSessions struct{ sessions map[int64]botflota.Session }

func (f fakeSessions) Latest(_ context.Context, chatID int64) (*botflota.Session, error) {
	session, ok := f.sessions[chatID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &session, nil
}

func (f fakeSessions) Save(context.Context, *botflota.Session) error { return nil }

func (f fakeSessions) Delete(context.Context, int64) error { return nil }

type fakeMessages struct{ messages map[int64][]botflota.Message }

func (f fakeMessages) Append(context.Context, *botflota.Message) error { return nil }

func (f fakeMessages) List(
	_ context.Context, chatID int64, limit int,
) ([]botflota.Message, error) {
	messages := f.messages[chatID]
	if len(messages) > limit {
		messages = messages[:limit]
	}
	return messages, nil
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
			"ventas": {
				Bots: map[string]spec.Bot{"other": {}},
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
	sender := botflota.Sender{
		ID: 7, Scope: "support", Phone: "5215500000000", DisplayName: "Ana",
	}
	chat := botflota.ChatSummary{
		Chat: botflota.Chat{
			ID: 3, Scope: "support", Bot: "demo", SenderID: sender.ID,
			StartedAt: testTime, UpdatedAt: testTime, MessageCount: 2,
		},
		Phone: sender.Phone, DisplayName: sender.DisplayName,
	}
	quiet := botflota.Sender{
		ID: 8, Scope: "support", Phone: "5215599999999", DisplayName: "Luis",
	}
	quietChat := botflota.ChatSummary{
		Chat: botflota.Chat{
			ID: 4, Scope: "support", Bot: "demo", SenderID: quiet.ID,
			StartedAt: testTime, UpdatedAt: testTime,
		},
		Phone: quiet.Phone, DisplayName: quiet.DisplayName,
	}
	keys := &fakeKeys{nextID: 3, keys: []botflota.APIKey{
		{
			ID: 1, Name: "scoped", Hash: hashOf(testScopeKey),
			Scopes: []string{"support"}, CreatedAt: testTime,
		},
		{
			ID: 2, Name: "revoked", Hash: hashOf(testDeadKey),
			Scopes: []string{"support"}, CreatedAt: testTime,
			RevokedAt: testTime,
		},
		{
			ID: 3, Name: "expired", Hash: hashOf(testExpiredKey),
			Scopes: []string{"support"}, CreatedAt: testTime,
			ExpiresAt: time.Now().Add(-time.Hour),
		},
	}}
	server, err := New(Options{
		Config: cfg,
		Fleet: fakeFleet{statuses: []fleet.BotStatus{
			{Scope: "support", Bot: "demo", State: "connected", OutboxDepth: 2},
			{Scope: "ventas", Bot: "other", State: "connected", OutboxDepth: 1},
		}},
		Accounts: accounts,
		Pairings: pairings,
		Senders: fakeSenders{senders: map[string]botflota.Sender{
			"support/" + sender.Phone: sender,
			"support/" + quiet.Phone:  quiet,
		}},
		Chats: fakeChats{chats: map[string]botflota.ChatSummary{
			"support/demo/7": chat,
			"support/demo/8": quietChat,
		}},
		Sessions: fakeSessions{sessions: map[int64]botflota.Session{
			chat.ID: {
				ID: 9, ChatID: chat.ID, Node: "menu",
				VisitedNodes: []string{"menu"},
				CreatedAt:    testTime, UpdatedAt: testTime,
				ExpiresAt: testTime.Add(time.Hour),
			},
		}},
		Messages: fakeMessages{messages: map[int64][]botflota.Message{
			chat.ID: {
				{
					ID: 1, ChatID: chat.ID, Direction: botflota.Inbound,
					Kind: botflota.KindText, Body: "hola", At: testTime,
				},
				{
					ID: 2, ChatID: chat.ID, SessionID: 9,
					Direction: botflota.Outbound, Kind: botflota.KindText,
					Body: "hi", At: testTime,
				},
			},
		}},
		Keys: keys,
		Auth: NewAuth(hashOf(testAdminKey), keys),
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
	if len(body.Bots) != 2 {
		t.Fatalf("bots = %#v, want two", body.Bots)
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
	if len(body.Bots) != 2 {
		t.Fatalf("bots = %#v, want two", body.Bots)
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

func TestChatsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(), "/api/v1/scopes/support/chats?bot=demo", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Chats []struct {
			ID     int64  `json:"id"`
			Scope  string `json:"scope"`
			Bot    string `json:"bot"`
			Sender struct {
				Phone string `json:"phone"`
				Name  string `json:"name"`
			} `json:"sender"`
			Messages int64 `json:"messages"`
		} `json:"chats"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Chats) != 2 {
		t.Fatalf("chats = %#v, want two", body.Chats)
	}
	chat := body.Chats[0]
	if chat.ID != 3 || chat.Scope != "support" || chat.Bot != "demo" ||
		chat.Sender.Phone != "5215500000000" || chat.Sender.Name != "Ana" ||
		chat.Messages != 2 {
		t.Fatalf("chat = %#v", chat)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/messages?bot=demo&sender=5215500000000&limit=1",
		testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Scope    string `json:"scope"`
		Bot      string `json:"bot"`
		ChatID   int64  `json:"chat_id"`
		Messages []struct {
			ID        int64  `json:"id"`
			Direction string `json:"direction"`
			Body      string `json:"body"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "support" || body.Bot != "demo" || body.ChatID != 3 {
		t.Fatalf("messages envelope = %#v", body)
	}
	if len(body.Messages) != 1 || body.Messages[0].Body != "hola" ||
		body.Messages[0].Direction != string(botflota.Inbound) {
		t.Fatalf("messages = %#v", body.Messages)
	}
}

func TestSessionsEndpoint(t *testing.T) {
	server := testServer(t)
	recorder := get(t, server.Handler(),
		"/api/v1/scopes/support/sessions?bot=demo&sender=5215500000000", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Session struct {
			ID   int64  `json:"id"`
			Node string `json:"node"`
		} `json:"session"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Session.ID != 9 || body.Session.Node != "menu" {
		t.Fatalf("session = %#v", body.Session)
	}
}

func TestQueryErrors(t *testing.T) {
	server := testServer(t)
	cases := []struct {
		name string
		path string
		want int
	}{
		{"unknown scope", "/api/v1/scopes/nope/chats", http.StatusNotFound},
		{"unknown bot", "/api/v1/scopes/support/chats?bot=nope", http.StatusNotFound},
		{"bad limit", "/api/v1/scopes/support/chats?limit=zero", http.StatusBadRequest},
		{"missing sender",
			"/api/v1/scopes/support/messages?bot=demo", http.StatusBadRequest},
		{"unknown sender",
			"/api/v1/scopes/support/messages?bot=demo&sender=000", http.StatusNotFound},
		{"no session",
			"/api/v1/scopes/support/sessions?bot=demo&sender=5215599999999",
			http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := get(t, server.Handler(), tc.path, testAdminKey)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.want)
			}
		})
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

	hash, created, err := EnsureAuth(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureAuth: %v", err)
	}
	if !created || store.key == nil {
		t.Fatal("EnsureAuth did not create a key")
	}
	if !bytes.Equal(hash, store.key.Hash) {
		t.Fatal("returned hash does not match the stored key")
	}
	path := filepath.Join(dataDir, KeyFile)
	clear, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if fileHash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(fileHash, hash) {
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
	if !bytes.Equal(again, hash) {
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
	if bytes.Equal(first, second) {
		t.Fatal("Rotate kept the same key")
	}
	clear, err := os.ReadFile(filepath.Join(dataDir, KeyFile))
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if fileHash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(fileHash, second) {
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

func TestScopedTokenIsolation(t *testing.T) {
	server := testServer(t)
	handler := server.Handler()

	recorder := get(t, handler, "/api/v1/scopes/support/chats", testScopeKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("own scope status = %d, want 200", recorder.Code)
	}
	// Another configured scope answers 404, never 403, so a scoped principal
	// cannot infer that it exists (ROADMAP Phase 13).
	recorder = get(t, handler, "/api/v1/scopes/ventas/chats", testScopeKey)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("other scope status = %d, want 404", recorder.Code)
	}
	for _, path := range []string{"/api/v1/config", "/api/v1/keys"} {
		recorder = get(t, handler, path, testScopeKey)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", path, recorder.Code)
		}
	}
	recorder = get(t, handler, "/api/v1/bots", testScopeKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bots status = %d, want 200", recorder.Code)
	}
	var body struct {
		Bots []struct {
			Scope string `json:"scope"`
		} `json:"bots"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Bots) != 1 || body.Bots[0].Scope != "support" {
		t.Fatalf("bots = %#v, want only support", body.Bots)
	}
}

func TestRejectedTokens(t *testing.T) {
	server := testServer(t)
	for _, key := range []string{testDeadKey, testExpiredKey} {
		recorder := get(t, server.Handler(), "/api/v1/status", key)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status with %q = %d, want 401", key, recorder.Code)
		}
	}
}

func TestKeysEndpoints(t *testing.T) {
	server := testServer(t)
	handler := server.Handler()

	body := strings.NewReader(`{"name":"ci","scopes":["support"]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/keys", body)
	request.Header.Set("Authorization", "Bearer "+testAdminKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID     int64    `json:"id"`
		Name   string   `json:"name"`
		Key    string   `json:"key"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID != 4 || created.Name != "ci" || created.Key == "" {
		t.Fatalf("created = %#v", created)
	}

	recorder = get(t, handler, "/api/v1/scopes/support/chats", created.Key)
	if recorder.Code != http.StatusOK {
		t.Fatalf("minted key status = %d, want 200", recorder.Code)
	}
	recorder = get(t, handler, "/api/v1/scopes/ventas/chats", created.Key)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("minted key other scope = %d, want 404", recorder.Code)
	}

	recorder = get(t, handler, "/api/v1/keys", testAdminKey)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), created.Key) {
		t.Fatal("key list exposed a clear key")
	}

	request = httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/keys/%d", created.ID), nil)
	request.Header.Set("Authorization", "Bearer "+testAdminKey)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", recorder.Code)
	}
	recorder = get(t, handler, "/api/v1/scopes/support/chats", created.Key)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d, want 401", recorder.Code)
	}
}

func TestKeyMintValidation(t *testing.T) {
	server := testServer(t)
	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed body", `{`, http.StatusBadRequest},
		{"no name", `{"scopes":["support"]}`, http.StatusBadRequest},
		{"no scopes", `{"name":"x"}`, http.StatusBadRequest},
		{"unknown scope", `{"name":"x","scopes":["nope"]}`, http.StatusBadRequest},
		{"duplicate scope", `{"name":"x","scopes":["support","support"]}`,
			http.StatusBadRequest},
		{"past expiry",
			`{"name":"x","scopes":["support"],"expires_at":"2000-01-01T00:00:00Z"}`,
			http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/keys",
				strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+testAdminKey)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want,
					recorder.Body)
			}
		})
	}
}
