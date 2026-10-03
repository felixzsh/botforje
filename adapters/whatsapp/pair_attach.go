package whatsapp

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// Full-sync window requested at registration only; WhatsApp stays the
// authority over what it actually sends, and an unset day limit leaves the
// phone on its own much shorter default.
const (
	fullSyncDays = 3650
	fullSyncSize = 10240
	storageQuota = 10240
)

// historyWaitBudget is separate from the QR session timeout because the
// widened full sync can outlast the code.
func historyWaitBudget(full bool) time.Duration {
	if full {
		return historyFullWait
	}
	return historyPlainWait
}

// configureHistorySync overrides only the fields whose default is narrower
// than what the import wants, so a whatsmeow default for the other capability
// flags is inherited instead of copied here.
func configureHistorySync(enabled bool) {
	store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	store.DeviceProps.RequireFullSync = proto.Bool(enabled)
	if !enabled {
		return
	}
	config := store.DeviceProps.GetHistorySyncConfig()
	if config == nil {
		config = &waCompanionReg.DeviceProps_HistorySyncConfig{}
		store.DeviceProps.HistorySyncConfig = config
	}
	config.FullSyncDaysLimit = proto.Uint32(fullSyncDays)
	config.FullSyncSizeMbLimit = proto.Uint32(fullSyncSize)
	config.StorageQuotaMb = proto.Uint32(storageQuota)
}

// attachHistory imports the history sync WhatsApp delivers right after
// linking: whichever connection is alive captures it, this pairing session
// before it exits and the fleet connection the rest (ADR 0035). Both results
// are nil when history is disabled.
func attachHistory(
	ctx context.Context, client *whatsmeow.Client, opts PairOptions,
) (*historyWaiter, *ingest.HistoryImporter) {
	if !opts.History {
		return nil, nil
	}
	waiter := newHistoryWaiter(opts.FullHistory)
	lookup := func(lid types.JID) (types.JID, bool) {
		if client.Store == nil || client.Store.LIDs == nil {
			return types.JID{}, false
		}
		pn, err := client.Store.LIDs.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			return types.JID{}, false
		}
		return pn, true
	}
	importer := ingest.NewHistory(opts.Scope, opts.Bot, opts.Contacts, opts.Chats,
		opts.Messages, lookup, opts.Log)
	if importer == nil {
		return nil, nil
	}
	importer.SetOwnPhone(DevicePhone(client.Store))
	client.AddEventHandler(func(raw any) {
		event, ok := raw.(*events.HistorySync)
		if !ok {
			return
		}
		importer.Handle(ctx, event)
		if event.Data != nil {
			waiter.progress(event.Data.GetSyncType(), event.Data.GetProgress())
		}
	})
	return waiter, importer
}

// attachContacts copies the account's address book while the pairing session
// is alive; the caller reads the cache afterwards for whatever the events
// missed (ADR 0037).
func attachContacts(
	ctx context.Context, client *whatsmeow.Client, opts PairOptions,
) *ingest.AddressBookImporter {
	if !opts.ImportContacts {
		return nil
	}
	lookup := func(lid types.JID) (types.JID, bool) {
		if client.Store == nil || client.Store.LIDs == nil {
			return types.JID{}, false
		}
		pn, err := client.Store.LIDs.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			return types.JID{}, false
		}
		return pn, true
	}
	importer := ingest.NewAddressBook(opts.Scope, opts.Bot, opts.AddressBook, lookup, opts.Log)
	if importer == nil {
		return nil
	}
	// A full address-book sync is otherwise applied silently, which would skip
	// every contact event this import runs on.
	client.EmitAppStateEventsOnFullSync = true
	client.AddEventHandler(func(raw any) { importer.Handle(ctx, raw) })
	return importer
}
