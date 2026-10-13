package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/felixzsh/botflota"
)

// fakeHistory serves the rows a test hands it, so the endpoint's archive can be
// checked without a database, and records the delete and move it was asked for.
type fakeHistory struct {
	chats       []botflota.HistoryChat
	sessions    []botflota.Session
	messages    []botflota.Message
	addressBook []botflota.DirectoryEntry
	err         error
	writeErr    error
	deleted     []string
	moved       []string
	occupancy   map[string]bool
	at          time.Time
}

func (f *fakeHistory) DeleteBot(
	_ context.Context, scope, bot string,
) (botflota.HistoryCounts, error) {
	if f.writeErr != nil {
		return botflota.HistoryCounts{}, f.writeErr
	}
	f.deleted = append(f.deleted, scope+"/"+bot)
	return botflota.HistoryCounts{Chats: 2, GraphRuns: 1, Messages: 3, Directory: 4}, nil
}

func (f *fakeHistory) MoveBot(
	_ context.Context, scope, from, to string, at time.Time,
) (botflota.HistoryCounts, error) {
	if f.writeErr != nil {
		return botflota.HistoryCounts{}, f.writeErr
	}
	if f.occupancy[scope+"/"+to] {
		return botflota.HistoryCounts{}, botflota.ErrDestinationNotEmpty
	}
	f.moved = append(f.moved, scope+"/"+from+"->"+to)
	f.at = at
	return botflota.HistoryCounts{Chats: 2, GraphRuns: 1, Messages: 3, Directory: 4}, nil
}

func (f *fakeHistory) ReadHistory(
	_ context.Context, scope, bot string, sink botflota.HistorySink,
) error {
	if f.err != nil {
		return f.err
	}
	// Each table is announced right before its rows, the way the store reads it.
	if err := sink.BeginTable(botflota.TableChats); err != nil {
		return err
	}
	for _, chat := range f.chats {
		if err := sink.Chat(chat); err != nil {
			return err
		}
	}
	if err := sink.BeginTable(botflota.TableGraphRuns); err != nil {
		return err
	}
	for _, session := range f.sessions {
		if err := sink.Session(session); err != nil {
			return err
		}
	}
	if err := sink.BeginTable(botflota.TableMessages); err != nil {
		return err
	}
	for _, message := range f.messages {
		if err := sink.Message(message); err != nil {
			return err
		}
	}
	if err := sink.BeginTable(botflota.TableDirectory); err != nil {
		return err
	}
	for _, entry := range f.addressBook {
		if err := sink.DirectoryEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

func newHistoryServer(t *testing.T, history *fakeHistory) *Server {
	t.Helper()
	server := testServer(t)
	server.history = history
	return server
}

func exportHistory(t *testing.T, server *Server, scope, token string) *httptest.ResponseRecorder {
	t.Helper()
	return get(t, server.Handler(),
		"/api/v1/scopes/"+scope+"/bots/demo/history/export", token)
}

func readArchive(t *testing.T, recorder *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.Bytes()
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		content, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		files[file.Name] = string(content)
	}
	return files
}

func csvRows(t *testing.T, content string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(content)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv %q: %v", content, err)
	}
	return rows
}

func TestHistoryExportStreamsOneCsvPerTable(t *testing.T) {
	server := newHistoryServer(t, &fakeHistory{
		chats: []botflota.HistoryChat{{
			Chat: botflota.Chat{
				ID: 3, Scope: "support", Bot: "demo", PeerID: 7,
				Target:    waTarget("5215500000000"),
				StartedAt: testTime, UpdatedAt: testTime, MessageCount: 1,
				Guards: map[string]botflota.GuardState{"greet": {}},
			},
			PeerKey: waKey("5215500000000"), PeerName: "Ana",
		}},
		sessions: []botflota.Session{{
			ID: 9, ChatID: 3, Node: "menu", VisitedNodes: []string{"menu"},
			CreatedAt: testTime, UpdatedAt: testTime, ExpiresAt: testTime,
		}},
		messages: []botflota.Message{{
			ID: 1, ChatID: 3, SessionID: 9, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hola, mundo"},
			At:      testTime, Ingest: botflota.IngestLive,
		}},
		addressBook: []botflota.DirectoryEntry{{
			Scope: "support", Bot: "demo", PeerKey: waKey("5215500000000"),
			SourceIdentity: waKey("5215500000000"), DisplayName: "Ana",
			UpdatedAt: testTime,
		}},
	})
	recorder := exportHistory(t, server, "support", testOwnerToken)

	if got := recorder.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("content type = %q, want a zip", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(
		got, "attachment") || !strings.Contains(got, "support-demo-history.zip") {
		t.Fatalf("content disposition = %q", got)
	}

	files := readArchive(t, recorder)
	for _, name := range []string{"chats.csv", "graph_runs.csv", "messages.csv",
		"directory.csv"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("%s is missing: %v", name, files)
		}
	}

	// Each file carries a header and its values, and the body keeps its comma
	// because encoding/csv quotes it.
	header := csvRows(t, files["messages.csv"])[0]
	for _, column := range []string{"id", "chat_id", "graph_run_id", "direction",
		"kind", "body", "content", "at", "provider_message_id"} {
		if !slices.Contains(header, column) {
			t.Fatalf("messages header %v is missing %q", header, column)
		}
	}
	row := csvRows(t, files["messages.csv"])[1]
	if row[slices.Index(header, "body")] != "hola, mundo" {
		t.Fatalf("body = %q, want it intact", row[slices.Index(header, "body")])
	}
	if row[slices.Index(header, "content")] != `{"body":"hola, mundo"}` {
		t.Fatalf("content = %q, want the stored JSON",
			row[slices.Index(header, "content")])
	}

	chats := csvRows(t, files["chats.csv"])
	if chats[1][slices.Index(chats[0], "peer_subject")] != "5215500000000" {
		t.Fatalf("chats row = %v, want the peer's subject", chats[1])
	}
	if chats[1][slices.Index(chats[0], "guards")] != `{"greet":{}}` {
		t.Fatalf("guards = %q, want the stored JSON",
			chats[1][slices.Index(chats[0], "guards")])
	}
}

func TestHistoryExportWritesAHeaderForEveryTable(t *testing.T) {
	server := newHistoryServer(t, &fakeHistory{
		messages: []botflota.Message{{
			ID: 1, ChatID: 3, Direction: botflota.Inbound,
			Content: botflota.TextContent{Body: "hi"}, At: testTime,
		}},
	})
	files := readArchive(t, exportHistory(t, server, "support", testOwnerToken))
	// A table with no rows still gets its file and its header, so a reader can
	// tell "nothing here" from "not exported".
	for name, wantHeader := range map[string]string{
		"chats.csv":      "peer_subject",
		"graph_runs.csv": "graph_context",
		"messages.csv":   "provider_message_id",
		"directory.csv":  "source_subject",
	} {
		content, ok := files[name]
		if !ok {
			t.Fatalf("%s is missing: %v", name, files)
		}
		rows := csvRows(t, content)
		if !slices.Contains(rows[0], wantHeader) {
			t.Fatalf("%s header = %v, want it to name %q", name, rows[0], wantHeader)
		}
	}
	// Only the table with rows has a row to show.
	if rows := csvRows(t, files["chats.csv"]); len(rows) != 1 {
		t.Fatalf("chats.csv = %v, want a header and nothing else", rows)
	}
	if rows := csvRows(t, files["messages.csv"]); len(rows) != 2 {
		t.Fatalf("messages.csv = %v, want a header and the stored message", rows)
	}
}

func TestHistoryExportAuthority(t *testing.T) {
	history := &fakeHistory{}
	t.Run("a scoped token exports its own scope", func(t *testing.T) {
		server := newHistoryServer(t, history)
		recorder := exportHistory(t, server, "support", testAccessToken)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}
	})
	t.Run("another scope is unknown, not forbidden", func(t *testing.T) {
		server := newHistoryServer(t, history)
		recorder := exportHistory(t, server, "ventas", testAccessToken)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
	t.Run("an unknown bot answers 404", func(t *testing.T) {
		server := newHistoryServer(t, history)
		recorder := get(t, server.Handler(),
			"/api/v1/scopes/support/bots/nope/history/export", testOwnerToken)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", recorder.Code)
		}
	})
	t.Run("a server without a reader says so", func(t *testing.T) {
		server := testServer(t)
		recorder := exportHistory(t, server, "support", testOwnerToken)
		requireUnsupportedOperation(t, recorder.Code, recorder.Body.String())
	})
}
