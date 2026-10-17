package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const twoPeers = `{"scope":"support","peers":[
	{"id":7,"provider":"whatsapp","subject":"5215500000000",
	 "display_name":"Ana","first_seen":"2026-09-13T10:00:00Z",
	 "last_seen":"2026-09-13T10:05:00Z",
	 "directory_imported":true,"has_chat":true,"has_history":true},
	{"id":8,"provider":"whatsapp","subject":"5215599999999",
	 "display_name":"","first_seen":"0001-01-01T00:00:00Z",
	 "last_seen":"0001-01-01T00:00:00Z",
	 "directory_imported":false,"has_chat":false,"has_history":false}
]}`

func TestPrintPeersListsIdentities(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scopes/support/peers" {
			t.Errorf("path = %q", r.URL.Path)
		}
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(twoPeers))
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}
	query := peersQuery{scope: "support", limit: 50}

	var out bytes.Buffer
	if err := printPeers(context.Background(), &out, client, query); err != nil {
		t.Fatalf("printPeers: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"ID", "PROVIDER", "SUBJECT", "NAME", "FIRST_SEEN", "LAST_SEEN",
		"DIR", "CHAT", "HIST",
		"7", "whatsapp", "5215500000000", "Ana",
		"8", "5215599999999", "-", "true", "false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
	}
	if strings.Contains(text, "phone") || strings.Contains(text, "PHONE") {
		t.Fatalf("output %q retains the retired phone projection", text)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "limit=50") {
		t.Fatalf("queries = %v, want the limit", queries)
	}
}

func TestPrintPeerReadsOneIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/scopes/support/peers/7" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(`{"id":7,"provider":"whatsapp",` +
			`"subject":"5215500000000","display_name":"Ana",` +
			`"first_seen":"2026-09-13T10:00:00Z",` +
			`"last_seen":"2026-09-13T10:05:00Z",` +
			`"directory_imported":true,"has_chat":true,"has_history":true}`))
	}))
	defer server.Close()
	client := &apiClient{base: server.URL, token: "tok", http: server.Client()}

	var out bytes.Buffer
	query := peerQuery{scope: "support", id: 7}
	if err := printPeer(context.Background(), &out, client, query); err != nil {
		t.Fatalf("printPeer: %v", err)
	}
	text := out.String()
	for _, want := range []string{"7", "whatsapp", "5215500000000", "Ana"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
	}
}

func TestRunPeerListRejectsAPositionalBot(t *testing.T) {
	// Peers are scope-owned: the command takes no bot argument.
	if code := runPeerList([]string{"demo"}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a positional bot", code)
	}
}

func TestRunPeerShowNeedsAPeerID(t *testing.T) {
	if code := runPeerShow(nil); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a missing peer id", code)
	}
	if code := runPeerShow([]string{"demo"}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a positional bot", code)
	}
}

// TestPeersQueryPath checks a scope-addressed route: the query must encode
// it like the directory command does.
func TestPeersQueryPath(t *testing.T) {
	got := peersQuery{scope: "dev scope", limit: 5}.path()
	if !strings.Contains(got, "/api/v1/scopes/dev%20scope/peers?") {
		t.Fatalf("path = %q, want the escaped scope", got)
	}
	if !strings.Contains(got, "limit=5") {
		t.Fatalf("path = %q, want the limit", got)
	}
}

// TestPeerQueryPath checks the scope-owned id route: the id travels in the
// path, never as a bare subject.
func TestPeerQueryPath(t *testing.T) {
	got := peerQuery{scope: "dev scope", id: 7}.path()
	if got != "/api/v1/scopes/dev%20scope/peers/7" {
		t.Fatalf("path = %q, want the escaped scope and id", got)
	}
}
