package whatsapp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestWaLoGFiltersBelowMinimum(t *testing.T) {
	var buffer bytes.Buffer
	wa := NewWaLog(botflota.NewLogger("debug", &buffer), slog.LevelWarn)
	wa.Debugf("dropped-debug")
	wa.Infof("dropped-info")
	wa.Warnf("kept-warn")
	wa.Errorf("kept-error")

	out := buffer.String()
	if strings.Contains(out, "dropped") {
		t.Fatalf("below minimum leaked: %q", out)
	}
	if !strings.Contains(out, "kept-warn") || !strings.Contains(out, "kept-error") {
		t.Fatalf("at or above minimum dropped: %q", out)
	}
}
