// Package stores implements the core store ports as SQLite repositories
// (ADR 0006). The connection and the DDL live in adapters/sqlite.
package stores

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// dbtx is what the stores need from the connection: *sql.DB and *sql.Tx both
// satisfy it, so a turn can run the stores it composes inside one
// transaction.
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func encodeJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeJSON(data string, out any) error {
	if data == "" {
		return nil
	}
	return json.Unmarshal([]byte(data), out)
}

// formatTime writes a fixed-width RFC 3339 UTC timestamp: the width keeps
// the stored text ordered chronologically, which is what the comparisons in
// NoteHistory, CountActive, and the outbox retry schedule rely on. A zero
// time is the empty string.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
