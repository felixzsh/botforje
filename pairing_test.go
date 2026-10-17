package botflota

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestSetupSessionLogsWithoutItsPayload proves the credential boundary of a
// setup session: the state and the step kind are loggable, the display payload
// is not (ADR 0053).
func TestSetupSessionLogsWithoutItsPayload(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("debug", &buf)
	now := time.Date(2026, 9, 21, 20, 0, 0, 0, time.UTC)
	logger.Info("setup session", "session", SetupSession{
		Bot: BotRef{Scope: "support", Bot: "demo"}, ID: "s-1", Method: "qr",
		Mode: "native", State: SetupAwaitingInput, Revision: 3,
		StartedAt: now, UpdatedAt: now, ExpiresAt: now.Add(15 * time.Minute),
		Step: &SetupStep{
			Kind:    SetupStepDisplay,
			Display: &SetupDisplay{Format: "qr", Payload: "qr-secret-payload"},
		},
	})

	line := buf.String()
	if line == "" {
		t.Fatal("nothing was logged")
	}
	if strings.Contains(line, "qr-secret-payload") {
		t.Fatalf("the display payload leaked into the log: %s", line)
	}
	for _, want := range []string{"scope=support", "bot=demo", "awaiting_input",
		"step=display"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q lacks %q", line, want)
		}
	}
}
