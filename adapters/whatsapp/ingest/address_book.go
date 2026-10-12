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
// DirectoryStore and registers each entry's peer identity as a scope peer, so
// the directory holds the people the account knows (ADR 0029, ADR 0037).
type AddressBookImporter struct {
	scope  string
	bot    string
	peers  botflota.PeerStore
	book   botflota.DirectoryStore
	lookup PNLookup
	log    *slog.Logger
	now    func() time.Time
	// source is the normalized identity of the account whose book this is, set
	// once the device is loaded (ADR 0051).
	source botflota.IdentityKey
}

// NewAddressBook returns nil when either store is missing, so a caller can
// treat address-book import as disabled.
func NewAddressBook(
	scope, bot string, peers botflota.PeerStore, book botflota.DirectoryStore,
	lookup PNLookup, log *slog.Logger,
) *AddressBookImporter {
	if peers == nil || book == nil {
		return nil
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return &AddressBookImporter{
		scope: scope, bot: bot, peers: peers, book: book,
		lookup: lookup, log: log, now: time.Now,
	}
}

// SetSource records the normalized identity of the account that supplied the
// book, which is known only once the device is loaded.
func (h *AddressBookImporter) SetSource(identity botflota.IdentityKey) {
	h.source = identity
}

// Handle stores one contact-related event. The phone's address-book name wins
// over a business name, which wins over the push name, so a later push name
// never erases the name the phone's address book holds (ADR 0051).
func (h *AddressBookImporter) Handle(ctx context.Context, raw any) {
	switch event := raw.(type) {
	case *events.Contact:
		if event.Action == nil {
			return
		}
		h.put(ctx, event.JID, event.Action.GetPnJID(), true,
			contactAt(event.Timestamp, h.now),
			contactName(event.Action.GetFullName(), event.Action.GetFirstName()))
	case *events.PushName:
		h.put(ctx, event.JID, "", false, contactAt(time.Time{}, h.now),
			event.NewPushName)
	case *events.BusinessName:
		h.put(ctx, event.JID, "", false, contactAt(time.Time{}, h.now),
			event.NewBusinessName)
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
		// Membership comes from the address book alone; a push or business
		// name only names an identity that is already a member, and never
		// fabricates one (ADR 0037, ADR 0051).
		book := contactName(info.FullName, info.FirstName)
		name := directoryName(book, info.BusinessName, info.PushName)
		member := book != ""
		if h.put(ctx, jid, "", member, h.now().UTC(), name) && member {
			stored++
		}
	}
	return stored
}

// put stores one normalized name, creating directory membership only with
// contact-list evidence; a group, broadcast, or unresolved hidden-user contact
// is skipped. An address-book name replaces a stored one, while a push or
// business name only fills a gap.
func (h *AddressBookImporter) put(
	ctx context.Context, jid types.JID, pnJID string, member bool, at time.Time,
	name string,
) bool {
	if !member && name == "" {
		return false
	}
	phone, ok := contactPhone(jid, pnJID, h.lookup)
	if !ok {
		return false
	}
	key := PeerKey(phone)
	entry := botflota.DirectoryEntry{
		Scope:          h.scope,
		Bot:            h.bot,
		PeerKey:        key,
		SourceIdentity: h.source,
		DisplayName:    name,
		UpdatedAt:      at,
	}
	// The book links to the scope's peers by identity: registering the peer
	// here is what makes an address-book-only number appear as a peer, while
	// the entry's name stays per bot.
	peer, err := h.peers.EnsurePeer(ctx, h.scope, key, at)
	if err != nil {
		h.log.Warn("register peer for address-book entry", "peer", phone, "error", err)
		return false
	}
	if name != "" && peer.DisplayName == "" {
		if err := h.peers.SetDisplayName(ctx, h.scope, key, name, at); err != nil {
			h.log.Warn("set peer display name", "peer", phone, "error", err)
		}
	}
	if member {
		err = h.book.Put(ctx, &entry)
	} else {
		err = h.book.Merge(ctx, &entry)
	}
	if err != nil {
		h.log.Warn("import directory entry", "peer", phone, "error", err)
		return false
	}
	return member
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

// directoryName applies the adapter's name precedence to one normalized value
// before the common store sees it (ADR 0051).
func directoryName(book, business, push string) string {
	switch {
	case book != "":
		return book
	case business != "":
		return business
	default:
		return push
	}
}

// contactAt falls back to now when the event carries no time.
func contactAt(at time.Time, now func() time.Time) time.Time {
	if at.IsZero() {
		return now().UTC()
	}
	return at.UTC()
}
