// Package stores implements the core store ports as SQLite repositories
// (ADR 0006). The connection and the DDL live in adapters/sqlite.
package stores

import (
	"encoding/json"
	"time"
)

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
