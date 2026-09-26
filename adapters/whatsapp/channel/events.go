package channel

import (
	"context"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

func (a *Adapter) handleEvent(raw any) {
	switch event := raw.(type) {
	case *events.Message:
		a.onMessage(event)
	case *events.HistorySync:
		a.onHistory(event)
	case *events.Contact, *events.PushName, *events.BusinessName:
		a.onContact(raw)
	case *events.Connected:
		a.backoff.Reset()
		a.setState(StateConnected)
		a.onceConnected()
	case *events.Disconnected:
		a.setState(StateConnecting)
		a.signalReconnect()
	case *events.LoggedOut:
		a.setState(StateLoggedOut)
		a.stopReconnecting()
	case *events.TemporaryBan:
		a.setState(StateBanned)
		a.stopReconnecting()
	case *events.StreamReplaced:
		a.setState(StateStopped)
		a.stopReconnecting()
	}
}

func (a *Adapter) onMessage(event *events.Message) {
	if event.IsEdit {
		return
	}
	// An unclassifiable live payload is dropped rather than turned into a
	// conversation turn; the import still keeps it as an anchor (ADR 0036).
	content, ok := ingest.Decode(event.Message)
	if !ok || content.MessageKind() == botflota.KindUnknown {
		return
	}
	id := string(event.Info.ID)
	if !a.seen.fresh(id) {
		return
	}
	if event.Info.IsFromMe {
		a.onOwnMessage(event, id, content)
		return
	}
	pn, ok := resolveSender(event.Info, a.lookupPN)
	if !ok {
		a.log.Warn("dropping message: sender has no known phone number")
		return
	}
	sender := pn.User
	chat := event.Info.Chat

	a.mu.Lock()
	a.last[sender] = lastInbound{
		id: event.Info.ID, chat: chat, sender: event.Info.Sender,
	}
	a.mu.Unlock()

	message := botflota.IncomingMessage{
		Bot:               a.bot,
		SenderPhone:       sender,
		SenderDisplayName: event.Info.PushName,
		Content:           content,
		ProviderMessageID: id,
		IsGroup:           event.Info.IsGroup,
		At:                event.Info.Timestamp,
	}
	// The queue applies backpressure instead of dropping: the whatsmeow event
	// handler waits until the bot actor takes the message (ADR 0011,
	// docs/architecture.md), and closing the adapter unblocks it.
	select {
	case a.incoming <- message:
	case <-a.stop:
		a.log.Debug("message not delivered; adapter stopped", "sender", sender)
	}
}

// onOwnMessage reports a live message that left the linked account: our own
// echo is ignored, and anything else hands the chat to a human (ADR 0043).
// Only 1:1 chats are observed; a group, broadcast, or channel message never
// pauses a chat.
func (a *Adapter) onOwnMessage(
	event *events.Message, id string, content botflota.MessageContent,
) {
	if a.sentByUs(id) {
		return
	}
	if event.Info.IsGroup {
		return
	}
	chat := event.Info.Chat
	if chat.Server != types.DefaultUserServer && chat.Server != types.HiddenUserServer {
		return
	}
	pn, ok := resolveJID(chat, event.Info.RecipientAlt, a.lookupPN)
	if !ok {
		a.log.Warn("dropping own message: chat has no known phone number")
		return
	}
	message := botflota.OutboundMessage{
		Bot:               a.bot,
		RecipientPhone:    pn.User,
		Content:           content,
		ProviderMessageID: id,
		At:                event.Info.Timestamp,
	}
	// Backpressure, like the inbound queue: the event handler waits until the
	// bot actor takes the observation (ADR 0011, ADR 0043).
	select {
	case a.outbound <- message:
	case <-a.stop:
		a.log.Debug("own message not delivered; adapter stopped",
			"sender", pn.User)
	}
}

// onHistory queues chunks for the import worker, so a large bootstrap never
// blocks the event loop (ADR 0035).
func (a *Adapter) onHistory(event *events.HistorySync) {
	if a.importer == nil {
		return
	}
	select {
	case a.history <- event:
	case <-a.stop:
	}
}

func (a *Adapter) onContact(raw any) {
	if a.contacts == nil {
		return
	}
	a.contacts.Handle(context.Background(), raw)
}

// onceConnected copies the cached address book once per adapter: a reconnect
// whose snapshot was applied silently would otherwise never reach the store.
func (a *Adapter) onceConnected() {
	if a.contacts == nil {
		return
	}
	a.contactsOnce.Do(func() { go a.importAllContacts() })
}

// importAllContacts runs off the event loop because the book can hold
// thousands of entries.
func (a *Adapter) importAllContacts() {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || client.Store == nil {
		return
	}
	a.contacts.ImportCached(context.Background(), client.Store.Contacts)
}

// importHistory drains the queued sync chunks until the adapter stops. It is
// the only caller of the importer, so an on-demand response and a background
// chunk never import concurrently.
func (a *Adapter) importHistory() {
	defer close(a.historyDone)
	for {
		select {
		case <-a.stop:
			for {
				select {
				case event := <-a.history:
					a.handleHistory(event)
				default:
					return
				}
			}
		case event := <-a.history:
			a.handleHistory(event)
		}
	}
}

func (a *Adapter) handleHistory(event *events.HistorySync) {
	stats := a.importer.Handle(context.Background(), event)
	a.observeSync(event, stats)
}
