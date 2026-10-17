package testchannel_test

import (
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/conformance"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
)

// TestConformance runs the reusable suite against the adapter's real
// definition, backend, stores, fleet, engine, and API. The adapter supplies
// only its own files: this is the whole onboarding.
func TestConformance(t *testing.T) {
	backend := testchannel.NewBackend()
	conformance.Run(t, conformance.Subject{
		Name: "testchannel",
		Install: func(bindings botflota.BindingStore) channels.Definition {
			def := testchannel.Definition()
			deps := testchannel.Deps{Bindings: bindings, Backend: backend}
			def.Build = testchannel.Builder(deps)
			def.Prepare = testchannel.Prepare(deps)
			def.Setup = testchannel.Setup(deps)
			return def
		},
		Options: conformance.Options(t, "options:\n  namespace: conf-profile\n"),
		Binding: func(scope, bot string) botflota.Binding {
			return botflota.Binding{
				Scope: scope, Bot: bot,
				Provider: testchannel.Provider, Adapter: testchannel.Kind,
				Mode: testchannel.Mode, Namespace: "conf-profile",
				Identity: botflota.IdentityKey{
					Provider: testchannel.Provider, Subject: "acct-conf",
				},
			}
		},
		Channel: func(scope, bot string) conformance.Channel {
			channel, ok := backend.Lookup(scope, bot)
			if !ok {
				t.Fatalf("the definition never built a channel for %s/%s", scope, bot)
			}
			return channel
		},
	})
}
