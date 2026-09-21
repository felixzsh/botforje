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

// contactImporter copies the linked account's address book into ContactStore
// (ADR 0037).
type contactImporter struct {
	scope    string
	bot      string
	contacts botflota.ContactStore
	lookup   pnLookup
	log      *slog.Logger
	now      func() time.Time
}

// newContactImporter returns nil when the store is missing, so a caller can
// treat contact import as disabled.
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
// name, and the business name stay in separate fields, so a later push name
// never erases the name the phone's address book holds.
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

// ImportCached covers a session that ended before every contact event
// arrived, and a reconnect whose address-book snapshot was applied silently.
func (h *contactImporter) ImportCached(
	ctx context.Context, known store.ContactStore,
) int {
	if known == nil {
		return 0
	}
	contacts, err := known.GetAllContacts(ctx)
	if err != nil {
		h.log.Warn("read contacts", "error", err)
		return 0
	}
	return h.ImportAll(ctx, contacts)
}

func (h *contactImporter) ImportAll(
	ctx context.Context, known map[types.JID]types.ContactInfo,
) int {
	stored := 0
	for jid, info := range known {
		if !info.Found {
			continue
		}
		if h.put(ctx, jid, "", h.now().UTC(), botflota.Contact{
			Name:         contactName(info.FullName, info.FirstName),
			PushName:     info.PushName,
			BusinessName: info.BusinessName,
		}) {
			stored++
		}
	}
	return stored
}

// put stores one entry and reports whether it did; a group, broadcast, or
// unresolved hidden-user contact is skipped.
func (h *contactImporter) put(
	ctx context.Context, jid types.JID, pnJID string, at time.Time,
	contact botflota.Contact,
) bool {
	if contact.Name == "" && contact.PushName == "" && contact.BusinessName == "" {
		return false
	}
	phone, ok := contactPhone(jid, pnJID, h.lookup)
	if !ok {
		return false
	}
	contact.Scope = h.scope
	contact.Bot = h.bot
	contact.Phone = phone
	contact.UpdatedAt = at
	if err := h.contacts.Put(ctx, &contact); err != nil {
		h.log.Warn("import contact", "phone", phone, "error", err)
		return false
	}
	return true
}

// contactPhone prefers the phone JID the action carries over the entry's own.
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

// contactName falls back to the first name when the book holds only that.
func contactName(full, first string) string {
	if full != "" {
		return full
	}
	return first
}

// contactAt falls back to now when the event carries no time.
func contactAt(at time.Time, now func() time.Time) time.Time {
	if at.IsZero() {
		return now().UTC()
	}
	return at.UTC()
}
