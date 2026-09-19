package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

func TestCheckNoSecret(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"plain", "hello", false},
		{"literal dollar", "cost: $5", false},
		{"secret", "token ${API_TOKEN}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkNoSecret(tc.text)
			if (err != nil) != tc.want {
				t.Fatalf("checkNoSecret(%q) = %v, want error=%v", tc.text, err, tc.want)
			}
		})
	}
}

// testRun builds the minimal state a message or location step needs.
func testRun() *run {
	return &run{
		ctx:    context.Background(),
		engine: New(Options{}),
		tctx:   newtemplateContext(),
		msg:    botflota.IncomingMessage{Sender: "5215500000000"},
	}
}

func TestMessageRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.message(spec.Message{Body: spec.Text{"token ${API_TOKEN}"}})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("message error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}

func TestMessageQueuesPlainBody(t *testing.T) {
	r := testRun()
	if err := r.message(spec.Message{Body: spec.Text{"hi there"}}); err != nil {
		t.Fatalf("message: %v", err)
	}
	if len(r.out) != 1 {
		t.Fatalf("queued %d messages, want one", len(r.out))
	}
}

func TestLocationRejectsSecret(t *testing.T) {
	r := testRun()
	err := r.location(spec.Location{Latitude: 1, Longitude: 2, Name: "${API_TOKEN}"})
	if !errors.Is(err, errSecretInMessage) {
		t.Fatalf("location error = %v, want errSecretInMessage", err)
	}
	if len(r.out) != 0 {
		t.Fatalf("queued %d messages, want none", len(r.out))
	}
}
