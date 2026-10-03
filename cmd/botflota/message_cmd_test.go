package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type messagesServer struct {
	mu      sync.Mutex
	queries []string
	page    func(afterID string) string
}

func (m *messagesServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/scopes/support/messages") {
			t.Errorf("path = %q", r.URL.Path)
		}
		m.mu.Lock()
		m.queries = append(m.queries, r.URL.RawQuery)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(m.page(r.URL.Query().Get("after_id"))))
	})
}

func (m *messagesServer) lastQuery(t *testing.T) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queries) == 0 {
		t.Fatal("the API was never called")
	}
	return m.queries[len(m.queries)-1]
}

const twoMessages = `{"messages":[
	{"id":1,"direction":"in","ingest_kind":"live","routing":"trigger_fuzzy",
	 "body":"hola","at":"2026-09-13T10:05:00Z"},
	{"id":2,"direction":"out","ingest_kind":"live","origin":"preset",
	 "routing":"trigger_fuzzy","body":"hi","at":"2026-09-13T10:05:02Z"}
]}`

func TestPrintMessagesUsesTheNewestPage(t *testing.T) {
	page := &messagesServer{page: func(afterID string) string {
		if afterID != "" {
			t.Errorf("after_id = %q, want no cursor on the first call", afterID)
		}
		return twoMessages
	}}
	server := httptest.NewServer(page.handler(t))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := messageQuery{scope: "support", bot: "demo", phone: "52155", limit: 50}

	var out bytes.Buffer
	last, err := printMessages(context.Background(), &out, client, query, 0)
	if err != nil {
		t.Fatalf("printMessages: %v", err)
	}
	if last != 2 {
		t.Fatalf("cursor = %d, want the last id 2", last)
	}
	text := out.String()
	if !strings.Contains(text, "hola") || !strings.Contains(text, "hi") {
		t.Fatalf("output = %q, want both messages", text)
	}
	if !strings.Contains(text, "preset") || !strings.Contains(text, "trigger_fuzzy") {
		t.Fatalf("output = %q, want the provenance columns", text)
	}
	raw := page.lastQuery(t)
	for _, want := range []string{"bot=demo", "contact=52155", "limit=50"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("query %q is missing %q", raw, want)
		}
	}
}

func TestPrintMessagesResumesFromCursor(t *testing.T) {
	page := &messagesServer{page: func(afterID string) string {
		if afterID != "2" {
			t.Errorf("after_id = %q, want 2", afterID)
		}
		return `{"messages":[{"id":3,"direction":"in","body":"nuevo",
			"at":"2026-09-13T10:06:00Z"}]}`
	}}
	server := httptest.NewServer(page.handler(t))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := messageQuery{scope: "support", bot: "demo", phone: "52155", limit: 50}

	var out bytes.Buffer
	last, err := printMessages(context.Background(), &out, client, query, 2)
	if err != nil {
		t.Fatalf("printMessages: %v", err)
	}
	if last != 3 {
		t.Fatalf("cursor = %d, want 3", last)
	}
	if !strings.Contains(out.String(), "nuevo") {
		t.Fatalf("output = %q, want the new message", out.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestFollowMessagesPollsWithCursor(t *testing.T) {
	advanced := make(chan struct{}, 1)
	page := &messagesServer{page: func(afterID string) string {
		switch afterID {
		case "2":
			return `{"messages":[{"id":3,"direction":"in","body":"nuevo",
				"at":"2026-09-13T10:06:00Z"}]}`
		case "3":
			select {
			case advanced <- struct{}{}:
			default:
			}
			return `{"messages":[]}`
		default:
			t.Errorf("after_id = %q, want the cursor to advance from 2", afterID)
			return `{"messages":[]}`
		}
	}}
	server := httptest.NewServer(page.handler(t))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := messageQuery{scope: "support", bot: "demo", phone: "52155", limit: 50}

	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuffer
	done := make(chan error, 1)
	go func() {
		done <- followMessages(ctx, &out, client, query, 2, time.Millisecond)
	}()
	waitFor(t, func() bool { return strings.Contains(out.String(), "nuevo") },
		"the follower never printed the polled message")
	waitFor(t, func() bool { return len(advanced) > 0 },
		"the follower never advanced the cursor")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("followMessages: %v", err)
	}
}

func waitFor(t *testing.T, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(time.Millisecond)
	}
}
