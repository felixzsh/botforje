package channel

import (
	"testing"

	"github.com/felixzsh/botflota"
)

// TestThreadRefusesTargetsItCannotAddress covers the outbound target check: the
// direct thread is addressed exactly, while a shared conversation, another
// provider, and a topic — which this adapter cannot represent — are refused
// instead of being sent to the unthreaded chat (ADR 0051).
// TestThreadRefusalsArePermanent: every refused target classifies as
// permanent invalid input, so delivery fails it at once instead of burning
// retries (ADR 0052).
func TestThreadRefusalsArePermanent(t *testing.T) {
	targets := []botflota.ChatTarget{
		{
			Provider: "whatsapp", Kind: botflota.ChatDirect,
			Thread: "5215500000000", Topic: "a-topic",
		},
		{Provider: "whatsapp", Kind: botflota.ChatGroup, Thread: "g"},
		{Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-77"},
		{Provider: "whatsapp", Kind: botflota.ChatDirect},
	}
	for _, target := range targets {
		t.Run(target.Thread+string(target.Kind)+target.Provider, func(t *testing.T) {
			a := &Adapter{}
			_, err := a.thread(target)
			if err == nil {
				t.Fatalf("thread(%+v) succeeded, want a refusal", target)
			}
			failure, ok := botflota.AsSendFailure(err)
			if !ok || !failure.Permanent {
				t.Fatalf("AsSendFailure = %#v, %v, want permanent", failure, ok)
			}
		})
	}
}

func TestThreadRefusesTargetsItCannotAddress(t *testing.T) {
	for _, tc := range []struct {
		name    string
		target  botflota.ChatTarget
		wantErr bool
	}{
		{name: "direct thread", target: waTarget("5215500000000")},
		{
			name: "topic",
			target: botflota.ChatTarget{
				Provider: "whatsapp", Kind: botflota.ChatDirect,
				Thread: "5215500000000", Topic: "a-topic",
			},
			wantErr: true,
		},
		{
			name: "shared conversation",
			target: botflota.ChatTarget{
				Provider: "whatsapp", Kind: botflota.ChatGroup, Thread: "g",
			},
			wantErr: true,
		},
		{
			name: "another provider",
			target: botflota.ChatTarget{
				Provider: "signal", Kind: botflota.ChatDirect, Thread: "thread-77",
			},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Adapter{}
			thread, err := a.thread(tc.target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("thread(%+v) error = %v, wantErr %v", tc.target, err, tc.wantErr)
			}
			if !tc.wantErr && thread != tc.target.Thread {
				t.Fatalf("thread = %q, want %q", thread, tc.target.Thread)
			}
		})
	}
}
