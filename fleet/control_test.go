package fleet

import (
	"testing"
	"time"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// TestBotHandsOverAChat covers the two ways in and the release: an external
// reply counts down, a manual hold does not expire, and a release returns the
// chat to automatic (ADR 0043).
func TestBotHandsOverAChat(t *testing.T) {
	now := t0
	bot := &Bot{
		externalPause: 15 * time.Minute,
		now:           func() time.Time { return now },
		control:       map[string]chatControl{},
	}
	if bot.paused("s", now) {
		t.Fatal("a fresh chat is not paused")
	}
	bot.noteExternal("s", now)
	if !bot.paused("s", now.Add(time.Minute)) {
		t.Fatal("an external reply did not pause the chat")
	}
	if bot.paused("s", now.Add(16*time.Minute)) {
		t.Fatal("the automatic pause outlived its window")
	}
	bot.setHuman("s", true)
	if !bot.paused("s", now.Add(time.Hour)) {
		t.Fatal("a manual hold expired")
	}
	if state := bot.controlOf("s", now.Add(time.Hour)); state.Mode != botflota.ChatHuman {
		t.Fatalf("mode = %q, want human", state.Mode)
	}
	bot.setHuman("s", false)
	if bot.paused("s", now) {
		t.Fatal("a released chat is automatic")
	}
	if state := bot.controlOf("s", now); state.Mode != botflota.ChatAuto {
		t.Fatalf("mode = %q, want auto", state.Mode)
	}
}

// TestExternalReplyRestartsTheCountdown: each new external reply renews the
// window for that chat only (ADR 0043).
func TestExternalReplyRestartsTheCountdown(t *testing.T) {
	now := t0
	bot := &Bot{
		externalPause: 15 * time.Minute,
		now:           func() time.Time { return now },
		control:       map[string]chatControl{},
	}
	bot.noteExternal("s", now)
	bot.noteExternal("s", now.Add(10*time.Minute))
	if !bot.paused("s", now.Add(20*time.Minute)) {
		t.Fatal("the countdown was not restarted")
	}
	if bot.paused("other", now) {
		t.Fatal("another chat was paused too")
	}
}

// TestAnExternalReplyDoesNotReleaseAManualHold: a reply from the phone renews
// the countdown but never hands back a chat an operator is keeping (ADR 0043).
func TestAnExternalReplyDoesNotReleaseAManualHold(t *testing.T) {
	now := t0
	bot := &Bot{
		externalPause: 15 * time.Minute,
		now:           func() time.Time { return now },
		control:       map[string]chatControl{},
	}
	bot.setHuman("s", true)
	bot.noteExternal("s", now)

	// Long past the countdown the external reply started, the hold still stands.
	if !bot.paused("s", now.Add(time.Hour)) {
		t.Fatal("an external reply released a manual hold")
	}
	if state := bot.controlOf("s", now.Add(time.Hour)); state.Mode != botflota.ChatHuman {
		t.Fatalf("mode = %q, want human", state.Mode)
	}
}

// TestAnExplicitAutoCancelsARunningCountdown: releasing a chat means the bot
// answers now, so it does not wait out a countdown already running (ADR 0043).
func TestAnExplicitAutoCancelsARunningCountdown(t *testing.T) {
	now := t0
	bot := &Bot{
		externalPause: 15 * time.Minute,
		now:           func() time.Time { return now },
		control:       map[string]chatControl{},
	}
	bot.noteExternal("s", now)
	if !bot.paused("s", now.Add(time.Minute)) {
		t.Fatal("the chat was not paused")
	}
	bot.setHuman("s", false)
	if bot.paused("s", now.Add(time.Minute)) {
		t.Fatal("an explicit auto did not cancel the countdown")
	}
}

func TestExternalReplyPauseResolvesTheDefault(t *testing.T) {
	if got := externalReplyPause(spec.BotSettings{}); got != 15*time.Minute {
		t.Fatalf("default = %s, want 15m", got)
	}
	if got := externalReplyPause(spec.BotSettings{ExternalReplyPause: 1000}); got != time.Second {
		t.Fatalf("configured = %s, want 1s", got)
	}
}
