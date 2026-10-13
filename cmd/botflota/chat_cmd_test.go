package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type chatsServer struct {
	mu      sync.Mutex
	queries []string
	body    string
}

func (c *chatsServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scopes/support/chats" {
			t.Errorf("path = %q", r.URL.Path)
		}
		c.mu.Lock()
		c.queries = append(c.queries, r.URL.RawQuery)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(c.body))
	})
}

func (c *chatsServer) lastQuery(t *testing.T) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queries) == 0 {
		t.Fatal("the API was never called")
	}
	return c.queries[len(c.queries)-1]
}

const twoChats = `{"chats":[
	{"id":3,"scope":"support","bot":"demo",
	 "peer":{"provider":"whatsapp","subject":"5215500000000","name":"Ana"},
	 "started_at":"2026-09-13T10:00:00Z","updated_at":"2026-09-13T10:05:00Z",
	 "messages":5,"sync":3,"live":2},
	{"id":1,"scope":"support","bot":"demo",
	 "peer":{"provider":"whatsapp","subject":"5215500000001"},
	 "started_at":"2026-09-12T09:00:00Z","updated_at":"2026-09-12T09:00:00Z",
	 "messages":0,"sync":0,"live":0}
]}`

func TestPrintChatsListsPeers(t *testing.T) {
	page := &chatsServer{body: twoChats}
	server := httptest.NewServer(page.handler(t))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := chatsQuery{scope: "support", bot: "demo", limit: 50}

	var out bytes.Buffer
	if err := printChats(context.Background(), &out, client, query); err != nil {
		t.Fatalf("printChats: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"ID", "PROVIDER", "SUBJECT", "NAME", "MESSAGES", "SYNC", "LIVE",
		"STARTED", "UPDATED",
		"3", "whatsapp", "5215500000000", "Ana",
		"1", "5215500000001", "-", "5", "3", "2",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
	}
	if strings.Contains(text, "phone") || strings.Contains(text, "PHONE") {
		t.Fatalf("output %q retains the retired phone projection", text)
	}
	raw := page.lastQuery(t)
	for _, want := range []string{"bot=demo", "limit=50"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("query %q is missing %q", raw, want)
		}
	}
}

func TestRunChatListNeedsABot(t *testing.T) {
	if code := runChatList(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing bot", code)
	}
}

func TestRunChatSyncNeedsABotAndChatID(t *testing.T) {
	if code := runChatSync(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing bot", code)
	}
	// A bot without --chat-id and without --all names no destination.
	if code := runChatSync([]string{"demo"}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing chat id", code)
	}
}

// TestChatsQueryPath checks a scope-addressed route: the query must encode
// it like the messages command does.
func TestChatsQueryPath(t *testing.T) {
	got := chatsQuery{scope: "dev scope", bot: "demo", limit: 5}.path()
	if !strings.Contains(got, "/api/v1/scopes/dev%20scope/chats?") {
		t.Fatalf("path = %q, want the escaped scope", got)
	}
	if !strings.Contains(got, "bot=demo") || !strings.Contains(got, "limit=5") {
		t.Fatalf("path = %q, want the filters", got)
	}
}

// TestSyncEveryChatWalksEveryChat covers `chat sync --all`: every chat listed
// with a zero limit, backfilled by chat id in order (ADR 0035, ADR 0044).
func TestSyncEveryChatWalksEveryChat(t *testing.T) {
	var (
		mu     sync.Mutex
		synced []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/api/v1/scopes/support/chats":
			if got := r.URL.Query().Get("limit"); got != "0" {
				t.Errorf("chats limit = %q, want 0 for every chat", got)
			}
			_, _ = w.Write([]byte(twoChats))
		case "/api/v1/scopes/support/chats/sync":
			mu.Lock()
			synced = append(synced, r.URL.Query().Get("chat_id"))
			mu.Unlock()
			_, _ = w.Write([]byte(`{"received":3,"rounds":1,"imported":2,` +
				`"duplicate":1,"skipped":0,"stable":true}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}

	var out bytes.Buffer
	code := syncEveryChat(context.Background(), &out, client, "support", "demo", 0, 90)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	text := out.String()
	for _, want := range []string{
		"syncing 2 chat(s)",
		"imported 2 (duplicate 1, skipped 0) [nothing older]",
		"synced 2 chat(s), imported 4 (duplicate 2, skipped 0), 0 failed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output %q is missing %q", text, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(synced) != 2 || synced[0] != "3" || synced[1] != "1" {
		t.Fatalf("synced = %v, want both chats by id in order", synced)
	}
}

// TestSyncEveryChatKeepsGoingAfterAFailure: one unreachable chat is counted
// and reported without stopping the walk.
func TestSyncEveryChatKeepsGoingAfterAFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		switch r.URL.Path {
		case "/api/v1/scopes/support/chats":
			_, _ = w.Write([]byte(twoChats))
		case "/api/v1/scopes/support/chats/sync":
			if r.URL.Query().Get("chat_id") == "3" {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(
					`{"error":{"code":"sync_failed","message":"no answer"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"received":1,"rounds":1,"imported":1,` +
				`"duplicate":0,"skipped":0,"exhausted":true}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}

	var out bytes.Buffer
	code := syncEveryChat(context.Background(), &out, client, "support", "demo", 0, 90)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: one chat failing is not the walk failing", code)
	}
	text := out.String()
	for _, want := range []string{
		"failed", "no answer", "[no more access]",
		"synced 1 chat(s), imported 1 (duplicate 0, skipped 0), 1 failed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output %q is missing %q", text, want)
		}
	}
}
