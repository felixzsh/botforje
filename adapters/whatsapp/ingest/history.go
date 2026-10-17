package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
)

// HistoryImporter stores the messages of an account-wide WhatsApp history
// sync. It is inert: it never runs the engine or the outbox, and every row it
// writes says sync/unknown, because the past cannot say who wrote an outbound
// message outside Botflota (ADR 0026, ADR 0035).
type HistoryImporter struct {
	scope     string
	bot       string
	peers     botflota.PeerStore
	chats     botflota.ChatStore
	messages  botflota.MessageStore
	lookup    PNLookup
	log       *slog.Logger
	now       func() time.Time
	pushNames map[string]string
	ownPhone  string
	// mu guards the counters and the name cache: the pairing session imports
	// from the whatsmeow event handler while the command reads the totals.
	mu             sync.Mutex
	totals         ImportTotals
	transfer       ImportTotals
	transferType   string
	transferClosed bool
}

// NewHistory returns nil when a store is missing, so a caller can
// treat the import as disabled.
func NewHistory(
	scope, bot string,
	peers botflota.PeerStore, chats botflota.ChatStore,
	messages botflota.MessageStore, lookup PNLookup, log *slog.Logger,
) *HistoryImporter {
	if peers == nil || chats == nil || messages == nil {
		return nil
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return &HistoryImporter{
		scope: scope, bot: bot, peers: peers, chats: chats,
		messages: messages, lookup: lookup, log: log, now: time.Now,
		pushNames: map[string]string{},
	}
}

// ImportTotals is what a history import produced: Handle returns the totals of
// one chunk, Totals the running totals of the session (ADR 0038).
type ImportTotals struct {
	Chunks        int
	Conversations int
	Imported      int
	Duplicate     int
	Skipped       int
	Received      int
	PushNames     int
}

// Handle imports one history-sync chunk and reports what it stored.
// Conversation and message errors are logged and skipped; one bad row never
// aborts a sync.
func (h *HistoryImporter) Handle(
	ctx context.Context, event *events.HistorySync,
) ImportTotals {
	if event == nil || event.Data == nil {
		return ImportTotals{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	data := event.Data
	stats := ImportTotals{}
	// Names first, so a conversation in the same chunk can use them.
	h.importPushNames(ctx, data.GetPushnames(), &stats)
	h.importCallLogs(ctx, data.GetCallLogRecords(), &stats)
	for _, conversation := range data.GetConversations() {
		stats.Conversations++
		got, dup, skip, received, err := h.importConversation(ctx, conversation)
		if err != nil {
			h.log.Warn("import history conversation", "error", err)
		}
		stats.Imported += got
		stats.Duplicate += dup
		stats.Skipped += skip
		stats.Received += received
		// The per-chunk log is how a chat the phone omitted is told from one
		// it delivered empty.
		if h.log.Enabled(ctx, slog.LevelDebug) {
			phone, _ := HistoryChatPhone(conversation, h.lookup)
			h.log.Debug("history conversation",
				"chat", conversation.GetID(),
				"phone", phone,
				"messages", len(conversation.GetMessages()),
				"imported", got,
				"skipped", skip,
				"received", received,
				"end_of_history", conversation.GetEndOfHistoryTransferType().String())
		}
	}
	stats.Chunks = 1
	h.record(data, stats)
	return stats
}

// record folds one chunk into the running totals and closes a transfer that
// just ended. A full sync arrives in hundreds of chunks, so the per-chunk line
// is debug and only the per-transfer summary is info (ADR 0038).
func (h *HistoryImporter) record(data *waHistorySync.HistorySync, stats ImportTotals) {
	syncType := data.GetSyncType().String()
	if syncType != h.transferType {
		h.summarize()
		h.transferType = syncType
		h.transferClosed = false
	}
	h.log.Debug("history chunk imported",
		"sync_type", syncType,
		"chunk", data.GetChunkOrder(),
		"progress", data.GetProgress(),
		"conversations", stats.Conversations,
		"imported", stats.Imported,
		"duplicate", stats.Duplicate,
		"skipped", stats.Skipped,
		"received", stats.Received,
		"push_names", stats.PushNames)
	h.transfer = addTotals(h.transfer, stats)
	h.totals = addTotals(h.totals, stats)
	// Progress 100 ends the transfer, and the chunks that follow it belong to
	// the next type. A second chunk still at 100 is not a new transfer.
	if data.GetProgress() >= 100 && !h.transferClosed {
		h.summarize()
		h.transferClosed = true
	}
}

func (h *HistoryImporter) summarize() {
	if h.transfer.Chunks == 0 {
		return
	}
	h.log.Info("history sync imported",
		"sync_type", h.transferType,
		"chunks", h.transfer.Chunks,
		"conversations", h.transfer.Conversations,
		"imported", h.transfer.Imported,
		"duplicate", h.transfer.Duplicate,
		"skipped", h.transfer.Skipped,
		"received", h.transfer.Received,
		"push_names", h.transfer.PushNames)
	h.transfer = ImportTotals{}
}

func (h *HistoryImporter) Totals() ImportTotals {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.totals
}

// SetOwnPhone tells the importer the paired account's own number, which is
// known only once the device is loaded. The number doubles as the
// adapter-declared message space of every row this import writes: history
// keeps the provenance of the account that recorded it (ADR 0026, ADR 0051).
func (h *HistoryImporter) SetOwnPhone(phone string) { h.ownPhone = phone }

func addTotals(a, b ImportTotals) ImportTotals {
	a.Chunks += b.Chunks
	a.Conversations += b.Conversations
	a.Imported += b.Imported
	a.Duplicate += b.Duplicate
	a.Skipped += b.Skipped
	a.Received += b.Received
	a.PushNames += b.PushNames
	return a
}

// importPushNames applies the contact names WhatsApp sends in its own chunk.
// It only names identities Botflota already knows; a name for a contact that
// never wrote is remembered for an identity created by a later chunk.
func (h *HistoryImporter) importPushNames(
	ctx context.Context, names []*waHistorySync.Pushname, stats *ImportTotals,
) {
	at := h.now().UTC()
	for _, entry := range names {
		name := entry.GetPushname()
		// "-" is WhatsApp's placeholder for a removed name.
		if name == "" || name == "-" {
			continue
		}
		phone, ok := historyPushNamePhone(entry, h.lookup)
		if !ok {
			continue
		}
		h.pushNames[phone] = name
		stats.PushNames++
		if err := h.peers.SetDisplayName(ctx, h.scope, PeerKey(phone), name, at); err != nil {
			h.log.Warn("import history push name", "error", err)
		}
	}
}

// importConversation stores the content messages and call attempts of one
// conversation, oldest first, and widens the chat's activity to cover them. It
// registers its contact and chat only when it stored at least one message or
// call: WhatsApp sends a control-only shell for every number the account's
// address book knows (ADR 0035).
func (h *HistoryImporter) importConversation(
	ctx context.Context, conversation *waHistorySync.Conversation,
) (int, int, int, int, error) {
	phone, ok := HistoryChatPhone(conversation, h.lookup)
	if !ok {
		// Groups, broadcasts, status, and unresolved hidden-user chats.
		return 0, 0, 1, 0, nil
	}
	ordered := append([]*waHistorySync.HistorySyncMsg(nil), conversation.GetMessages()...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].GetMsgOrderID() < ordered[j].GetMsgOrderID()
	})
	records := make([]botflota.Message, 0, len(ordered))
	name := conversation.GetName()
	skipped := 0
	received := 0
	var from, to time.Time
	for _, entry := range ordered {
		if at := HistoryEntryAt(conversation, entry); !at.IsZero() {
			if from.IsZero() || at.Before(from) {
				from = at
			}
			if at.After(to) {
				to = at
			}
		}
		if entry.GetMessage().GetKey().GetID() == "" {
			skipped++
			continue
		}
		received++
		record, pushName, ok := historyRecord(conversation, entry)
		if !ok {
			skipped++
			continue
		}
		record.MessageSpace = h.ownPhone
		if record.Direction == botflota.Inbound && pushName != "" {
			name = pushName
		}
		records = append(records, record)
	}
	// A conversation whose entries are all control traffic registers nothing:
	// no contact, no chat, no row.
	if len(records) == 0 {
		return 0, 0, skipped, received, nil
	}
	if name == "" {
		name = h.pushNames[phone]
	}
	ensureAt := to
	if ensureAt.IsZero() {
		ensureAt = from
	}
	if ensureAt.IsZero() {
		ensureAt = h.now().UTC()
	}
	key := PeerKey(phone)
	target := DirectTarget(phone)
	peer, err := h.peers.EnsurePeer(ctx, h.scope, key, ensureAt)
	if err != nil {
		return 0, 0, skipped, received, fmt.Errorf("peer %s: %w", phone, err)
	}
	if name != "" {
		if err := h.peers.SetDisplayName(ctx, h.scope, key, name, ensureAt); err != nil {
			return 0, 0, skipped, received, fmt.Errorf("peer name %s: %w", phone, err)
		}
	}
	chat, err := h.chats.Ensure(ctx, h.scope, h.bot, target, peer.ID, ensureAt)
	if err != nil {
		return 0, 0, skipped, received, fmt.Errorf("chat %s: %w", phone, err)
	}
	if err := h.chats.NoteHistory(ctx, chat.ID, from, to); err != nil {
		return 0, 0, skipped, received, fmt.Errorf("chat activity %s: %w", phone, err)
	}
	imported, duplicate := 0, 0
	for i := range records {
		records[i].ChatID = chat.ID
		records[i].Target = target
		if err := h.messages.Append(ctx, &records[i]); err != nil {
			if errors.Is(err, botflota.ErrDuplicate) {
				duplicate++
				continue
			}
			return imported, duplicate, skipped, received,
				fmt.Errorf("append %s: %w", phone, err)
		}
		imported++
	}
	return imported, duplicate, skipped, received, nil
}

// historyRecord keeps every content kind, so a chat whose history is all media
// still gets a row; a control or internal entry, or one without a channel id,
// reports false and is skipped (ADR 0035, ADR 0036).
func historyRecord(
	conversation *waHistorySync.Conversation, entry *waHistorySync.HistorySyncMsg,
) (botflota.Message, string, bool) {
	web := entry.GetMessage()
	if web == nil || web.GetKey() == nil {
		return botflota.Message{}, "", false
	}
	providerID := web.GetKey().GetID()
	if providerID == "" {
		return botflota.Message{}, "", false
	}
	content, ok := Decode(unwrapMessage(web.GetMessage()))
	if !ok {
		return botflota.Message{}, "", false
	}
	direction := botflota.Inbound
	origin := botflota.MessageOrigin("")
	if web.GetKey().GetFromMe() {
		direction = botflota.Outbound
		origin = botflota.OriginUnknown
	}
	return botflota.Message{
		Direction:         direction,
		Content:           content,
		At:                HistoryEntryAt(conversation, entry),
		ProviderMessageID: providerID,
		Ingest:            botflota.IngestSync,
		Origin:            origin,
		Routing:           botflota.RoutingUnknown,
	}, web.GetPushName(), true
}

// HistoryEntryAt falls back to the conversation's last timestamp when a
// message carries none of its own.
func HistoryEntryAt(
	conversation *waHistorySync.Conversation, entry *waHistorySync.HistorySyncMsg,
) time.Time {
	web := entry.GetMessage()
	if web == nil {
		return time.Time{}
	}
	if web.GetMessageTimestamp() != 0 {
		return time.Unix(int64(web.GetMessageTimestamp()), 0).UTC()
	}
	if conversation.GetLastMsgTimestamp() != 0 {
		return time.Unix(int64(conversation.GetLastMsgTimestamp()), 0).UTC()
	}
	return time.Time{}
}
