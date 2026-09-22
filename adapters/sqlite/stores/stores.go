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

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
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
