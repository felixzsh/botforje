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

// waLog forwards whatsmeow's logger into the structured logger, so protocol
// warnings and errors land in journalctl/Docker without ANSI colors. It builds
// the record itself so the source is whatsmeow's call site, not this adapter.
type waLog struct {
	logger *slog.Logger
	module string
}

func newWaLog(logger *slog.Logger) walog.Logger {
	return waLog{logger: logger, module: "whatsmeow"}
}

func (l waLog) Debugf(msg string, args ...any) { l.emit(slog.LevelDebug, msg, args...) }
func (l waLog) Infof(msg string, args ...any)  { l.emit(slog.LevelInfo, msg, args...) }
func (l waLog) Warnf(msg string, args ...any)  { l.emit(slog.LevelWarn, msg, args...) }
func (l waLog) Errorf(msg string, args ...any) { l.emit(slog.LevelError, msg, args...) }

func (l waLog) Sub(module string) walog.Logger {
	return waLog{logger: l.logger, module: l.module + "/" + module}
}

func (l waLog) emit(level slog.Level, msg string, args ...any) {
	message := fmt.Sprintf(msg, args...)
	// On shutdown whatsmeow warns when the close frame races a peer that has
	// already closed the socket (framesocket.go); it is benign, so keep it but
	// at debug instead of warning on every clean exit.
	if level == slog.LevelWarn && strings.HasPrefix(message, "Error sending close to websocket") {
		level = slog.LevelDebug
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
