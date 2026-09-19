package botflota

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// sourceName is the "dir/file.go" source column the handler derives for this
// file, wherever the repository is checked out.
func sourceName(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
}

func TestFormat(t *testing.T) {
	var buffer bytes.Buffer
	log := NewLogger("debug", &buffer)
	log.Info("bot started", "scope", "dev", "bot", "dev-bot")

	line := strings.TrimSuffix(buffer.String(), "\n")
	pattern := regexp.MustCompile(
		`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] INFO  ` +
			`\[` + regexp.QuoteMeta(sourceName(t)) + `:\d+\] ` +
			`bot started scope=dev bot=dev-bot$`)
	if !pattern.MatchString(line) {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, "\x1b") {
		t.Fatal("output contains an ANSI escape")
	}
}

func TestLevelAndFileAlign(t *testing.T) {
	var buffer bytes.Buffer
	log := NewLogger("debug", &buffer)
	log.Info("first")
	log.Error("second")

	lines := strings.Split(strings.TrimSuffix(buffer.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	// The file column is the second '[' on the line.
	fileColumn := func(line string) int {
		first := strings.IndexByte(line, '[')
		return first + 1 + strings.IndexByte(line[first+1:], '[')
	}
	if fileColumn(lines[0]) != fileColumn(lines[1]) {
		t.Fatalf("file column not aligned:\n%s\n%s", lines[0], lines[1])
	}
}

func TestLevelFilter(t *testing.T) {
	var buffer bytes.Buffer
	log := NewLogger("warn", &buffer)
	log.Debug("dropped-debug")
	log.Info("dropped-info")
	log.Warn("kept-warn")

	out := buffer.String()
	if strings.Contains(out, "dropped") || !strings.Contains(out, "kept-warn") {
		t.Fatalf("output = %q", out)
	}
}

func TestErrorQuoting(t *testing.T) {
	var buffer bytes.Buffer
	NewLogger("info", &buffer).Error("boom", "error", errors.New("connection reset"))
	if !strings.Contains(buffer.String(), `error="connection reset"`) {
		t.Fatalf("output = %q", buffer.String())
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"off", slog.LevelError + 1},
		{"", slog.LevelInfo},
		{"nonsense", slog.LevelInfo},
	}
	for _, tc := range cases {
		if got := ParseLogLevel(tc.in); got != tc.want {
			t.Fatalf("ParseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestOffSilences(t *testing.T) {
	var buffer bytes.Buffer
	NewLogger("off", &buffer).Error("nope")
	if buffer.Len() != 0 {
		t.Fatalf("output = %q", buffer.String())
	}
}

func TestMultilineValueStaysOnOneLine(t *testing.T) {
	var buffer bytes.Buffer
	NewLogger("info", &buffer).Info("msg", "body", "line1\nline2")

	lines := strings.Split(strings.TrimSuffix(buffer.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("record spans %d lines: %q", len(lines), buffer.String())
	}
	if !strings.Contains(lines[0], `body="line1\nline2"`) {
		t.Fatalf("body not quoted: %q", lines[0])
	}
}

func TestDiscard(t *testing.T) {
	if DiscardLogger().Enabled(context.Background(), slog.LevelError) {
		t.Fatal("discard must disable every level")
	}
}
