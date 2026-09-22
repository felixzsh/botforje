package botflota

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestPairingSessionLogsWithoutItsPayload proves the credential boundary of
// a pairing session: the state is loggable, the QR payload is not (ADR 0025).
func TestPairingSessionLogsWithoutItsPayload(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("debug", &buf)
	now := time.Date(2026, 9, 21, 20, 0, 0, 0, time.UTC)
	logger.Info("pairing session", "session", PairingSession{
		Scope: "support", Bot: "demo", Method: "qr",
		State: PairWaiting, Code: "qr-secret-payload",
		StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(15 * time.Minute),
	})

	line := buf.String()
	if line == "" {
		t.Fatal("nothing was logged")
	}
	if strings.Contains(line, "qr-secret-payload") {
		t.Fatalf("the QR payload leaked into the log: %s", line)
	}
	for _, want := range []string{"scope=support", "waiting", "code_present=true"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q lacks %q", line, want)
		}
	}
}
