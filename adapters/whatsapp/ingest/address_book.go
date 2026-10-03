package ingest

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
)

// AddressBookImporter copies the linked account's address book into the
// AddressBookStore and registers each entry's phone as a scope contact, so the
// directory holds the people the account knows (ADR 0029, ADR 0037).
type AddressBookImporter struct {
	scope    string
	bot      string
	contacts botflota.ContactStore
	book     botflota.AddressBookStore
	lookup   PNLookup
	log      *slog.Logger
	now      func() time.Time
}

// NewAddressBook returns nil when either store is missing, so a caller can
// treat address-book import as disabled.
func NewAddressBook(
	scope, bot string, contacts botflota.ContactStore, book botflota.AddressBookStore,
	lookup PNLookup, log *slog.Logger,
) *AddressBookImporter {
	if contacts == nil || book == nil {
		return nil
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return &AddressBookImporter{
		scope: scope, bot: bot, contacts: contacts, book: book,
		lookup: lookup, log: log, now: time.Now,
	}
}

// Handle stores one contact-related event. The address-book name, the push
// name, and the business name stay in separate fields, so a later push name
// never erases the name the phone's address book holds.
func (h *AddressBookImporter) Handle(ctx context.Context, raw any) {
	switch event := raw.(type) {
	case *events.Contact:
		if event.Action == nil {
			return
		}
		h.put(ctx, event.JID, event.Action.GetPnJID(),
			contactAt(event.Timestamp, h.now), botflota.AddressBookEntry{
				Name: contactName(event.Action.GetFullName(), event.Action.GetFirstName()),
			})
	case *events.PushName:
		h.put(ctx, event.JID, "", contactAt(time.Time{}, h.now),
			botflota.AddressBookEntry{PushName: event.NewPushName})
	case *events.BusinessName:
		h.put(ctx, event.JID, "", contactAt(time.Time{}, h.now),
			botflota.AddressBookEntry{BusinessName: event.NewBusinessName})
	}
}

// ImportCached covers a session that ended before every contact event
// arrived, and a reconnect whose address-book snapshot was applied silently.
func (h *AddressBookImporter) ImportCached(
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

func (h *AddressBookImporter) ImportAll(
	ctx context.Context, known map[types.JID]types.ContactInfo,
) int {
	stored := 0
	for jid, info := range known {
		if !info.Found {
			continue
		}
		if h.put(ctx, jid, "", h.now().UTC(), botflota.AddressBookEntry{
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
func (h *AddressBookImporter) put(
	ctx context.Context, jid types.JID, pnJID string, at time.Time,
	entry botflota.AddressBookEntry,
) bool {
	if entry.Name == "" && entry.PushName == "" && entry.BusinessName == "" {
		return false
	}
	phone, ok := contactPhone(jid, pnJID, h.lookup)
	if !ok {
		return false
	}
	entry.Scope = h.scope
	entry.Bot = h.bot
	entry.Phone = phone
	entry.UpdatedAt = at
	// The book links to the scope's directory by phone: registering the
	// identity here is what makes an address-book-only number appear as a
	// contact, while the entry's names stay per bot.
	if _, err := h.contacts.EnsureContact(ctx, h.scope, phone, at); err != nil {
		h.log.Warn("register contact for address-book entry", "phone", phone, "error", err)
		return false
	}
	if err := h.book.Put(ctx, &entry); err != nil {
		h.log.Warn("import address-book entry", "phone", phone, "error", err)
		return false
	}
	return true
}

// contactPhone prefers the phone JID the action carries over the entry's own.
func contactPhone(jid types.JID, pnJID string, lookup PNLookup) (string, bool) {
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
