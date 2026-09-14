// Package logging builds the one structured logger used across BotForje. It
// writes one plain, column-aligned line per record with no ANSI escapes, so
// journalctl and `docker logs` show it verbatim (ADR 0012 prefers stdlib).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const timeFormat = "2006-01-02 15:04:05"

// New returns a logger at the given level writing to w. The level accepts
// debug, info, warn, or error (anything else is info).
func New(level string, w io.Writer) *slog.Logger {
	return slog.New(&handler{w: w, level: parseLevel(level), mu: &sync.Mutex{}})
}

// Discard returns a logger that drops every record.
func Discard() *slog.Logger {
	return slog.New(&handler{w: io.Discard, level: slog.LevelError + 1, mu: &sync.Mutex{}})
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// handler formats one line as:
//
//	[2006-01-02 15:04:05] LEVEL [file.go:line] message key=value
//
// Every column is separated by exactly one space.
type handler struct {
	w     io.Writer
	level slog.Level
	mu    *sync.Mutex
	attrs []slog.Attr
	group string
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	var builder strings.Builder
	builder.WriteByte('[')
	builder.WriteString(record.Time.Format(timeFormat))
	builder.WriteString("] ")
	builder.WriteString(record.Level.String())
	builder.WriteByte(' ')
	builder.WriteByte('[')
	builder.WriteString(source(record))
	builder.WriteByte(']')
	builder.WriteByte(' ')
	builder.WriteString(record.Message)
	for _, attr := range h.attrs {
		writeAttr(&builder, h.group, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		writeAttr(&builder, h.group, attr)
		return true
	})
	builder.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, builder.String())
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := *h
	if clone.group == "" {
		clone.group = name
	} else {
		clone.group += "." + name
	}
	return &clone
}

// source is the base file name and line of the caller, never the package path.
func source(record slog.Record) string {
	if record.PC == 0 {
		return "?:0"
	}
	frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
	if frame.File == "" {
		return "?:0"
	}
	return filepath.Base(frame.File) + ":" + strconv.Itoa(frame.Line)
}

func writeAttr(builder *strings.Builder, group string, attr slog.Attr) {
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		prefix := group
		if attr.Key != "" {
			if prefix != "" {
				prefix += "."
			}
			prefix += attr.Key
		}
		for _, child := range attr.Value.Group() {
			writeAttr(builder, prefix, child)
		}
		return
	}
	key := attr.Key
	if group != "" {
		key = group + "." + key
	}
	builder.WriteByte(' ')
	builder.WriteString(key)
	builder.WriteByte('=')
	builder.WriteString(formatValue(attr.Value))
}

func formatValue(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return quoteIfNeeded(value.String())
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return quoteIfNeeded(err.Error())
		}
		return quoteIfNeeded(fmt.Sprint(value.Any()))
	case slog.KindTime:
		return value.Time().Format(timeFormat)
	default:
		return value.String()
	}
}

func quoteIfNeeded(value string) string {
	if value == "" || strings.ContainsAny(value, " \t\"=") {
		return strconv.Quote(value)
	}
	return value
}
