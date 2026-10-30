package loadtest

import "testing"

// TestPacingSettingsReachEveryBot proves the run's pacing knobs configure the
// fleet itself, so a report cannot claim pacing while the bots run unpaced.
func TestPacingSettingsReachEveryBot(t *testing.T) {
	l := layout{
		Scopes: 2, Bots: 5, Chats: 1,
		QueueDelayMS: 2000, JitterMS: 500, MaxPerMinute: 20,
	}
	cfg := l.config(t)
	for i := 0; i < l.Bots; i++ {
		bot := cfg.Scopes[l.scopeName(l.scopeOf(i))].Bots[l.bot(i)]
		if bot.Settings == nil {
			t.Fatalf("%s has no outbox settings", l.bot(i))
		}
		got := *bot.Settings
		if got.QueueDelay != 2000 || got.Jitter != 500 || got.MaxMsgPerMinute != 20 {
			t.Fatalf("%s pacing = %+v, want 2000ms/500ms/20", l.bot(i), got)
		}
	}
}

// TestNoPacingLeavesSettingsUnset keeps the unpaced capacity runs honest: with
// no knob the bots keep the adapter defaults instead of a zero-value policy.
func TestNoPacingLeavesSettingsUnset(t *testing.T) {
	l := layout{Scopes: 1, Bots: 2, Chats: 1}
	cfg := l.config(t)
	for i := 0; i < l.Bots; i++ {
		if got := cfg.Scopes["s0"].Bots[l.bot(i)].Settings; got != nil {
			t.Fatalf("%s settings = %+v, want none", l.bot(i), *got)
		}
	}
}
