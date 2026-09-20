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

// chatsServer answers the chats route and records the queries it saw.
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
	 "sender":{"phone":"5215500000000","name":"Ana"},
	 "started_at":"2026-09-13T10:00:00Z","updated_at":"2026-09-13T10:05:00Z",
	 "messages":4},
	{"id":1,"scope":"support","bot":"demo",
	 "sender":{"phone":"5215500000001","name":""},
	 "started_at":"2026-09-12T09:00:00Z","updated_at":"2026-09-12T09:00:00Z",
	 "messages":0}
]}`

func TestPrintChatsListsSenders(t *testing.T) {
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
		"PHONE", "NAME", "MESSAGES", "STARTED", "UPDATED",
		"5215500000000", "Ana", "5215500000001", "-", "4",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
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

// The routes are scope-addressed; make sure the query encodes them like the
// messages command does.
func TestChatsQueryPath(t *testing.T) {
	got := chatsQuery{scope: "dev scope", bot: "demo", limit: 5}.path()
	if !strings.Contains(got, "/api/v1/scopes/dev%20scope/chats?") {
		t.Fatalf("path = %q, want the escaped scope", got)
	}
	if !strings.Contains(got, "bot=demo") || !strings.Contains(got, "limit=5") {
		t.Fatalf("path = %q, want the filters", got)
	}
}
