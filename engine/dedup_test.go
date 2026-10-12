package engine_test

import (
	"context"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/engine"
)

func TestDedupIsScopedToTheConsumingBot(t *testing.T) {
	for _, tc := range []struct {
		name, scope, bot string
	}{
		{"another bot", "support", "other-bot"},
		{"another scope", "other-scope", "support-bot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMemStore()
			scope := loadScope(t)
			makeEngine := func(scopeName, bot string) *engine.Engine {
				return newTestEngine(t, engine.Options{
					Scope: scopeName, Bot: bot,
					Graph: scope.Graphs["faq"], Actions: scope.Actions,
					Store: store, Chats: store, Peers: store, Messages: store, Turn: store,
				})
			}
			first := makeEngine("support", "support-bot")
			second := makeEngine(tc.scope, tc.bot)
			for _, owner := range []struct {
				engine *engine.Engine
				bot    string
			}{{first, "support-bot"}, {second, tc.bot}} {
				msg := botflota.IncomingMessage{
					Bot: owner.bot, Sender: waKey("sender"), Target: waTarget("sender"),
					Content: botflota.TextContent{Body: "1"}, At: t0,
					ProviderMessageID: "shared-id",
				}
				out, err := owner.engine.Handle(ctx, msg)
				if err != nil || len(out) != 1 {
					t.Fatalf("first Handle(%s) = %v, %v; want one reply", owner.bot, out, err)
				}
				out, err = owner.engine.Handle(ctx, msg)
				if err != nil || len(out) != 0 {
					t.Fatalf("redelivery Handle(%s) = %v, %v; want no reply", owner.bot, out, err)
				}
			}
		})
	}
}
