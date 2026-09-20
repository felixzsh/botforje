package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota"
)

// historyImporter stores the messages of an account-wide WhatsApp history
// sync. It is inert: it never runs the engine or the outbox, and every row it
// writes says sync/unknown, because the past cannot say who wrote an outbound
// message outside Botflota (ADR 0026, ADR 0035).
//
// WhatsApp delivers the sync in chunks right after a companion links, and the
// chunks carry conversations for many senders at once, not for one. The
// importer resolves the chat's phone number, creates the sender and chat, and
// appends the text messages and call attempts; a repeated chunk is
// deduplicated through the channel message id. A conversation with any message
// registers the chat even when every entry is a type the importer does not
// store. A PUSH_NAME chunk carries the account's contact names, which the
// importer applies to the senders it already has.
type historyImporter struct {
	scope     string
	bot       string
	senders   botflota.SenderStore
	chats     botflota.ChatStore
	messages  botflota.MessageStore
	lookup    pnLookup
	log       *slog.Logger
	now       func() time.Time
	pushNames map[string]string
}

// newHistoryImporter builds an importer. It returns nil when a store is
// missing, so a caller can treat the import as disabled.
func newHistoryImporter(
	scope, bot string,
	senders botflota.SenderStore, chats botflota.ChatStore,
	messages botflota.MessageStore, lookup pnLookup, log *slog.Logger,
) *historyImporter {
	if senders == nil || chats == nil || messages == nil {
		return nil
	}
	if log == nil {
		log = botflota.DiscardLogger()
	}
	return &historyImporter{
		scope: scope, bot: bot, senders: senders, chats: chats,
		messages: messages, lookup: lookup, log: log, now: time.Now,
		pushNames: map[string]string{},
	}
}

// historyStats summarizes what one sync chunk produced.
type historyStats struct {
	conversations int
	imported      int
	duplicate     int
	skipped       int
	pushNames     int
}

// Handle imports one history-sync chunk, logs its progress, and reports what
// it stored. Conversation and message errors are logged and skipped; one bad
// row never aborts a sync.
func (h *historyImporter) Handle(ctx context.Context, event *events.HistorySync) historyStats {
	if event == nil || event.Data == nil {
		return historyStats{}
	}
	data := event.Data
	stats := historyStats{}
	// Names first, so a conversation in the same chunk can use them.
	h.importPushNames(ctx, data.GetPushnames(), &stats)
	for _, conversation := range data.GetConversations() {
		stats.conversations++
		got, dup, skip, err := h.importConversation(ctx, conversation)
		if err != nil {
			h.log.Warn("import history conversation", "error", err)
		}
		stats.imported += got
		stats.duplicate += dup
		stats.skipped += skip
	}
	h.log.Info("history sync imported",
		"sync_type", data.GetSyncType().String(),
		"chunk", data.GetChunkOrder(),
		"progress", data.GetProgress(),
		"conversations", stats.conversations,
		"imported", stats.imported,
		"duplicate", stats.duplicate,
		"skipped", stats.skipped,
		"push_names", stats.pushNames)
	return stats
}

// importPushNames applies the contact names WhatsApp sends in its own chunk.
// It only names senders Botflota already knows; a contact that never wrote is
// remembered for a sender created by a later chunk, not created here.
func (h *historyImporter) importPushNames(
	ctx context.Context, names []*waHistorySync.Pushname, stats *historyStats,
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
		stats.pushNames++
		if err := h.senders.SetDisplayName(ctx, h.scope, phone, name, at); err != nil {
			h.log.Warn("import history push name", "error", err)
		}
	}
}

// importConversation stores the text messages and call attempts of one
// conversation, oldest first, and widens the chat's activity to cover them. A
// conversation with any message registers the sender and chat even when every
// entry is a type the importer does not store, so a chat that only ever saw a
// call or media still appears in the read API (ADR 0035). It returns how many
// messages were imported, how many were duplicates, and how many entries were
// skipped.
func (h *historyImporter) importConversation(
	ctx context.Context, conversation *waHistorySync.Conversation,
) (int, int, int, error) {
	phone, ok := historyChatPhone(conversation, h.lookup)
	if !ok {
		// Groups, broadcasts, status, and unresolved hidden-user chats.
		return 0, 0, 1, nil
	}
	ordered := append([]*waHistorySync.HistorySyncMsg(nil), conversation.GetMessages()...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].GetMsgOrderID() < ordered[j].GetMsgOrderID()
	})
	records := make([]botflota.Message, 0, len(ordered))
	name := conversation.GetName()
	skipped := 0
	var from, to time.Time
	for _, entry := range ordered {
		if at := historyEntryAt(conversation, entry); !at.IsZero() {
			if from.IsZero() || at.Before(from) {
				from = at
			}
			if at.After(to) {
				to = at
			}
		}
		record, pushName, ok := historyRecord(conversation, entry)
		if !ok {
			skipped++
			continue
		}
		if record.Direction == botflota.Inbound && pushName != "" {
			name = pushName
		}
		records = append(records, record)
	}
	if len(ordered) == 0 {
		return 0, 0, skipped, nil
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
	sender, _, err := h.senders.Upsert(ctx, h.scope, phone, name, ensureAt)
	if err != nil {
		return 0, 0, skipped, fmt.Errorf("sender %s: %w", phone, err)
	}
	chat, err := h.chats.Ensure(ctx, h.scope, h.bot, sender.ID, ensureAt)
	if err != nil {
		return 0, 0, skipped, fmt.Errorf("chat %s: %w", phone, err)
	}
	if err := h.chats.NoteHistory(ctx, chat.ID, from, to); err != nil {
		return 0, 0, skipped, fmt.Errorf("chat activity %s: %w", phone, err)
	}
	imported, duplicate := 0, 0
	for i := range records {
		records[i].ChatID = chat.ID
		if err := h.messages.Append(ctx, &records[i]); err != nil {
			if errors.Is(err, botflota.ErrDuplicate) {
				duplicate++
				continue
			}
			return imported, duplicate, skipped, fmt.Errorf("append %s: %w", phone, err)
		}
		imported++
	}
	return imported, duplicate, skipped, nil
}

// historyRecord maps one synced message to a stored message. Every content
// kind is kept, so the chat has an anchor for `chat sync` even when the entry
// is media with no text; a control or internal entry, or one without a channel
// id, reports false, because a row with no provider id cannot anchor an
// on-demand request (ADR 0035, ADR 0036).
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
	content, ok := contentType(unwrapMessage(web.GetMessage()))
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
		At:                historyEntryAt(conversation, entry),
		ProviderMessageID: providerID,
		Ingest:            botflota.IngestSync,
		Origin:            origin,
		Routing:           botflota.RoutingUnknown,
	}, web.GetPushName(), true
}

// historyEntryAt is the timestamp of one synced entry: the message's own time,
// or the conversation's last timestamp when the message carries none.
func historyEntryAt(
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

// historyChatPhone resolves the phone number of a conversation's chat. It uses
// the conversation's phone JID, the chat id when it is already a phone number,
// or the LID store for a hidden-user chat (ADR 0020). A group, broadcast, or
// unresolved chat reports false, and the import skips it.
func historyChatPhone(
	conversation *waHistorySync.Conversation, lookup pnLookup,
) (string, bool) {
	if pn := conversation.GetPnJID(); pn != "" {
		if jid, err := types.ParseJID(pn); err == nil &&
			jid.Server == types.DefaultUserServer {
			return jid.User, true
		}
	}
	id := conversation.GetID()
	if id == "" {
		return "", false
	}
	jid, err := types.ParseJID(id)
	if err != nil {
		return "", false
	}
	if jid.Server == types.DefaultUserServer {
		return jid.User, true
	}
	if jid.Server == types.HiddenUserServer && lookup != nil {
		if pn, ok := lookup(jid); ok && !pn.IsEmpty() {
			return pn.User, true
		}
	}
	return "", false
}

// historyPushNamePhone resolves the phone number of one PUSH_NAME entry, which
// may address the contact by phone number or by hidden user id.
func historyPushNamePhone(entry *waHistorySync.Pushname, lookup pnLookup) (string, bool) {
	id := entry.GetID()
	if id == "" {
		return "", false
	}
	jid, err := types.ParseJID(id)
	if err != nil {
		return "", false
	}
	if jid.Server == types.DefaultUserServer {
		return jid.User, true
	}
	if jid.Server == types.HiddenUserServer && lookup != nil {
		if pn, ok := lookup(jid); ok && !pn.IsEmpty() {
			return pn.User, true
		}
	}
	return "", false
}
