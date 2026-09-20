package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	walog "go.mau.fi/whatsmeow/util/log"

	"github.com/felixzsh/botflota"
)

// SyncOptions configures one on-demand history request (ADR 0035). It opens
// the account's device directly, so the fleet must be stopped while it runs.
type SyncOptions struct {
	Scope    string
	Bot      string
	Devices  *Devices
	Accounts botflota.LinkedAccountStore
	Senders  botflota.SenderStore
	Chats    botflota.ChatStore
	Messages botflota.MessageStore
	Phone    string
	Count    int
	Timeout  time.Duration
	Logger   walog.Logger
}

// SyncResult reports what one on-demand response imported.
type SyncResult struct {
	Imported  int
	Duplicate int
	Skipped   int
}

// SyncHistory asks the primary device for messages older than the chat's
// oldest known message and imports the response (ADR 0035). It is a bounded
// experiment: WhatsApp may or may not answer, so it waits up to Timeout for an
// ON_DEMAND sync and reports an error when none arrives.
func SyncHistory(ctx context.Context, opts SyncOptions) (SyncResult, error) {
	if opts.Devices == nil {
		return SyncResult{}, errors.New("whatsapp: no device store")
	}
	count := opts.Count
	if count <= 0 {
		count = 50
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	account, err := opts.Accounts.Get(ctx, opts.Scope, opts.Bot)
	if err != nil {
		return SyncResult{}, fmt.Errorf(
			"whatsapp: bot %q is not paired (use bot pair): %w", opts.Bot, err)
	}
	device, err := opts.Devices.device(ctx, account)
	if err != nil {
		return SyncResult{}, err
	}
	if device == nil {
		return SyncResult{}, missingDeviceError(account)
	}
	anchor, err := syncAnchor(ctx, opts)
	if err != nil {
		return SyncResult{}, err
	}

	logger := opts.Logger
	if logger == nil {
		logger = walog.Stdout("whatsmeow/"+opts.Bot, "error", false)
	}
	client := whatsmeow.NewClient(device, logger)
	defer client.Disconnect()
	importer := newHistoryImporter(opts.Scope, opts.Bot, opts.Senders, opts.Chats,
		opts.Messages, syncLookup(ctx, client), nil)
	if importer == nil {
		return SyncResult{}, errors.New(
			"whatsapp: on-demand history needs sender, chat, and message stores")
	}

	responses := make(chan *events.HistorySync, 4)
	client.AddEventHandler(func(raw any) {
		event, ok := raw.(*events.HistorySync)
		if !ok || event.Data.GetSyncType() != waHistorySync.HistorySync_ON_DEMAND {
			return
		}
		select {
		case responses <- event:
		default:
		}
	})
	if err := client.Connect(); err != nil {
		return SyncResult{}, fmt.Errorf("whatsapp: connect: %w", err)
	}
	if !client.WaitForConnection(timeout) {
		return SyncResult{}, errors.New("whatsapp: could not connect to request history")
	}
	request := client.BuildHistorySyncRequest(anchor, count)
	if _, err := client.SendPeerMessage(ctx, request); err != nil {
		return SyncResult{}, fmt.Errorf("whatsapp: send history request: %w", err)
	}

	select {
	case event := <-responses:
		stats := importer.Handle(ctx, event)
		return SyncResult{
			Imported:  stats.imported,
			Duplicate: stats.duplicate,
			Skipped:   stats.skipped,
		}, nil
	case <-time.After(timeout):
		return SyncResult{}, errors.New(
			"whatsapp: no on-demand history arrived; the primary device did not answer")
	case <-ctx.Done():
		return SyncResult{}, ctx.Err()
	}
}

// syncAnchor loads the oldest message of the sender's chat, the point the
// primary device pages back from.
func syncAnchor(ctx context.Context, opts SyncOptions) (*types.MessageInfo, error) {
	sender, err := opts.Senders.Get(ctx, opts.Scope, opts.Phone)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: no sender %s in scope %q: %w", opts.Phone, opts.Scope, err)
	}
	chat, err := opts.Chats.ForSender(ctx, opts.Scope, opts.Bot, sender.ID)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: no chat with %s for bot %q: %w", opts.Phone, opts.Bot, err)
	}
	oldest, err := opts.Messages.Oldest(ctx, chat.ID)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: no stored message to page back from: %w", err)
	}
	if oldest.At.IsZero() {
		return nil, errors.New("whatsapp: the oldest stored message has no timestamp")
	}
	chatJID := types.NewJID(opts.Phone, types.DefaultUserServer)
	return &types.MessageInfo{
		ID: types.MessageID(oldest.ProviderMessageID),
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			Sender:   chatJID,
			IsFromMe: oldest.Direction == botflota.Outbound,
		},
		Timestamp: oldest.At,
	}, nil
}

// syncLookup resolves a LID through the client's LID store (ADR 0020).
func syncLookup(ctx context.Context, client *whatsmeow.Client) pnLookup {
	return func(lid types.JID) (types.JID, bool) {
		if client.Store == nil || client.Store.LIDs == nil {
			return types.JID{}, false
		}
		pn, err := client.Store.LIDs.GetPNForLID(ctx, lid)
		if err != nil || pn.IsEmpty() {
			return types.JID{}, false
		}
		return pn, true
	}
}
