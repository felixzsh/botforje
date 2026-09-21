package whatsapp

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
)

// contactImporter copies the linked account's address book into Botflota's own
// contacts when a bot imports contacts. It never creates a chat or a sender: a
// contact is an identity the account knows, not a conversation, and the
// address book belongs to the account rather than to the scope (ADR 0029,
// ADR 0037).
type contactImporter struct {
	scope    string
	bot      string
	contacts botflota.ContactStore
	lookup   pnLookup
	log      *slog.Logger
	now      func() time.Time
}

// newContactImporter builds an importer, or nil when the store is missing, so
// a caller can treat contact import as disabled.
func newContactImporter(
	scope, bot string, contacts botflota.ContactStore,
	lookup pnLookup, log *slog.Logger,
) *contactImporter {
	if contacts == nil {
		return nil
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return &contactImporter{
		scope: scope, bot: bot, contacts: contacts,
		lookup: lookup, log: log, now: time.Now,
	}
}

// Handle stores one contact-related event. The address-book name, the push
// name, and the verified business name stay in separate fields, so a later
// push name never erases the name the phone's address book holds.
func (h *contactImporter) Handle(ctx context.Context, raw any) {
	switch event := raw.(type) {
	case *events.Contact:
		if event.Action == nil {
			return
		}
		h.put(ctx, event.JID, event.Action.GetPnJID(),
			contactAt(event.Timestamp, h.now), botflota.Contact{
				Name: contactName(event.Action.GetFullName(), event.Action.GetFirstName()),
			})
	case *events.PushName:
		h.put(ctx, event.JID, "", contactAt(time.Time{}, h.now),
			botflota.Contact{PushName: event.NewPushName})
	case *events.BusinessName:
		h.put(ctx, event.JID, "", contactAt(time.Time{}, h.now),
			botflota.Contact{BusinessName: event.NewBusinessName})
	}
}

// ImportCached copies the address book the device store already holds. It
// covers a session that ended before every contact event arrived, and a
// reconnect whose address-book snapshot was applied silently.
func (h *contactImporter) ImportCached(ctx context.Context, known store.ContactStore) {
	if known == nil {
		return
	}
	contacts, err := known.GetAllContacts(ctx)
	if err != nil {
		h.log.Warn("read contacts", "error", err)
		return
	}
	h.ImportAll(ctx, contacts)
}

// ImportAll stores every entry of one address book.
func (h *contactImporter) ImportAll(
	ctx context.Context, known map[types.JID]types.ContactInfo,
) {
	for jid, info := range known {
		if !info.Found {
			continue
		}
		h.put(ctx, jid, "", h.now().UTC(), botflota.Contact{
			Name:         contactName(info.FullName, info.FirstName),
			PushName:     info.PushName,
			BusinessName: info.BusinessName,
		})
	}
}

// put resolves the phone number and stores the entry. A group, broadcast, or
// unresolved hidden-user contact is skipped.
func (h *contactImporter) put(
	ctx context.Context, jid types.JID, pnJID string, at time.Time,
	contact botflota.Contact,
) {
	if contact.Name == "" && contact.PushName == "" && contact.BusinessName == "" {
		return
	}
	phone, ok := contactPhone(jid, pnJID, h.lookup)
	if !ok {
		return
	}
	contact.Scope = h.scope
	contact.Bot = h.bot
	contact.Phone = phone
	contact.UpdatedAt = at
	if err := h.contacts.Put(ctx, &contact); err != nil {
		h.log.Warn("import contact", "phone", phone, "error", err)
	}
}

// contactPhone resolves one contact entry to the domain phone number: the
// phone JID the action carries, the entry's own phone JID, or the LID store
// (ADR 0020).
func contactPhone(jid types.JID, pnJID string, lookup pnLookup) (string, bool) {
	if pnJID != "" {
		if phone, ok := historyJIDPhone(pnJID, lookup); ok {
			return phone, true
		}
	}
	if jid.IsEmpty() {
		return "", false
	}
	return historyJIDPhone(jid.String(), lookup)
}

// contactName picks the address-book name: the full name WhatsApp stores, or
// the first name when the book holds only that.
func contactName(full, first string) string {
	if full != "" {
		return full
	}
	return first
}

// contactAt is the event time, or now when the event carries none.
func contactAt(at time.Time, now func() time.Time) time.Time {
	if at.IsZero() {
		return now().UTC()
	}
	return at.UTC()
}
