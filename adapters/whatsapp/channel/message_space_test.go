package channel

import (
	"testing"

	"github.com/felixzsh/botflota"
)

// TestMessageSpaceFollowsTheLinkedAccount proves the adapter-declared
// message space: the linked account's own subject, so a second account is a
// different space while reauthorization of the same account — a new binding
// generation included — keeps it (ADR 0026, ADR 0051). Binding generation
// is delivery authorization, never a message-ID namespace.
func TestMessageSpaceFollowsTheLinkedAccount(t *testing.T) {
	def := Definition()
	if def.MessageSpace == nil {
		t.Fatal("the WhatsApp definition declares no message space")
	}
	account := func(subject string, generation int64) *botflota.Binding {
		return &botflota.Binding{
			Scope: "dev", Bot: "bot", Provider: Provider,
			Mode:       Mode,
			Identity:   botflota.IdentityKey{Provider: Provider, Subject: subject},
			Generation: generation,
		}
	}
	for _, tc := range []struct {
		name    string
		binding *botflota.Binding
		want    string
	}{
		{"first link", account("5215500000000", 1), "5215500000000"},
		{"reauthorization keeps the space", account("5215500000000", 2), "5215500000000"},
		{"a second account is a different space", account("5215500000001", 1), "5215500000001"},
		{"a foreign provider carries no space", &botflota.Binding{
			Scope: "dev", Bot: "bot", Provider: "pager", Mode: Mode,
			Identity: botflota.IdentityKey{Provider: "pager", Subject: "x"},
		}, ""},
		{"no binding carries no space", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := def.MessageSpace(tc.binding); got != tc.want {
				t.Fatalf("MessageSpace = %q, want %q", got, tc.want)
			}
		})
	}
}
