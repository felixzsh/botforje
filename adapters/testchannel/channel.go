package testchannel

import (
	"context"
	"fmt"
	"sync"

	"github.com/felixzsh/botflota"
)

// Backend is one testchannel installation's shared in-memory provider: the
// logical channels it built per bot, so a caller can drive setup, inbound
// delivery, and confirmation without a real service. A production adapter's
// equivalent is its provider connection; this one keeps it in memory.
type Backend struct {
	mu       sync.Mutex
	channels map[botflota.BotRef]*Channel
}

// NewBackend returns an empty in-memory provider.
func NewBackend() *Backend {
	return &Backend{channels: map[botflota.BotRef]*Channel{}}
}

// Lookup returns the channel this installation built for one bot, so a test
// can inject inbound messages and read what was delivered.
func (b *Backend) Lookup(scope, bot string) (*Channel, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	found, ok := b.channels[botflota.BotRef{Scope: scope, Bot: bot}]
	return found, ok
}

// newChannel builds and registers one bot's logical view.
func (b *Backend) newChannel(scope, bot string, options BotOptions) *Channel {
	b.mu.Lock()
	defer b.mu.Unlock()
	channel := &Channel{
		scope: scope, bot: bot, label: options.Label,
		namespace: options.Namespace,
		incoming:  make(chan botflota.IncomingMessage, 16),
		state:     "connected",
	}
	b.channels[botflota.BotRef{Scope: scope, Bot: bot}] = channel
	return channel
}

// Channel is one bot's logical view of its committed testchannel account. It
// implements the transport port plus the optional state, confirmation, and
// observation capabilities this adapter honestly declares.
type Channel struct {
	scope     string
	bot       string
	label     string
	namespace string

	incoming chan botflota.IncomingMessage

	mu       sync.Mutex
	sent     []botflota.OutgoingMessage
	receipts []botflota.ReceiveReceipt

	sentCount    int
	receiptCount int
	// retain caps the samples kept for later inspection; 0 keeps every one.
	// Counts stay exact either way, so a long run observes a bounded history.
	retain int
	onSend func(botflota.OutgoingMessage)
	nextID int64
	state  string
}

// Connect marks the view live. A testchannel account needs no handshake.
func (c *Channel) Connect(context.Context) error {
	c.setState("connected")
	return nil
}

// Disconnect marks the view stopped without closing its inbound stream: the
// fleet's run loop already ends the bot, and a later build gets a new view.
func (c *Channel) Disconnect(context.Context) error {
	c.setState("stopped")
	return nil
}

// Send records one delivery and returns its profile-local id. The id is unique
// within the committed account namespace, which is what MessageSpace reports.
func (c *Channel) Send(
	_ context.Context, msg botflota.OutgoingMessage,
) (string, error) {
	c.mu.Lock()
	c.nextID++
	id := fmt.Sprintf("%s-%d", c.bot, c.nextID)
	c.sent = append(c.sent, msg)
	c.sentCount++
	c.trimLocked()
	observer := c.onSend
	c.mu.Unlock()
	if observer != nil {
		observer(msg)
	}
	return id, nil
}

// OnSend registers one observer called after every recorded delivery. It lets a
// long run measure each send as it happens instead of retaining the history,
// and it must not block: the observing bot's send already happened.
func (c *Channel) OnSend(observer func(botflota.OutgoingMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onSend = observer
}

// Retain caps how many sent messages and receipts this view keeps for later
// inspection. A non-positive value keeps every one, which is what a short
// behavioral test expects; a run over hundreds of bots keeps a small sample.
func (c *Channel) Retain(samples int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retain = samples
	c.trimLocked()
}

// SentCount reports how many deliveries this view recorded, including any
// dropped from the retained sample.
func (c *Channel) SentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sentCount
}

// ReceiptCount reports how many processing outcomes this view recorded,
// including any dropped from the retained sample.
func (c *Channel) ReceiptCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.receiptCount
}

// trimLocked drops the oldest samples beyond the retention limit.
func (c *Channel) trimLocked() {
	if c.retain <= 0 {
		return
	}
	if len(c.sent) > c.retain {
		c.sent = append(c.sent[:0], c.sent[len(c.sent)-c.retain:]...)
	}
	if len(c.receipts) > c.retain {
		c.receipts = append(c.receipts[:0], c.receipts[len(c.receipts)-c.retain:]...)
	}
}

// Incoming is the stream a caller injects into.
func (c *Channel) Incoming() <-chan botflota.IncomingMessage { return c.incoming }

// ChannelState implements botflota.StateReporter (ADR 0011).
func (c *Channel) ChannelState() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// ConfirmReceive records one entry's agreed processing outcome, so a test can
// prove the fleet confirms after the turn commits and never on handoff
// (ADR 0004, ADR 0040).
func (c *Channel) ConfirmReceive(
	_ context.Context, receipt botflota.ReceiveReceipt,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.receipts = append(c.receipts, receipt)
	c.receiptCount++
	c.trimLocked()
	return nil
}

// Inject delivers one inbound message through the running bot. The adapter
// fills its own message space and, when the caller supplied none, a receive
// cursor, so a test only writes the neutral fields shared code understands.
func (c *Channel) Inject(msg botflota.IncomingMessage) {
	// A behavioral test has no deadline of its own; a load generator uses
	// InjectContext so a stopped fleet cannot trap its sender.
	_ = c.InjectContext(context.Background(), msg)
}

// InjectContext delivers one inbound message and gives up when ctx ends,
// reporting ctx's error. It is the cancellable form a long or bounded run
// needs: a full inbound buffer blocks until the bot drains it.
func (c *Channel) InjectContext(
	ctx context.Context, msg botflota.IncomingMessage,
) error {
	msg = c.prepare(msg)
	select {
	case c.incoming <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// prepare fills the adapter-owned fields of one inbound message.
func (c *Channel) prepare(msg botflota.IncomingMessage) botflota.IncomingMessage {
	if msg.Bot == "" {
		msg.Bot = c.bot
	}
	if msg.MessageSpace == "" {
		msg.MessageSpace = c.namespace
	}
	if msg.ReceiveCursor == "" {
		msg.ReceiveCursor = "cursor-" + msg.ProviderMessageID
	}
	return msg
}

// Sent returns a copy of what the channel delivered, oldest first.
func (c *Channel) Sent() []botflota.OutgoingMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]botflota.OutgoingMessage(nil), c.sent...)
}

// Receipts returns a copy of the processing outcomes the fleet confirmed.
func (c *Channel) Receipts() []botflota.ReceiveReceipt {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]botflota.ReceiveReceipt(nil), c.receipts...)
}

func (c *Channel) setState(state string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}
