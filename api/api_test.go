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
	"sync"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

const (
	testOwnerToken   = "test-owner-token"
	testAccessToken  = "test-access-token"
	testRevokedToken = "test-revoked-token"
	testExpiredToken = "test-expired-token"
)

var testTime = time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)

// testGraph is a minimal valid graph, so the fixture configuration passes the
// validation a publication runs before it writes.
var testGraph = spec.Graph{
	Root:  "hello",
	Nodes: map[string]spec.Node{"hello": {Goal: "greet"}},
}

type fakeFleet struct {
	statuses []botflota.BotStatus
	stopped  []string
	// onStop, when set, runs inside StopBot, so a test can hold an unpair
	// open while it checks the operation lock.
	onStop func()
}

func (f *fakeFleet) Status() []botflota.BotStatus { return f.statuses }

func (f *fakeFleet) StopBotForUnpair(scope, bot string) error {
	if f.onStop != nil {
		f.onStop()
	}
	f.stopped = append(f.stopped, scope+"/"+bot)
	return nil
}

// fakeUnpairer records the links the handler dropped; with the account store
// it also removes the record, the way the real unpairer does.
type fakeUnpairer struct {
	calls    []string
	err      error
	accounts *fakeAccounts
}

func (f *fakeUnpairer) UnpairAccount(
	ctx context.Context, scope, bot string,
) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, scope+"/"+bot)
	if f.accounts != nil {
		if err := f.accounts.Delete(ctx, scope, bot); err != nil {
			return err
		}
	}
	return nil
}

type fakeAccounts struct {
	accounts map[string]botflota.Binding
}

func (f *fakeAccounts) Get(
	_ context.Context, scope, bot string,
) (*botflota.Binding, error) {
	account, ok := f.accounts[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &account, nil
}

func (f *fakeAccounts) List(context.Context) ([]botflota.Binding, error) {
	return nil, nil
}

func (f *fakeAccounts) Commit(context.Context, *botflota.Binding) error {
	return nil
}

func (f *fakeAccounts) Delete(_ context.Context, scope, bot string) error {
	delete(f.accounts, scope+"/"+bot)
	return nil
}

type fakePairings struct {
	states map[string]botflota.PairingState
	// changed is optional: a state without a timestamp reports none.
	changed map[string]time.Time
}

func (f fakePairings) Get(
	_ context.Context, scope, bot string,
) (*botflota.PairingStatus, error) {
	state, ok := f.states[scope+"/"+bot]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &botflota.PairingStatus{
		Scope: scope, Bot: bot, State: state,
		LastChange: f.changed[scope+"/"+bot],
	}, nil
}

func (f fakePairings) Set(context.Context, *botflota.PairingStatus) error { return nil }

type fakePairing struct {
	session   botflota.PairingSession
	present   bool
	cancelled bool
	ended     bool
	started   botflota.PairRequest
}

func (f *fakePairing) StartPair(
	_ context.Context, scope, bot string, req botflota.PairRequest,
) (*botflota.PairingSession, error) {
	if !f.present {
		return nil, botflota.ErrBusy
	}
	f.started = req
	session := f.session
	session.Scope, session.Bot = scope, bot
	return &session, nil
}

func (f *fakePairing) CurrentPair(
	_ context.Context, scope, bot string,
) (*botflota.PairingSession, error) {
	if !f.present {
		return nil, botflota.ErrNotFound
	}
	session := f.session
	session.Scope, session.Bot = scope, bot
	return &session, nil
}

func (f *fakePairing) CancelPair(context.Context, string, string) error {
	if !f.present {
		return botflota.ErrNotFound
	}
	f.cancelled = true
	return nil
}

func (f *fakePairing) EndPair(context.Context, string, string) error {
	f.ended = true
	return nil
}

type fakeOwnerTokens struct{ token *botflota.OwnerToken }

func (f *fakeOwnerTokens) Get(context.Context) (*botflota.OwnerToken, error) {
	if f.token == nil {
		return nil, botflota.ErrNotFound
	}
	return f.token, nil
}

func (f *fakeOwnerTokens) Put(_ context.Context, token *botflota.OwnerToken) error {
	f.token = token
	return nil
}

type fakeAccessTokens struct {
	tokens []botflota.AccessToken
	nextID int64
}

func (f *fakeAccessTokens) Create(_ context.Context, token *botflota.AccessToken) error {
	f.nextID++
	token.ID = f.nextID
	f.tokens = append(f.tokens, *token)
	return nil
}

func (f *fakeAccessTokens) FindByHash(
	_ context.Context, hash []byte,
) (*botflota.AccessToken, error) {
	for _, token := range f.tokens {
		if bytes.Equal(token.Hash, hash) {
			found := token
			return &found, nil
		}
	}
	return nil, botflota.ErrNotFound
}

func (f *fakeAccessTokens) List(context.Context) ([]botflota.AccessToken, error) {
	tokens := make([]botflota.AccessToken, 0, len(f.tokens))
	for _, token := range f.tokens {
		token.Hash = nil
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func (f *fakeAccessTokens) Revoke(_ context.Context, id int64, at time.Time) error {
	for i := range f.tokens {
		if f.tokens[i].ID == id {
			if f.tokens[i].RevokedAt.IsZero() {
				f.tokens[i].RevokedAt = at
			}
			return nil
		}
	}
	return botflota.ErrNotFound
}

type fakeDirectory struct{ entries []botflota.DirectoryEntry }

func (f fakeDirectory) Put(context.Context, *botflota.DirectoryEntry) error { return nil }

func (f fakeDirectory) Merge(context.Context, *botflota.DirectoryEntry) error {
	return nil
}

func (f fakeDirectory) List(
	_ context.Context, scope, bot string, limit int,
) ([]botflota.DirectoryEntry, error) {
	out := make([]botflota.DirectoryEntry, 0, len(f.entries))
	for _, entry := range f.entries {
		if entry.Scope == scope && entry.Bot == bot {
			out = append(out, entry)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fakePeers serves the scope's identities and records the last list read,
// so a test can assert the query parameters reached the store.
type fakePeers struct {
	contacts   map[string]botflota.Peer
	listPage   botflota.PeerPage
	listErr    error
	lastSince  time.Time
	lastLimit  int
	lastCursor string
}

func (f fakePeers) Get(
	_ context.Context, scope string, key botflota.IdentityKey,
) (*botflota.Peer, error) {
	contact, ok := f.contacts[scope+"/"+key.Subject]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &contact, nil
}

func (f fakePeers) EnsurePeer(
	context.Context, string, botflota.IdentityKey, time.Time,
) (*botflota.Peer, error) {
	return nil, nil
}

func (f fakePeers) RecordInbound(
	context.Context, string, botflota.IdentityKey, string, time.Time,
) (*botflota.Peer, bool, error) {
	return nil, false, nil
}

func (f fakePeers) SetDisplayName(
	context.Context, string, botflota.IdentityKey, string, time.Time,
) error {
	return nil
}

func (f *fakePeers) List(
	_ context.Context, _ string, since time.Time, limit int, cursor string,
) (botflota.PeerPage, error) {
	f.lastSince, f.lastLimit, f.lastCursor = since, limit, cursor
	if f.listErr != nil {
		return botflota.PeerPage{}, f.listErr
	}
	return f.listPage, nil
}

type fakeChats struct {
	chats map[string]botflota.ChatSummary
}

func (f fakeChats) Ensure(
	context.Context, string, string, botflota.ChatTarget, int64, time.Time,
) (*botflota.Chat, error) {
	return nil, nil
}

func (f fakeChats) ForTarget(
	_ context.Context, scope, bot string, target botflota.ChatTarget,
) (*botflota.Chat, error) {
	for _, summary := range f.chats {
		if summary.Scope == scope && summary.Bot == bot && summary.Target == target {
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

func (f fakeChats) NoteHistory(context.Context, int64, time.Time, time.Time) error {
	return nil
}

func (f fakeChats) SaveGuards(
	context.Context, int64, map[string]botflota.GuardState,
) error {
	return nil
}

type fakeSessions struct {
	sessions map[int64]botflota.Session
	active   map[string]int
}

func (f fakeSessions) Latest(_ context.Context, chatID int64) (*botflota.Session, error) {
	session, ok := f.sessions[chatID]
	if !ok {
		return nil, botflota.ErrNotFound
	}
	return &session, nil
}

func (f fakeSessions) Save(context.Context, *botflota.Session) error { return nil }

func (f fakeSessions) Delete(context.Context, int64) error { return nil }

func (f fakeSessions) CountActive(
	_ context.Context, scope, bot string, _ time.Time,
) (int, error) {
	return f.active[scope+"/"+bot], nil
}

func (fakeSessions) HasActiveSessionOutsideNodes(
	context.Context, string, string, []string, time.Time,
) (bool, error) {
	return false, nil
}

type fakeMessages struct{ messages map[int64][]botflota.Message }

func (f fakeMessages) Append(context.Context, *botflota.Message) error { return nil }

func (f fakeMessages) HasProviderID(
	context.Context, string, string, botflota.ChatTarget, string,
) (bool, error) {
	return false, nil
}

func (f fakeMessages) Oldest(_ context.Context, chatID int64) (*botflota.Message, error) {
	var oldest *botflota.Message
	for i := range f.messages[chatID] {
		message := f.messages[chatID][i]
		if message.ProviderMessageID == "" {
			continue
		}
		if oldest == nil || message.At.Before(oldest.At) {
			copied := message
			oldest = &copied
		}
	}
	if oldest == nil {
		return nil, botflota.ErrNotFound
	}
	return oldest, nil
}

func (f fakeMessages) History(
	_ context.Context, chatID, afterID int64, limit int,
) ([]botflota.Message, error) {
	if afterID > 0 {
		var out []botflota.Message
		for _, message := range f.messages[chatID] {
			if message.ID > afterID && len(out) < limit {
				out = append(out, message)
			}
		}
		return out, nil
	}
	messages := f.messages[chatID]
	if len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages, nil
}

func hashOf(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}

// writeTestConfig saves a config as the files a writer would change and returns
// the main file's path, so a test that publishes changes real files.
func writeTestConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	// The fixture carries a ${API_TOKEN} reference, so the writer can load what
	// it is about to rewrite.
	t.Setenv("API_TOKEN", "header-secret")
	path := filepath.Join(t.TempDir(), "botflota.yml")
	if err := cfg.WriteFile(path); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func testServer(t *testing.T) *Server {
	t.Helper()
	return testServerWithFleet(t, &fakeFleet{statuses: []botflota.BotStatus{
		{Scope: "support", Bot: "demo", State: "connected", OutboxDepth: 2},
		{Scope: "ventas", Bot: "other", State: "connected", OutboxDepth: 1},
	}})
}

func testServerWithFleet(t *testing.T, live Fleet) *Server {
	return testServerWithConfig(t, live, nil)
}

// testServerWithConfig builds the standard server; mutate adjusts the config
// before New, so a test can exercise an instance setting.
func testServerWithConfig(
	t *testing.T, live Fleet, mutate func(*config.Config),
) *Server {
	t.Helper()
	return testServerWithOptions(t, live, nil, mutate)
}

// testServerWithOptions additionally adjusts the options before New, so a test
// can replace or drop an optional dependency.
func testServerWithOptions(
	t *testing.T, live Fleet, adjust func(*Options), mutate func(*config.Config),
) *Server {
	t.Helper()
	cfg := &config.Config{
		Address: "127.0.0.1",
		Port:    3000,
		Scopes: map[string]spec.Scope{
			"support": {
				LLMs: map[string]spec.LLM{
					"primary": {
						Provider: "openai", Model: "gpt-4o-mini",
						APIKey: spec.NewSecret("sk-live-secret"),
					},
				},
				Actions: map[string]spec.Action{
					"ping": {Steps: []spec.Step{{Request: &spec.Request{
						Name: "ping", URL: "http://example.test",
						Headers: map[string]spec.Secret{
							"Authorization": spec.NewSecretRef(
								"Bearer ${API_TOKEN}", "Bearer sk-live-header"),
						},
					}}}},
				},
				Graphs: map[string]spec.Graph{"main": testGraph},
				Bots:   map[string]spec.Bot{"demo": {Graph: "main", Channel: testChannel()}},
			},
			"ventas": {
				Graphs: map[string]spec.Graph{"main": testGraph},
				Bots:   map[string]spec.Bot{"other": {Graph: "main", Channel: testChannel()}},
			},
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	opts := testOptions(t, live, cfg)
	if adjust != nil {
		adjust(&opts)
	}
	server, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

// testOptions assembles the standard dependencies around one config, so a
// test can call New itself when it wants to observe a refused startup.
func testOptions(t *testing.T, live Fleet, cfg *config.Config) Options {
	t.Helper()
	writer, err := config.NewWriter(writeTestConfig(t, cfg), testCatalog(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	accounts := fakeAccounts{accounts: map[string]botflota.Binding{
		"support/demo": {
			Scope: "support", Bot: "demo",
			Identity: waKey("5215500000000"),
		},
	}}
	pairings := fakePairings{
		states: map[string]botflota.PairingState{
			"support/demo": botflota.PairingPaired,
		},
		changed: map[string]time.Time{"support/demo": testTime},
	}
	contact := botflota.Peer{
		ID: 7, Scope: "support", Key: waKey("5215500000000"), DisplayName: "Ana",
	}
	chat := botflota.ChatSummary{
		Chat: botflota.Chat{
			ID: 3, Scope: "support", Bot: "demo", PeerID: contact.ID,
			Target:    waTarget("5215500000000"),
			StartedAt: testTime, UpdatedAt: testTime, MessageCount: 2,
		},
		PeerKey: contact.Key, DisplayName: contact.DisplayName,
		Messages: 5, Sync: 3, Live: 2,
	}
	quiet := botflota.Peer{
		ID: 8, Scope: "support", Key: waKey("5215599999999"), DisplayName: "Luis",
	}
	quietChat := botflota.ChatSummary{
		Chat: botflota.Chat{
			ID: 4, Scope: "support", Bot: "demo", PeerID: quiet.ID,
			Target:    waTarget("5215599999999"),
			StartedAt: testTime, UpdatedAt: testTime,
		},
		PeerKey: quiet.Key, DisplayName: quiet.DisplayName,
	}
	tokens := &fakeAccessTokens{nextID: 3, tokens: []botflota.AccessToken{
		{
			ID: 1, Name: "scoped", Hash: hashOf(testAccessToken),
			Scopes: []string{"support"}, CreatedAt: testTime,
		},
		{
			ID: 2, Name: "revoked", Hash: hashOf(testRevokedToken),
			Scopes: []string{"support"}, CreatedAt: testTime,
			RevokedAt: testTime,
		},
		{
			ID: 3, Name: "expired", Hash: hashOf(testExpiredToken),
			Scopes: []string{"support"}, CreatedAt: testTime,
			ExpiresAt: time.Now().Add(-time.Hour),
		},
	}}
	return Options{
		Config:   config.NewSource(cfg),
		Saved:    config.NewSource(cfg),
		Writer:   writer,
		Fleet:    live,
		Bindings: &accounts,
		Pairings: pairings,
		Peers: &fakePeers{contacts: map[string]botflota.Peer{
			"support/" + contact.Key.Subject: contact,
			"support/" + quiet.Key.Subject:   quiet,
		}},
		Directory: fakeDirectory{entries: []botflota.DirectoryEntry{
			{
				Scope: "support", Bot: "demo", PeerKey: waKey("5215500000001"),
				SourceIdentity: waKey("5215500000001"), DisplayName: "Ana",
				UpdatedAt: testTime,
			},
			{
				Scope: "support", Bot: "demo", PeerKey: waKey("5215500000002"),
				SourceIdentity: waKey("5215500000002"), DisplayName: "Beto",
				UpdatedAt: testTime,
			},
			{
				Scope: "ventas", Bot: "other", PeerKey: waKey("5215500000003"),
				SourceIdentity: waKey("5215500000003"), DisplayName: "Cora",
				UpdatedAt: testTime,
			},
		}},
		Chats: fakeChats{chats: map[string]botflota.ChatSummary{
			"support/demo/7": chat,
			"support/demo/8": quietChat,
		}},
		Sessions: fakeSessions{
			sessions: map[int64]botflota.Session{
				chat.ID: {
					ID: 9, ChatID: chat.ID, Node: "menu",
					VisitedNodes: []string{"menu"},
					CreatedAt:    testTime, UpdatedAt: testTime,
					ExpiresAt: testTime.Add(time.Hour),
				},
			},
			active: map[string]int{"support/demo": 1},
		},
		Messages: fakeMessages{messages: map[int64][]botflota.Message{
			chat.ID: {
				{
					ID: 1, ChatID: chat.ID, Direction: botflota.Inbound,
					Content: botflota.TextContent{Body: "hola"}, At: testTime,
					Ingest: botflota.IngestLive, Routing: botflota.RoutingTriggerExact,
				},
				{
					ID: 2, ChatID: chat.ID, SessionID: 9,
					Direction: botflota.Outbound,
					Content:   botflota.TextContent{Body: "hi"}, At: testTime,
					Ingest: botflota.IngestLive, Origin: botflota.OriginPreset,
					Routing: botflota.RoutingTriggerExact,
				},
			},
		}},
		Tokens: tokens,
		Pairing: &fakePairing{
			present: true,
			session: botflota.PairingSession{
				Method: "qr", State: botflota.PairWaiting, Code: "qr-code",
				StartedAt: testTime, UpdatedAt: testTime,
				ExpiresAt: testTime.Add(time.Hour),
			},
		},
		Unpairer: &fakeUnpairer{accounts: &accounts},
		Sends:    &fakeSends{},
		Resolver: NewAuth(hashOf(testOwnerToken), tokens),
	}
}

func get(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, handler, http.MethodGet, path, token)
}

func post(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, handler, http.MethodPost, path, token)
}

func do(
	t *testing.T, handler http.Handler, method, path, token string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
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
		{"missing token", "", http.StatusUnauthorized},
		{"wrong token", "wrong", http.StatusUnauthorized},
		{"owner token", testOwnerToken, http.StatusOK},
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

func TestMethodNotAllowed(t *testing.T) {
	server := testServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", recorder.Code)
	}
}

func TestEnsureOwnerTokenCreatesFile(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := &fakeOwnerTokens{}

	hash, created, err := EnsureOwnerToken(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureOwnerToken: %v", err)
	}
	if !created || store.token == nil {
		t.Fatal("EnsureOwnerToken did not create a token")
	}
	if !bytes.Equal(hash, store.token.Hash) {
		t.Fatal("returned hash does not match the stored token")
	}
	path := filepath.Join(dataDir, OwnerTokenFile)
	clear, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if fileHash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(fileHash, hash) {
		t.Fatal("stored hash does not match the token file")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token file: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("token file mode = %o, want 600", mode)
	}

	again, created, err := EnsureOwnerToken(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureOwnerToken again: %v", err)
	}
	if created {
		t.Fatal("EnsureOwnerToken created a second token")
	}
	if !bytes.Equal(again, hash) {
		t.Fatal("EnsureOwnerToken did not reuse the stored token")
	}
}

func TestRotateOwnerTokenReplacesToken(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := &fakeOwnerTokens{}
	first, _, err := EnsureOwnerToken(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("EnsureOwnerToken: %v", err)
	}
	second, err := RotateOwnerToken(ctx, store, dataDir)
	if err != nil {
		t.Fatalf("RotateOwnerToken: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("RotateOwnerToken kept the same token")
	}
	clear, err := os.ReadFile(filepath.Join(dataDir, OwnerTokenFile))
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if fileHash := hashOf(strings.TrimSpace(string(clear))); !bytes.Equal(fileHash, second) {
		t.Fatal("token file does not match the rotated token")
	}
}

func TestNewRequiresDependencies(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"config", Options{}},
		{"saved config", Options{Config: config.NewSource(&config.Config{})}},
		{"fleet", Options{
			Config: config.NewSource(&config.Config{}),
			Saved:  config.NewSource(&config.Config{}),
		}},
		{"account unpairer", Options{
			Config:   config.NewSource(&config.Config{}),
			Fleet:    &fakeFleet{},
			Bindings: &fakeAccounts{},
			Pairings: fakePairings{},
			Pairing:  &fakePairing{},
		}},
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

	recorder := get(t, handler, "/api/v1/scopes/support/chats", testAccessToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("own scope status = %d, want 200", recorder.Code)
	}
	// Another configured scope answers 404, never 403, so a scoped principal
	// cannot infer that it exists.
	recorder = get(t, handler, "/api/v1/scopes/ventas/chats", testAccessToken)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("other scope status = %d, want 404", recorder.Code)
	}
	for _, path := range []string{"/api/v1/config", "/api/v1/tokens"} {
		recorder = get(t, handler, path, testAccessToken)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", path, recorder.Code)
		}
	}
	recorder = get(t, handler, "/api/v1/bots", testAccessToken)
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
	for _, key := range []string{testRevokedToken, testExpiredToken} {
		recorder := get(t, server.Handler(), "/api/v1/status", key)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("status with %q = %d, want 401", key, recorder.Code)
		}
	}
}

func TestTokensEndpoints(t *testing.T) {
	server := testServer(t)
	handler := server.Handler()

	body := strings.NewReader(`{"name":"ci","scopes":["support"]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body)
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201: %s", recorder.Code, recorder.Body)
	}
	var created struct {
		ID     int64    `json:"id"`
		Name   string   `json:"name"`
		Token  string   `json:"token"`
		Scopes []string `json:"scopes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.ID != 4 || created.Name != "ci" || created.Token == "" {
		t.Fatalf("created = %#v", created)
	}

	recorder = get(t, handler, "/api/v1/scopes/support/chats", created.Token)
	if recorder.Code != http.StatusOK {
		t.Fatalf("minted token status = %d, want 200", recorder.Code)
	}
	recorder = get(t, handler, "/api/v1/scopes/ventas/chats", created.Token)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("minted token other scope = %d, want 404", recorder.Code)
	}

	recorder = get(t, handler, "/api/v1/tokens", testOwnerToken)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), created.Token) {
		t.Fatal("token list exposed a clear token")
	}

	request = httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/tokens/%d", created.ID), nil)
	request.Header.Set("Authorization", "Bearer "+testOwnerToken)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", recorder.Code)
	}
	recorder = get(t, handler, "/api/v1/scopes/support/chats", created.Token)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", recorder.Code)
	}
}

func TestTokenMintValidation(t *testing.T) {
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
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens",
				strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want,
					recorder.Body)
			}
		})
	}
}

func TestExclusiveScopeGrants(t *testing.T) {
	cases := []struct {
		name      string
		exclusive bool
		revoke    bool
		body      string
		want      int
	}{
		// The default lets the owner share a scope between tokens.
		{"shared grant", false, false,
			`{"name":"ci","scopes":["support"]}`, http.StatusCreated},
		{"refuses a held scope", true, false,
			`{"name":"ci","scopes":["support"]}`, http.StatusConflict},
		{"allows a free scope", true, false,
			`{"name":"ci","scopes":["ventas"]}`, http.StatusCreated},
		// Only active tokens hold a scope: the fixture's revoked and expired
		// tokens name `support` too, and releasing its holder frees the scope.
		{"allows a released scope", true, true,
			`{"name":"ci","scopes":["support"]}`, http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := testServerWithConfig(t, &fakeFleet{},
				func(cfg *config.Config) {
					cfg.ExclusiveScopes = tc.exclusive
				})
			handler := server.Handler()
			if tc.revoke {
				recorder := do(t, handler, http.MethodDelete,
					"/api/v1/tokens/1", testOwnerToken)
				if recorder.Code != http.StatusNoContent {
					t.Fatalf("revoke status = %d, want 204", recorder.Code)
				}
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens",
				strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, tc.want,
					recorder.Body)
			}
		})
	}
}

// TestExclusiveScopesStartup: exclusive mode only keeps its promise when the
// grants already stored are exclusive too, so New refuses an overlap minted
// before the setting was turned on.
func TestExclusiveScopesStartup(t *testing.T) {
	cases := []struct {
		name      string
		exclusive bool
		overlap   bool
		wantErr   bool
	}{
		{"shared grants start", false, true, false},
		{"one holder per scope starts", true, false, false},
		{"overlap refuses to start", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := testOptions(t, &fakeFleet{}, &config.Config{
				ExclusiveScopes: tc.exclusive,
			})
			if tc.overlap {
				store, ok := opts.Tokens.(*fakeAccessTokens)
				if !ok {
					t.Fatalf("tokens = %T, want *fakeAccessTokens", opts.Tokens)
				}
				store.tokens = append(store.tokens, botflota.AccessToken{
					ID: 4, Name: "second", Hash: hashOf("second-token"),
					Scopes: []string{"support"}, CreatedAt: testTime,
				})
			}
			server, err := New(opts)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("New started on an overlap: %v", server)
			}
			for _, want := range []string{
				"exclusive_scopes", "support", `"scoped"`, `"second"`,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %s", err, want)
				}
			}
		})
	}
}

// TestConcurrentMintsShareNothing: while the setting holds, two mints racing
// for one scope cannot both pass the check; exactly one is created.
func TestConcurrentMintsShareNothing(t *testing.T) {
	server := testServerWithConfig(t, &fakeFleet{}, func(cfg *config.Config) {
		cfg.ExclusiveScopes = true
	})
	handler := server.Handler()

	const mints = 8
	codes := make([]int, mints)
	var wg sync.WaitGroup
	for i := range mints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/tokens",
				strings.NewReader(`{"name":"ci","scopes":["ventas"]}`))
			request.Header.Set("Authorization", "Bearer "+testOwnerToken)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			codes[i] = recorder.Code
		}()
	}
	wg.Wait()

	created := 0
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
		default:
			t.Errorf("status = %d, want 201 or 409", code)
		}
	}
	if created != 1 {
		t.Fatalf("created = %d, want 1", created)
	}
}
