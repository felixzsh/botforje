package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	var buffer bytes.Buffer
	log := New("debug", &buffer)
	log.Info("bot started", "scope", "dev", "bot", "dev-bot")

	line := strings.TrimSuffix(buffer.String(), "\n")
	pattern := regexp.MustCompile(
		`^\[\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\] INFO  ` +
			`\[logging_test\.go:\d+\] bot started scope=dev bot=dev-bot$`)
	if !pattern.MatchString(line) {
		t.Fatalf("line = %q", line)
	}
	if strings.Contains(line, "\x1b") {
		t.Fatal("output contains an ANSI escape")
	}
}

func TestLevelAndFileAlign(t *testing.T) {
	var buffer bytes.Buffer
	log := New("debug", &buffer)
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
	log := New("warn", &buffer)
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
	New("info", &buffer).Error("boom", "error", errors.New("connection reset"))
	if !strings.Contains(buffer.String(), `error="connection reset"`) {
		t.Fatalf("output = %q", buffer.String())
	}
}

func TestParseLevel(t *testing.T) {
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
		if got := ParseLevel(tc.in); got != tc.want {
			t.Fatalf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestOffSilences(t *testing.T) {
	var buffer bytes.Buffer
	New("off", &buffer).Error("nope")
	if buffer.Len() != 0 {
		t.Fatalf("output = %q", buffer.String())
	}
}

func TestMultilineValueStaysOnOneLine(t *testing.T) {
	var buffer bytes.Buffer
	New("info", &buffer).Info("msg", "body", "line1\nline2")

	lines := strings.Split(strings.TrimSuffix(buffer.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("record spans %d lines: %q", len(lines), buffer.String())
	}
	if !strings.Contains(lines[0], `body="line1\nline2"`) {
		t.Fatalf("body not quoted: %q", lines[0])
	}
}

func TestDiscard(t *testing.T) {
	if Discard().Enabled(context.Background(), slog.LevelError) {
		t.Fatal("discard must disable every level")
	}
}
