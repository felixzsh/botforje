package fleet

import (
	"testing"

	"github.com/felixzsh/botflota"
)

func TestFilterAllow(t *testing.T) {
	direct := botflota.IncomingMessage{
		Sender: waKey("52155"), Target: waTarget("52155"),
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
		{"direct is allowed", Filter{}, direct, true},
		{"shared conversation is refused", Filter{}, group, false},
		{"ignored sender", Filter{Ignored: []string{"52155"}}, direct, false},
		{"allowed sender", Filter{Allowed: []string{"52155"}}, direct, true},
		{"not in the allow list", Filter{Allowed: []string{"other"}}, direct, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Allow(tc.msg); got != tc.want {
				t.Fatalf("Allow = %v, want %v", got, tc.want)
			}
		})
	}
}
