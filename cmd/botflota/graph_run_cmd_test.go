package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const oneGraphRun = `{"scope":"support","bot":"demo","chat_id":3,
	"graph_run":{"id":9,"chat_id":3,"node":"menu","visited_nodes":["menu"],
	"created_at":"2026-09-13T10:00:00Z","updated_at":"2026-09-13T10:05:00Z",
	"expires_at":"2026-09-13T11:00:00Z"}}`

func TestPrintGraphRunReadsOneChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scopes/support/bots/demo/graph-runs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("chat_id"); got != "3" {
			t.Errorf("chat_id = %q, want 3", got)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(oneGraphRun))
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := graphRunQuery{scope: "support", bot: "demo", chatID: 3}

	var out bytes.Buffer
	if err := printGraphRun(context.Background(), &out, client, query); err != nil {
		t.Fatalf("printGraphRun: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"GRAPH_RUN_ID", "CHAT_ID", "NODE", "VISITED_NODES",
		"CREATED", "UPDATED", "EXPIRES",
		"9", "3", "menu",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
	}
	if strings.Contains(text, "session") || strings.Contains(text, "SESSION") {
		t.Fatalf("output %q uses the retired session name", text)
	}
}

func TestRunGraphRunListNeedsABotAndChatID(t *testing.T) {
	if code := runGraphRunList(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing bot", code)
	}
	// A bot without --chat-id names no conversation.
	if code := runGraphRunList([]string{"demo"}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing chat id", code)
	}
	// A second positional argument is not a second bot.
	if code := runGraphRunList(
		[]string{"demo", "unexpected", "--chat-id", "3"},
	); code != 2 {
		t.Fatalf("exit code = %d, want 2 for an extra argument", code)
	}
}

// TestGraphRunQueryPath checks a bot-addressed route: the query must encode
// it like the messages command does.
func TestGraphRunQueryPath(t *testing.T) {
	got := graphRunQuery{scope: "dev scope", bot: "demo", chatID: 3}.path()
	if !strings.Contains(got, "/api/v1/scopes/dev%20scope/bots/demo/graph-runs?") {
		t.Fatalf("path = %q, want the escaped scope", got)
	}
	if !strings.Contains(got, "chat_id=3") {
		t.Fatalf("path = %q, want the chat id", got)
	}
}
