package fleet

import (
	"testing"

	"github.com/felixzsh/botflota"
)

func TestFilterAllow(t *testing.T) {
	direct := botflota.IncomingMessage{
		Sender: waKey("52155"), Target: waTarget("52155"),
	}
	// The same subject string inside another provider's identity space is a
	// different peer, so no allow/ignore entry of the bot's provider names it.
	otherProvider := botflota.IncomingMessage{
		Sender: botflota.IdentityKey{Provider: "signal", Subject: "52155"},
		Target: botflota.ChatTarget{
			Provider: "signal", Kind: botflota.ChatDirect, Thread: "52155",
		},
	}
	group := botflota.IncomingMessage{
		Sender: waKey("52155"),
		Target: botflota.ChatTarget{
			Provider: "whatsapp", Kind: botflota.ChatGroup, Thread: "52155",
		},
	}

	cases := []struct {
		name   string
		filter Filter
		msg    botflota.IncomingMessage
		want   bool
	}{
		{"direct is allowed", Filter{Provider: "whatsapp"}, direct, true},
		{"shared conversation is refused", Filter{Provider: "whatsapp"}, group, false},
		{
			"ignored sender",
			Filter{Provider: "whatsapp", Ignored: []string{"52155"}}, direct, false,
		},
		{
			"allowed sender",
			Filter{Provider: "whatsapp", Allowed: []string{"52155"}}, direct, true,
		},
		{
			"not in the allow list",
			Filter{Provider: "whatsapp", Allowed: []string{"other"}}, direct, false,
		},
		{
			"an ignored subject is not ignored under another provider",
			Filter{Provider: "whatsapp", Ignored: []string{"52155"}},
			otherProvider, true,
		},
		{
			"an allowed subject is not allowed under another provider",
			Filter{Provider: "whatsapp", Allowed: []string{"52155"}},
			otherProvider, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Allow(tc.msg); got != tc.want {
				t.Fatalf("Allow = %v, want %v", got, tc.want)
			}
		})
	}
}
