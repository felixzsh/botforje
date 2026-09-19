package botflota

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

// NewLogger returns the one structured logger used across Botflota, at the
// given level writing to w. It writes one plain, column-aligned line per
// record with no ANSI escapes, so journalctl and `docker logs` show it
// verbatim (docs/architecture.md). The level accepts debug, info, warn,
// error, or off (anything else is info, and off drops everything).
func NewLogger(level string, w io.Writer) *slog.Logger {
	return slog.New(&handler{w: w, level: ParseLogLevel(level), mu: &sync.Mutex{}})
}

// DiscardLogger returns a logger that drops every record.
func DiscardLogger() *slog.Logger {
	return slog.New(&handler{w: io.Discard, level: slog.LevelError + 1, mu: &sync.Mutex{}})
}

// ParseLogLevel maps a config level to a slog level: debug, info, warn,
// error, or off (above error, so it disables the logger).
func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "off":
		return slog.LevelError + 1
	default:
		return slog.LevelInfo
	}
}

// handler formats one line as:
//
//	[2006-01-02 15:04:05] LEVEL [pkg/file.go:line] message key=value
//
// The time, level, and file columns are aligned; the message starts one space
// after pkg/file.go:line, so its column varies with the source name length.
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
	builder.WriteString(fmt.Sprintf("%-5s", record.Level.String()))
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

// source is the package directory, file name, and line of the caller, never
// the full path: [sqlite/stores.go:42]. The package is part of the name so two
// files that share a base name can never be confused in a log.
func source(record slog.Record) string {
	if record.PC == 0 {
		return "?:0"
	}
	frame, _ := runtime.CallersFrames([]uintptr{record.PC}).Next()
	if frame.File == "" {
		return "?:0"
	}
	name := filepath.Base(frame.File)
	if pkg := filepath.Base(filepath.Dir(frame.File)); pkg != "." && pkg != "" {
		name = pkg + "/" + name
	}
	return name + ":" + strconv.Itoa(frame.Line)
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
	if value == "" || strings.ContainsAny(value, " \t\n\r\"=") {
		return strconv.Quote(value)
	}
	return value
}
