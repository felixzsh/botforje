package whatsapp

import (
	"context"

	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
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
	if event.Info.IsFromMe || event.IsEdit {
		return
	}
	// An unclassifiable live payload is dropped rather than turned into a
	// conversation turn; the import still keeps it as an anchor (ADR 0036).
	content, ok := contentType(event.Message)
	if !ok || content.MessageKind() == botflota.KindUnknown {
		return
	}
	id := string(event.Info.ID)
	if !a.seen.fresh(id) {
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
	select {
	case a.incoming <- message:
	default:
		a.log.Warn("incoming queue full, dropping message", "sender", sender)
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
