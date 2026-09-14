package inbox

import (
	"testing"

	"github.com/felixzsh/botforje/internal/domain"
)

func TestAllow(t *testing.T) {
	direct := domain.IncomingMessage{Sender: "52155@s.whatsapp.net", ReplyTo: "52155@s.whatsapp.net"}
	group := domain.IncomingMessage{Sender: "52155@s.whatsapp.net", ReplyTo: "123@g.us", IsGroup: true}

	cases := []struct {
		name   string
		filter Filter
		msg    domain.IncomingMessage
		want   bool
	}{
		{"direct is allowed", Filter{}, direct, true},
		{"group ignored by default", Filter{IgnoreGroups: true}, group, false},
		{"group allowed when not ignored", Filter{IgnoreGroups: false}, group, true},
		{"ignored sender", Filter{Ignored: []string{"52155@s.whatsapp.net"}}, direct, false},
		{"ignored lid", Filter{Ignored: []string{"21215@lid"}},
			domain.IncomingMessage{Sender: "52155@s.whatsapp.net", SenderLID: "21215@lid"}, false},
		{"ignored chat", Filter{Ignored: []string{"status@broadcast"}},
			domain.IncomingMessage{Sender: "x", ReplyTo: "status@broadcast"}, false},
		{"allowed sender", Filter{Allowed: []string{"52155@s.whatsapp.net"}}, direct, true},
		{"not in the allow list", Filter{Allowed: []string{"other@s.whatsapp.net"}}, direct, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.filter.Allow(tc.msg); got != tc.want {
				t.Fatalf("Allow = %v, want %v", got, tc.want)
			}
		})
	}
}
