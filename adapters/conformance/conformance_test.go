package conformance_test

import (
	"context"
	"sync"
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/conformance"
	whatsapp "github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// fakeBackend is the suite's contrasting second subject: a hand-written
// text-only channel with no account setup, no message space, and no adapter
// options. It shares no code with the test adapter, so the suite proves it is
// reusable rather than written around one adapter.
type fakeBackend struct {
	mu       sync.Mutex
	channels map[string]*fakeChannel
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{channels: map[string]*fakeChannel{}}
}

func (b *fakeBackend) register(scope, bot string, channel *fakeChannel) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.channels[scope+"/"+bot] = channel
}

func (b *fakeBackend) lookup(scope, bot string) *fakeChannel {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.channels[scope+"/"+bot]
}

// fakeChannel is one bot's view of the contrasting fake.
type fakeChannel struct {
	incoming chan botflota.IncomingMessage

	mu   sync.Mutex
	sent []botflota.OutgoingMessage
}

func (c *fakeChannel) Connect(context.Context) error { return nil }

func (c *fakeChannel) Disconnect(context.Context) error { return nil }

func (c *fakeChannel) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return "fake-1", nil
}

func (c *fakeChannel) Incoming() <-chan botflota.IncomingMessage {
	return c.incoming
}

func (c *fakeChannel) Inject(msg botflota.IncomingMessage) { c.incoming <- msg }

func (c *fakeChannel) Sent() []botflota.OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]botflota.OutgoingMessage(nil), c.sent...)
}

// fakeSubject installs the contrasting definition over the fixture's store.
func fakeSubject(backend *fakeBackend) conformance.Subject {
	return conformance.Subject{
		Name: "contrast-fake",
		Install: func(botflota.BindingStore) channels.Definition {
			return channels.Definition{
				Provider: "contrast", Kind: "fake", Modes: []string{"text"},
				Capabilities: channels.Capabilities{
					Send:    []botflota.MessageKind{botflota.KindText},
					Receive: []botflota.MessageKind{botflota.KindText},
				},
				ValidateTarget: func(target botflota.ChatTarget) []string {
					if target.Provider != "contrast" {
						return []string{"target provider is not contrast"}
					}
					if target.Kind != botflota.ChatDirect {
						return []string{"shared conversations are not supported"}
					}
					return nil
				},
				Build: func(bind channels.Bind) (botflota.MessageChannel, error) {
					channel := &fakeChannel{
						incoming: make(chan botflota.IncomingMessage, 16),
					}
					backend.register(bind.Scope, bind.Bot, channel)
					return channel, nil
				},
			}
		},
		Binding: func(scope, bot string) botflota.Binding {
			return botflota.Binding{
				Scope: scope, Bot: bot,
				Provider: "contrast", Adapter: "fake", Mode: "text",
				Identity: botflota.IdentityKey{Provider: "contrast", Subject: "peer"},
			}
		},
		Channel: func(scope, bot string) conformance.Channel {
			return backend.lookup(scope, bot)
		},
	}
}

// TestContrastingFakeSubject proves the suite is adapter-independent: the same
// checks pass for a hand-written channel that shares no code with the test
// adapter.
func TestContrastingFakeSubject(t *testing.T) {
	conformance.Run(t, fakeSubject(newFakeBackend()))
}

// cardSubject declares a channel that composes two of the four card variants
// and names the contact kind first, so the suite's supported content is a card
// carrying real entries: both halves of the per-variant admission boundary run
// (declared accepted, undeclared refused) as does the same check under config
// validation.
func cardSubject(backend *fakeBackend) conformance.Subject {
	return conformance.Subject{
		Name: "card-variants",
		Install: func(botflota.BindingStore) channels.Definition {
			return channels.Definition{
				Provider: "card", Kind: "fake", Modes: []string{"text"},
				Capabilities: channels.Capabilities{
					Send: []botflota.MessageKind{
						botflota.KindContact, botflota.KindText,
					},
					Receive: []botflota.MessageKind{botflota.KindText},
					CardVariants: []spec.ContactEntryKind{
						spec.ContactPhone, spec.ContactURL,
					},
				},
				ValidateTarget: func(target botflota.ChatTarget) []string {
					if target.Provider != "card" {
						return []string{"target provider is not card"}
					}
					if target.Kind != botflota.ChatDirect {
						return []string{"shared conversations are not supported"}
					}
					return nil
				},
				Build: func(bind channels.Bind) (botflota.MessageChannel, error) {
					channel := &fakeChannel{
						incoming: make(chan botflota.IncomingMessage, 16),
					}
					backend.register(bind.Scope, bind.Bot, channel)
					return channel, nil
				},
			}
		},
		Binding: func(scope, bot string) botflota.Binding {
			return botflota.Binding{
				Scope: scope, Bot: bot,
				Provider: "card", Adapter: "fake", Mode: "text",
				Identity: botflota.IdentityKey{Provider: "card", Subject: "peer"},
			}
		},
		Channel: func(scope, bot string) conformance.Channel {
			return backend.lookup(scope, bot)
		},
	}
}

// TestCardVariantCapabilityConsistency proves the suite's per-variant gate can
// fail: an undeclared variant is refused, and a declared one is accepted.
func TestCardVariantCapabilityConsistency(t *testing.T) {
	conformance.Run(t, cardSubject(newFakeBackend()))
}

// TestWhatsappDeclaration runs the store-free declaration checks against the
// real installed WhatsApp definition, which cannot connect here. It shows the
// suite's pure half applies to production adapters too.
func TestWhatsappDeclaration(t *testing.T) {
	conformance.Definition(t, "whatsapp", whatsapp.Definition())
}
