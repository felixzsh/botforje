package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

const historyBatchSize = 50

// historyCursor is the provider point a backward page starts from: the key of
// a message that already exists in the chat.
type historyCursor struct {
	ID     string
	FromMe bool
	At     time.Time
}

// historyPage is one completed on-demand request. Received counts only the
// requested chat; Next is the oldest message the page returned, and nil means
// the page carried nothing older, so paging stops.
type historyPage struct {
	Received  int
	Imported  int
	Duplicate int
	Skipped   int
	Exhausted bool
	Next      *historyCursor
}

type cursorLoader func() (*historyCursor, error)

type historyRequester func(
	context.Context, *historyCursor, int, time.Duration,
) (historyPage, error)

// backfillChat pages backwards from the chat's oldest known message until
// the phone runs out of older entries or reports no access. It is independent
// of the WhatsApp connection, so its stop conditions are testable.
func backfillChat(
	ctx context.Context, limit int, timeout time.Duration,
	load cursorLoader, request historyRequester,
) (botflota.HistoryResult, error) {
	result := botflota.HistoryResult{}
	cursor, err := load()
	if err != nil {
		return result, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if limit > 0 && result.Received >= limit {
			return result, nil
		}
		batch := historyBatchSize
		if limit > 0 && limit-result.Received < batch {
			batch = limit - result.Received
		}
		page, err := request(ctx, cursor, batch, timeout)
		result.Rounds++
		result.Received += page.Received
		result.Imported += page.Imported
		result.Duplicate += page.Duplicate
		result.Skipped += page.Skipped
		if err != nil {
			return result, err
		}
		if page.Exhausted {
			result.Exhausted = true
			return result, nil
		}
		if page.Next == nil {
			result.Stable = true
			return result, nil
		}
		cursor = page.Next
	}
}

// oldestCursor finds the anchor an on-demand request pages back from (ADR
// 0035). A control entry (a reaction or an edit) and an account-level call
// record are not valid anchors, so they are never chosen.
func oldestCursor(
	ctx context.Context, scope, bot, phone string,
	contacts botflota.ContactStore, chats botflota.ChatStore,
	messages botflota.MessageStore,
) (*historyCursor, error) {
	contact, err := contacts.Get(ctx, scope, phone)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: no contact %s in scope %q: %w", phone, scope, err)
	}
	chat, err := chats.ForContact(ctx, scope, bot, contact.ID)
	if err != nil {
		return nil, fmt.Errorf(
			"whatsapp: no chat with %s for bot %q: %w", phone, bot, err)
	}
	message, err := messages.Oldest(ctx, chat.ID)
	if errors.Is(err, botflota.ErrNotFound) {
		return nil, fmt.Errorf(
			"whatsapp: chat with %s has no message to page from yet; "+
				"only calls, control entries, or nothing arrived for it", phone)
	}
	if err != nil {
		return nil, fmt.Errorf("whatsapp: read oldest message: %w", err)
	}
	if message.ProviderMessageID == "" || message.At.IsZero() {
		return nil, fmt.Errorf(
			"whatsapp: oldest message of %s carries no provider cursor", phone)
	}
	return &historyCursor{
		ID:     message.ProviderMessageID,
		FromMe: message.Direction == botflota.Outbound,
		At:     message.At,
	}, nil
}

// SyncHistory asks the primary device for older messages of one chat (ADR
// 0035). Count is the total to request; zero means every entry the phone
// makes available. One sync runs per bot at a time.
func (a *Adapter) SyncHistory(
	ctx context.Context, phone string, count int, timeout time.Duration,
) (botflota.HistoryResult, error) {
	if a.importer == nil {
		return botflota.HistoryResult{}, errors.New(
			"whatsapp: history sync is disabled for this bot (settings.history_sync)")
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if err := a.beginSync(); err != nil {
		return botflota.HistoryResult{}, err
	}
	defer a.endSync()

	load := func() (*historyCursor, error) {
		return oldestCursor(ctx, a.scope, a.bot, phone, a.contacts, a.chats, a.messages)
	}
	request := func(
		requestCtx context.Context, from *historyCursor, batch int,
		requestTimeout time.Duration,
	) (historyPage, error) {
		return a.requestHistory(requestCtx, phone, from, batch, requestTimeout)
	}
	return backfillChat(ctx, count, timeout, load, request)
}

func (a *Adapter) beginSync() error {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.syncing {
		return errors.New("whatsapp: a history sync is already running for this bot")
	}
	a.syncing = true
	return nil
}

func (a *Adapter) endSync() {
	a.syncMu.Lock()
	a.syncing = false
	a.pending = nil
	a.syncMu.Unlock()
}

// requestHistory sends one page and waits for every chunk belonging to its
// request id. Chunks for other ids are still imported, but cannot answer this
// round.
func (a *Adapter) requestHistory(
	ctx context.Context, phone string, from *historyCursor,
	count int, timeout time.Duration,
) (historyPage, error) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || !client.IsConnected() {
		return historyPage{}, errors.New("whatsapp: not connected")
	}
	anchor := historyAnchorInfo(ctx, phone, from, client.Store.LIDs)
	if client.Store.ID == nil {
		return historyPage{}, errors.New("whatsapp: not logged in")
	}
	// The id is chosen before sending so the correlator is installed before the
	// phone can answer, and it is the request's session id in the reply.
	requestID := string(client.GenerateMessageID())
	pending := newSyncRequest(requestID, phone, a.lookupPN)
	a.setPending(pending)
	defer a.clearPending(pending)
	a.log.Info("requesting history",
		"phone", phone, "count", count, "anchor", from.ID,
		"from_me", from.FromMe, "at", from.At.Format(time.RFC3339),
		"request", requestID)
	_, err := client.SendMessage(ctx, client.Store.ID.ToNonAD(),
		client.BuildHistorySyncRequest(anchor, count),
		whatsmeow.SendRequestExtra{ID: types.MessageID(requestID), Peer: true})
	if err != nil {
		return historyPage{}, fmt.Errorf("whatsapp: send history request: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-pending.done:
		page := pending.snapshot()
		a.log.Info("history page answered",
			"request", requestID, "received", page.Received,
			"imported", page.Imported, "exhausted", page.Exhausted)
		return page, nil
	case <-timer.C:
		return pending.snapshot(), fmt.Errorf(
			"whatsapp: the phone did not answer for anchor %s in %s",
			from.ID, timeout)
	case <-ctx.Done():
		return pending.snapshot(), ctx.Err()
	}
}

func (a *Adapter) setPending(request *syncRequest) {
	a.syncMu.Lock()
	a.pending = request
	a.syncMu.Unlock()
}

func (a *Adapter) clearPending(request *syncRequest) {
	a.syncMu.Lock()
	if a.pending == request {
		a.pending = nil
	}
	a.syncMu.Unlock()
}

// observeSync feeds one imported chunk to the sync waiting for it, if any.
// The import worker is the only caller, so the importer stays single-threaded.
func (a *Adapter) observeSync(event *events.HistorySync, stats ingest.ImportTotals) {
	a.syncMu.Lock()
	pending := a.pending
	a.syncMu.Unlock()
	if pending == nil || historySyncRequestID(event) != pending.id {
		return
	}
	a.logOnDemandChunk(event)
	pending.observe(event, stats)
}

// logOnDemandChunk is how an empty answer is told apart from a conversation
// that never came back.
func (a *Adapter) logOnDemandChunk(event *events.HistorySync) {
	if event.Data == nil || !a.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	for _, conversation := range event.Data.GetConversations() {
		phone, _ := ingest.HistoryChatPhone(conversation, a.lookupPN)
		a.log.Debug("on-demand conversation",
			"chat", conversation.GetID(),
			"phone", phone,
			"messages", len(conversation.GetMessages()),
			"end_of_history", conversation.GetEndOfHistoryTransferType().String())
	}
}

// syncRequest correlates one on-demand response with the request that asked
// for it.
type syncRequest struct {
	id     string
	phone  string
	lookup ingest.PNLookup

	mu        sync.Mutex
	page      historyPage
	completed bool
	done      chan struct{}
}

func newSyncRequest(id, phone string, lookup ingest.PNLookup) *syncRequest {
	return &syncRequest{
		id: id, phone: phone, lookup: lookup, done: make(chan struct{}),
	}
}

// observe accumulates one matching chunk and closes done exactly once, when
// the response is complete.
func (r *syncRequest) observe(event *events.HistorySync, stats ingest.ImportTotals) {
	if historySyncRequestID(event) != r.id {
		return
	}
	received, next := historyEventPage(event, r.phone, r.lookup)
	complete := historySyncCompleteFor(event, r.phone, r.lookup)
	exhausted := complete && historySyncExhausted(event, r.phone, r.lookup)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.page.Received += received
	r.page.Imported += stats.Imported
	r.page.Duplicate += stats.Duplicate
	r.page.Skipped += stats.Skipped
	if next != nil && (r.page.Next == nil || next.At.Before(r.page.Next.At)) {
		r.page.Next = next
	}
	if complete && !r.completed {
		r.page.Exhausted = exhausted
		r.completed = true
		close(r.done)
	}
}

func (r *syncRequest) snapshot() historyPage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.page
}

// historyAnchorInfo addresses the anchor to the chat's LID when the device
// store knows the mapping (ADR 0020).
func historyAnchorInfo(
	ctx context.Context, phone string, from *historyCursor,
	lids store.LIDStore,
) *types.MessageInfo {
	chatJID := types.NewJID(phone, types.DefaultUserServer)
	if lids != nil {
		if lid, err := lids.GetLIDForPN(ctx, chatJID); err == nil && !lid.IsEmpty() {
			chatJID = lid
		}
	}
	return &types.MessageInfo{
		ID: types.MessageID(from.ID),
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			Sender:   chatJID,
			IsFromMe: from.FromMe,
		},
		Timestamp: from.At,
	}
}
