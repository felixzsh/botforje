package whatsapp

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"

	walog "go.mau.fi/whatsmeow/util/log"
)

// waLog forwards whatsmeow's logger into the structured logger. It builds the
// record itself so the source is whatsmeow's call site, not this adapter, and
// drops anything below the dependency log level.
type waLog struct {
	logger *slog.Logger
	module string
	level  slog.Level
}

func NewWaLog(logger *slog.Logger, level slog.Level) walog.Logger {
	return waLog{logger: logger, module: "whatsmeow", level: level}
}

func (l waLog) Debugf(msg string, args ...any) { l.emit(slog.LevelDebug, msg, args...) }
func (l waLog) Infof(msg string, args ...any)  { l.emit(slog.LevelInfo, msg, args...) }
func (l waLog) Warnf(msg string, args ...any)  { l.emit(slog.LevelWarn, msg, args...) }
func (l waLog) Errorf(msg string, args ...any) { l.emit(slog.LevelError, msg, args...) }

func (l waLog) Sub(module string) walog.Logger {
	return waLog{logger: l.logger, module: l.module + "/" + module, level: l.level}
}

func (l waLog) emit(level slog.Level, msg string, args ...any) {
	message := fmt.Sprintf(msg, args...)
	// On shutdown whatsmeow warns when the close frame races an already-closed
	// socket (framesocket.go); benign, so demote it to debug.
	if level == slog.LevelWarn && strings.HasPrefix(message, "Error sending close to websocket") {
		level = slog.LevelDebug
	}
	if level < l.level {
		return
	}
	handler := l.logger.Handler()
	ctx := context.Background()
	if !handler.Enabled(ctx, level) {
		return
	}
	// Skip emit and the Debugf/Infof/Warnf/Errorf wrapper.
	pc, _, _, _ := runtime.Caller(2)
	record := slog.NewRecord(time.Now(), level, message, pc)
	record.AddAttrs(slog.String("module", l.module))
	_ = handler.Handle(ctx, record)
}
