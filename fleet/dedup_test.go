package fleet

import (
	"context"
	"testing"

	"github.com/felixzsh/botflota"
)

func TestFleetDedupIsScopedToTheBot(t *testing.T) {
	runner, channels, _ := buildFleetWithStore(t, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = runner.Run(ctx) }()
	waitConnected(t, runner)

	for _, bot := range []string{"alpha", "beta"} {
		t.Run(bot, func(t *testing.T) {
			target := waTarget("sender")
			channels[bot].incoming <- botflota.IncomingMessage{
				Bot: bot, Sender: waKey("sender"), Target: target,
				Content: botflota.TextContent{Body: "hi"}, At: t0,
				ProviderMessageID: "shared-id",
			}
			if sent := receive(t, channels[bot].sent); sent.Target != target {
				t.Fatalf("reply target = %+v, want %+v", sent.Target, target)
			}
		})
	}
}

func TestMessageLookupIsScopedToTheOwner(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	source := waTarget("source")
	destination := waTarget("destination")
	chat, err := store.Ensure(ctx, "dev", "alpha", source, 0, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, &botflota.Message{
		ChatID: chat.ID, Target: destination, ProviderMessageID: "shared-id",
		Content: botflota.TextContent{Body: "known"}, Direction: botflota.Outbound, At: t0,
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, scope, bot string
		target           botflota.ChatTarget
		want             bool
	}{
		{"own destination", "dev", "alpha", destination, true},
		{"history target", "dev", "alpha", source, false},
		{"another bot", "dev", "beta", destination, false},
		{"another scope", "other", "alpha", destination, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.HasProviderID(ctx, tc.scope, tc.bot, tc.target, "shared-id")
			if err != nil || got != tc.want {
				t.Fatalf("HasProviderID = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
