package api

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/felixzsh/botflota"
)

// historyExportFiles names the file and header of each table one bot's export
// carries: the tables its deletion removes, one CSV each, so the archive is what
// would be lost and nothing else (ADR 0042). A table with no rows still gets its
// file and its header, so a reader can tell "nothing here" from "not exported".
var historyExportFiles = map[botflota.HistoryTable]struct {
	name   string
	header []string
}{
	botflota.TableChats: {"chats.csv", []string{
		"id", "scope", "bot", "contact_id", "contact_phone", "contact_name",
		"started_at", "updated_at", "message_count", "guards",
	}},
	botflota.TableSessions: {"sessions.csv", []string{
		"id", "chat_id", "node", "visited_nodes", "graph_context", "node_context",
		"created_at", "updated_at", "expires_at",
	}},
	botflota.TableMessages: {"messages.csv", []string{
		"id", "chat_id", "session_id", "direction", "kind", "body", "content",
		"sent_at", "provider_message_id", "ingest_kind", "origin", "routing",
	}},
	botflota.TableAddressBook: {"address-book.csv", []string{
		"scope", "bot", "phone", "name", "push_name", "business_name", "updated_at",
	}},
}

// handleHistoryExport streams one bot's history as a zip of CSVs. The rows are
// written as they are read, so a conversation of several megabytes costs no
// more memory than a small one, and the whole read is one snapshot in the store
// (ADR 0042).
func (s *Server) handleHistoryExport(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.scopeParam(w, r)
	if !ok {
		return
	}
	bot, ok := s.botPathParam(w, r, scope)
	if !ok {
		return
	}
	if s.history == nil {
		writeError(w, http.StatusNotImplemented, "unsupported",
			"history export is not available")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=%q", scope+"-"+bot+"-history.zip"))

	// Everything that can be refused is checked before the first byte, because
	// a status cannot be changed once the archive is streaming.
	archive := zip.NewWriter(w)
	exports, err := newHistoryExports(archive)
	if err != nil {
		s.log.Error("api request failed", "operation", "history export",
			"scope", scope, "bot", bot, "error", err)
		return
	}
	if err := s.history.ReadHistory(r.Context(), scope, bot, exports); err != nil {
		// The response is already committed: a truncated archive is the honest
		// outcome, and its central directory is missing, so a client reports it
		// as broken instead of keeping a half-read history.
		s.log.Error("api request failed", "operation", "history export",
			"scope", scope, "bot", bot, "error", err)
		return
	}
	if err := exports.close(); err != nil {
		s.log.Error("api request failed", "operation", "history export",
			"scope", scope, "bot", bot, "error", err)
	}
}

// historyExports writes each table's rows as a CSV inside the archive. The zip
// writes one entry at a time, so a table is finished — flushed — before the next
// one is created, and only one CSV is ever in flight.
type historyExports struct {
	archive *zip.Writer
	current *csv.Writer
	err     error
}

func newHistoryExports(archive *zip.Writer) (*historyExports, error) {
	return &historyExports{archive: archive}, nil
}

// BeginTable finishes the table before it and starts writing the next, header
// first.
func (e *historyExports) BeginTable(table botflota.HistoryTable) error {
	if e.err != nil {
		return e.err
	}
	file, ok := historyExportFiles[table]
	if !ok {
		return fmt.Errorf("api: no export file for table %q", table)
	}
	e.flush()
	entry, err := e.archive.Create(file.name)
	if err != nil {
		e.err = err
		return err
	}
	e.current = csv.NewWriter(entry)
	e.err = e.current.Write(file.header)
	return e.err
}

func (e *historyExports) flush() {
	if e.current == nil {
		return
	}
	e.current.Flush()
	if err := e.current.Error(); err != nil && e.err == nil {
		e.err = err
	}
	e.current = nil
}

func (e *historyExports) write(row []string) error {
	if e.err != nil {
		return e.err
	}
	e.err = e.current.Write(row)
	return e.err
}

func (e *historyExports) close() error {
	e.flush()
	if e.err != nil {
		return e.err
	}
	return e.archive.Close()
}

func (e *historyExports) Chat(chat botflota.HistoryChat) error {
	guards, err := jsonText(chat.Guards)
	if err != nil {
		return err
	}
	return e.write([]string{
		i64(chat.ID), chat.Scope, chat.Bot, i64(chat.ContactID),
		chat.ContactPhone, chat.ContactName,
		at(chat.StartedAt), at(chat.UpdatedAt), i64(chat.MessageCount), guards,
	})
}

func (e *historyExports) Session(session botflota.Session) error {
	visited, err := jsonText(session.VisitedNodes)
	if err != nil {
		return err
	}
	graphContext, err := jsonText(session.GraphContext)
	if err != nil {
		return err
	}
	nodeContext, err := jsonText(session.NodeContext)
	if err != nil {
		return err
	}
	return e.write([]string{
		i64(session.ID), i64(session.ChatID), session.Node, visited,
		graphContext, nodeContext, at(session.CreatedAt), at(session.UpdatedAt),
		at(session.ExpiresAt),
	})
}

func (e *historyExports) Message(message botflota.Message) error {
	content, err := contentJSON(message.Content)
	if err != nil {
		return err
	}
	return e.write([]string{
		i64(message.ID), i64(message.ChatID), i64(message.SessionID),
		string(message.Direction), string(botflota.ContentKind(message.Content)),
		botflota.ContentText(message.Content), content, at(message.At),
		message.ProviderMessageID, string(message.Ingest),
		string(message.Origin), string(message.Routing),
	})
}

func (e *historyExports) AddressBookEntry(entry botflota.AddressBookEntry) error {
	return e.write([]string{
		entry.Scope, entry.Bot, entry.Phone, entry.Name,
		entry.PushName, entry.BusinessName, at(entry.UpdatedAt),
	})
}

func jsonText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func contentJSON(content botflota.MessageContent) (string, error) {
	if content == nil {
		return "", nil
	}
	data, err := botflota.ContentJSON(content)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func i64(value int64) string { return strconv.FormatInt(value, 10) }

// at writes the same fixed-width UTC timestamp the database stores, so an
// exported column sorts chronologically as text, like the stored one does.
func at(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
