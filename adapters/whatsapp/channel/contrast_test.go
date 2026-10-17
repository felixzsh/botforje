package channel

// This file is the P6 item 1 contrast test. It runs one fixture graph through
// three adapter subjects plus an update source, all over the real config,
// SQLite stores, fleet, outbox, engine, and API. The WhatsApp subject reuses
// this package's existing credential-free event-mapping path (newTestAdapter
// plus handleEvent); the other subjects are hand-written fakes. No provider
// branch and no SDK dependency is added.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/conformance"
	"github.com/felixzsh/botflota/adapters/testchannel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// TestChannelNeutralContrast proves the runtime is channel-neutral: the same
// fixture graph produces the same behavior for the provider event mapping and
// for two contrasting fakes, one with opaque provider-wide ids and one whose
// ids are local to a committed profile. The shared-backend and update-source
// cases run for the fakes; the provider subject has neither.
func TestChannelNeutralContrast(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subject conformance.Subject
	}{
		{"mapped-provider", newWhatsappSubject(t)},
		{"opaque-ids", newOpaqueSubject()},
		{"profile-local", newProfileSubject(t)},
		{"http-update-source", newSourceSubject()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conformance.RunContrast(t, tc.subject)
		})
	}
}

// ------------------------------------------------ mapped provider subject

// mappingView adapts this package's real Adapter to the conformance Channel:
// Inject runs the adapter's real event mapping, and Sent is the transport
// stand-in for the outbound direction (the mapping under test is inbound).
type mappingView struct {
	adapter *Adapter

	mu   sync.Mutex
	sent []botflota.OutgoingMessage
	next int64
}

func (v *mappingView) Connect(context.Context) error    { return nil }
func (v *mappingView) Disconnect(context.Context) error { return nil }

func (v *mappingView) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.next++
	v.sent = append(v.sent, msg)
	return fmt.Sprintf("wa-%d", v.next), nil
}

func (v *mappingView) Incoming() <-chan botflota.IncomingMessage {
	return v.adapter.incoming
}

func (v *mappingView) Inject(msg botflota.IncomingMessage) { v.deliver(msg) }

func (v *mappingView) Sent() []botflota.OutgoingMessage {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]botflota.OutgoingMessage(nil), v.sent...)
}

// deliver maps one neutral inbound through the adapter's real provider event
// handler, so the engine sees exactly what a live provider event produces.
func (v *mappingView) deliver(msg botflota.IncomingMessage) {
	v.adapter.mu.Lock()
	v.adapter.ownPhone = msg.Target.Thread
	v.adapter.mu.Unlock()
	body := botflota.ContentText(msg.Content)
	v.adapter.handleEvent(textEvent(msg.Sender.Subject, msg.ProviderMessageID, body))
}

// newWhatsappSubject installs the real WhatsApp definition over this
// package's credential-free test path. Only Build is overridden, with an
// adapter that never dials: the declaration, target rules, and message space
// stay the real adapter's.
func newWhatsappSubject(t *testing.T) conformance.Subject {
	t.Helper()
	views := map[botflota.BotRef]*mappingView{}
	def := Definition()
	return conformance.Subject{
		Name: "mapped-provider",
		Install: func(botflota.BindingStore) channels.Definition {
			installed := def
			installed.Build = func(bind channels.Bind) (botflota.MessageChannel, error) {
				adapter := newTestAdapter()
				adapter.bot = bind.Bot
				view := &mappingView{adapter: adapter}
				views[botflota.BotRef{Scope: bind.Scope, Bot: bind.Bot}] = view
				return view, nil
			}
			return installed
		},
		Options: spec.ChannelOptions{},
		Binding: func(scope, bot string) botflota.Binding {
			return botflota.Binding{
				Scope: scope, Bot: bot,
				Provider: Provider, Adapter: def.Kind, Mode: Mode,
				Identity: botflota.IdentityKey{
					Provider: Provider, Subject: "15551230000",
				},
			}
		},
		Channel: func(scope, bot string) conformance.Channel {
			return views[botflota.BotRef{Scope: scope, Bot: bot}]
		},
		Deliver: func(scope, bot string, msg botflota.IncomingMessage) {
			view := views[botflota.BotRef{Scope: scope, Bot: bot}]
			if view == nil {
				t.Fatalf("mapped-provider: no live view for %s", bot)
			}
			view.deliver(msg)
		},
		SupportedTarget: botflota.ChatTarget{
			Provider: Provider, Kind: botflota.ChatDirect, Thread: "15551230000",
		},
	}
}

// -------------------------------------------------------- opaque fake

// fakeCore is the shared in-memory transport of both hand-written fakes: one
// logical view per bot, with the adapter's own opaque message ids.
type fakeCore struct {
	incoming chan botflota.IncomingMessage

	mu     sync.Mutex
	sent   []botflota.OutgoingMessage
	next   int64
	prefix string
	space  string
}

func (c *fakeCore) Connect(context.Context) error    { return nil }
func (c *fakeCore) Disconnect(context.Context) error { return nil }

func (c *fakeCore) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	c.sent = append(c.sent, msg)
	return fmt.Sprintf("%s-%d", c.prefix, c.next), nil
}

func (c *fakeCore) Incoming() <-chan botflota.IncomingMessage { return c.incoming }

func (c *fakeCore) Inject(msg botflota.IncomingMessage) { c.inject(msg) }

func (c *fakeCore) inject(msg botflota.IncomingMessage) {
	if msg.MessageSpace == "" {
		msg.MessageSpace = c.space
	}
	if msg.ReceiveCursor == "" {
		msg.ReceiveCursor = "cursor-" + msg.ProviderMessageID
	}
	c.incoming <- msg
}

func (c *fakeCore) Sent() []botflota.OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]botflota.OutgoingMessage(nil), c.sent...)
}

// opaqueChannel declares no optional operation beyond the transport port.
type opaqueChannel struct{ *fakeCore }

// fakeBackend is one installation's shared in-memory provider. Two bindings
// over one backend get two views; nothing is shared between them but the map.
type fakeBackend struct {
	mu       sync.Mutex
	channels map[botflota.BotRef]*fakeCore
	prefix   string
	space    func(scope, bot string) string
}

func newFakeBackend(prefix string, space func(scope, bot string) string) *fakeBackend {
	return &fakeBackend{
		channels: map[botflota.BotRef]*fakeCore{},
		prefix:   prefix,
		space:    space,
	}
}

func (b *fakeBackend) build(scope, bot string) *fakeCore {
	b.mu.Lock()
	defer b.mu.Unlock()
	core := &fakeCore{
		incoming: make(chan botflota.IncomingMessage, 16),
		prefix:   b.prefix,
		space:    b.space(scope, bot),
	}
	b.channels[botflota.BotRef{Scope: scope, Bot: bot}] = core
	return core
}

func (b *fakeBackend) lookup(scope, bot string) *fakeCore {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.channels[botflota.BotRef{Scope: scope, Bot: bot}]
}

// opaqueDefinition declares a text-only channel whose ids are opaque and
// provider-wide: its message space is the committed account subject, so two
// accounts are two spaces. It declares no optional operation.
func opaqueDefinition() channels.Definition {
	const provider = "opaque"
	return channels.Definition{
		Provider: provider, Kind: "fake", Modes: []string{"text"},
		Capabilities: channels.Capabilities{
			Send:    []botflota.MessageKind{botflota.KindText},
			Receive: []botflota.MessageKind{botflota.KindText},
		},
		ValidateTarget: func(target botflota.ChatTarget) []string {
			if target.Provider != provider {
				return []string{"target provider is not opaque"}
			}
			if target.Kind != botflota.ChatDirect {
				return []string{"shared conversations are not supported"}
			}
			return nil
		},
		MessageSpace: func(binding *botflota.Binding) string {
			if binding == nil || binding.Identity.Provider != provider {
				return ""
			}
			return binding.Identity.Subject
		},
	}
}

func opaqueBinding(scope, bot string) botflota.Binding {
	return botflota.Binding{
		Scope: scope, Bot: bot,
		Provider: "opaque", Adapter: "fake", Mode: "text",
		Identity: botflota.IdentityKey{
			Provider: "opaque", Subject: "opaque-" + bot,
		},
	}
}

func newOpaqueSubject() conformance.Subject {
	backend := newFakeBackend("opaque", func(_, bot string) string {
		return "opaque-" + bot
	})
	return conformance.Subject{
		Name: "opaque-ids",
		Install: func(botflota.BindingStore) channels.Definition {
			def := opaqueDefinition()
			def.Build = func(bind channels.Bind) (botflota.MessageChannel, error) {
				return &opaqueChannel{backend.build(bind.Scope, bind.Bot)}, nil
			}
			return def
		},
		Options: spec.ChannelOptions{},
		Binding: opaqueBinding,
		Channel: func(scope, bot string) conformance.Channel {
			return &opaqueChannel{backend.lookup(scope, bot)}
		},
		Peer: &conformance.Peer{Bot: "peer", Binding: opaqueBinding},
	}
}

// ------------------------------------------------------- update source

// sourceChannel is a webhook-style update source: it records the processing
// outcome the fleet settles for every delivered entry, so a test can read the
// confirmations off the real ReceiveConfirmer path.
type sourceChannel struct {
	*fakeCore

	receiptMu sync.Mutex
	receipts  []botflota.ReceiveReceipt
}

func (c *sourceChannel) ConfirmReceive(
	_ context.Context, receipt botflota.ReceiveReceipt,
) error {
	c.receiptMu.Lock()
	defer c.receiptMu.Unlock()
	c.receipts = append(c.receipts, receipt)
	return nil
}

func (c *sourceChannel) Receipts() []botflota.ReceiveReceipt {
	c.receiptMu.Lock()
	defer c.receiptMu.Unlock()
	return append([]botflota.ReceiveReceipt(nil), c.receipts...)
}

// sourceDefinition is the same honest text-only declaration plus the one
// optional operation this source really implements: processing confirmation
// (ADR 0004, ADR 0040).
func sourceDefinition() channels.Definition {
	const provider = "source"
	return channels.Definition{
		Provider: provider, Kind: "fake", Modes: []string{"text"},
		Capabilities: channels.Capabilities{
			Send:    []botflota.MessageKind{botflota.KindText},
			Receive: []botflota.MessageKind{botflota.KindText},
		},
		ValidateTarget: func(target botflota.ChatTarget) []string {
			if target.Provider != provider {
				return []string{"target provider is not source"}
			}
			if target.Kind != botflota.ChatDirect {
				return []string{"shared conversations are not supported"}
			}
			return nil
		},
		MessageSpace: func(binding *botflota.Binding) string {
			if binding == nil || binding.Identity.Provider != provider {
				return ""
			}
			return binding.Identity.Subject
		},
	}
}

// sourceBackend is one update source installation's shared provider: it keeps
// the very channel instance the fleet built, so the confirmations the fleet
// settles are the ones a test reads back.
type sourceBackend struct {
	mu       sync.Mutex
	channels map[botflota.BotRef]*sourceChannel
}

func newSourceBackend() *sourceBackend {
	return &sourceBackend{channels: map[botflota.BotRef]*sourceChannel{}}
}

func (b *sourceBackend) build(scope, bot string) *sourceChannel {
	b.mu.Lock()
	defer b.mu.Unlock()
	channel := &sourceChannel{fakeCore: &fakeCore{
		incoming: make(chan botflota.IncomingMessage, 16),
		prefix:   "src",
		space:    "source-" + bot,
	}}
	b.channels[botflota.BotRef{Scope: scope, Bot: bot}] = channel
	return channel
}

func (b *sourceBackend) lookup(scope, bot string) *sourceChannel {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.channels[botflota.BotRef{Scope: scope, Bot: bot}]
}

func newSourceSubject() conformance.Subject {
	backend := newSourceBackend()
	return conformance.Subject{
		Name: "http-update-source",
		Install: func(botflota.BindingStore) channels.Definition {
			def := sourceDefinition()
			def.Build = func(bind channels.Bind) (botflota.MessageChannel, error) {
				return backend.build(bind.Scope, bind.Bot), nil
			}
			return def
		},
		Options: spec.ChannelOptions{},
		Binding: func(scope, bot string) botflota.Binding {
			return botflota.Binding{
				Scope: scope, Bot: bot,
				Provider: "source", Adapter: "fake", Mode: "text",
				Identity: botflota.IdentityKey{
					Provider: "source", Subject: "source-" + bot,
				},
			}
		},
		Channel: func(scope, bot string) conformance.Channel {
			return backend.lookup(scope, bot)
		},
	}
}

// ------------------------------------------------------ profile-local

// newProfileSubject installs the independent profile-local adapter: its
// message ids are local to a committed namespace, so a second profile is a
// second space. Two bindings share the one in-memory backend.
func newProfileSubject(t *testing.T) conformance.Subject {
	t.Helper()
	backend := testchannel.NewBackend()
	namespace := func(bot string) string { return "profile-" + bot }
	options := func(bot string) spec.ChannelOptions {
		return conformance.Options(t, "options:\n  namespace: "+namespace(bot)+"\n")
	}
	binding := func(scope, bot string) botflota.Binding {
		return botflota.Binding{
			Scope: scope, Bot: bot,
			Provider: testchannel.Provider, Adapter: testchannel.Kind,
			Mode: testchannel.Mode, Namespace: namespace(bot),
			Identity: botflota.IdentityKey{
				Provider: testchannel.Provider, Subject: "acct-" + bot,
			},
		}
	}
	return conformance.Subject{
		Name: "profile-local",
		Install: func(bindings botflota.BindingStore) channels.Definition {
			def := testchannel.Definition()
			deps := testchannel.Deps{Bindings: bindings, Backend: backend}
			def.Build = testchannel.Builder(deps)
			def.Prepare = testchannel.Prepare(deps)
			def.Setup = testchannel.Setup(deps)
			return def
		},
		Options: options("bot"),
		OptionsFor: func(_, bot string) spec.ChannelOptions {
			return options(bot)
		},
		Binding: binding,
		Channel: func(scope, bot string) conformance.Channel {
			live, ok := backend.Lookup(scope, bot)
			if !ok {
				t.Fatalf("the definition never built a channel for %s/%s", scope, bot)
			}
			return live
		},
		Peer: &conformance.Peer{
			Bot: "peer", Binding: binding, Options: options("peer"),
		},
	}
}
