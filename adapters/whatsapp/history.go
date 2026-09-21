package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
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
// appends the content messages and call attempts; a repeated chunk is
// deduplicated through the channel message id. A conversation registers its
// sender and chat only when it stored at least one message or call. A PUSH_NAME
// chunk carries the account's contact names, which the importer applies to the
// senders it already has.
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
	ownPhone  string
	// mu guards the counters and the name cache: the pairing session imports
	// from the whatsmeow event handler while the command reads the totals.
	mu             sync.Mutex
	totals         ImportTotals
	transfer       ImportTotals
	transferType   string
	transferClosed bool
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

// ImportTotals summarizes what a history importer produced. Handle returns the
// totals of one chunk; the importer keeps the running totals of the session,
// which `bot pair` prints as the result of a registration that carried history
// (ADR 0038).
type ImportTotals struct {
	Chunks        int
	Conversations int
	Imported      int
	Duplicate     int
	Skipped       int
	Received      int
	PushNames     int
}

// Handle imports one history-sync chunk, records its progress, and reports what
// it stored. Conversation and message errors are logged and skipped; one bad
// row never aborts a sync.
func (h *historyImporter) Handle(
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
		// Which chat arrived, with how many entries and how many survived, is
		// how a chat the phone omitted is told from one it delivered empty.
		if h.log.Enabled(ctx, slog.LevelDebug) {
			phone, _ := historyChatPhone(conversation, h.lookup)
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
func (h *historyImporter) record(data *waHistorySync.HistorySync, stats ImportTotals) {
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

// summarize logs one line for the transfer that just ended and starts a new
// one. A transfer with no chunk is nothing to report.
func (h *historyImporter) summarize() {
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

// Totals reports everything the importer has stored. The pairing command prints
// it instead of the per-chunk stream (ADR 0038).
func (h *historyImporter) Totals() ImportTotals {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.totals
}

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

// importCallLogs keeps call records that WhatsApp delivers outside the normal
// conversation message list, so a chat whose only trace is a call still shows
// one. The call id is not a key in the chat's message list, so it never anchors
// a history request.
func (h *historyImporter) importCallLogs(
	ctx context.Context, records []*waSyncAction.CallLogRecord, stats *ImportTotals,
) {
	for _, record := range records {
		phone, ok := historyCallPhone(record, h.lookup, h.ownPhone)
		if !ok || record.GetCallID() == "" {
			stats.Skipped++
			continue
		}
		at := callLogAt(record)
		if at.IsZero() {
			stats.Skipped++
			continue
		}
		name := h.pushNames[phone]
		sender, _, err := h.senders.Upsert(ctx, h.scope, phone, name, at)
		if err != nil {
			h.log.Warn("import history call sender", "phone", phone, "error", err)
			continue
		}
		chat, err := h.chats.Ensure(ctx, h.scope, h.bot, sender.ID, at)
		if err != nil {
			h.log.Warn("import history call chat", "phone", phone, "error", err)
			continue
		}
		if err := h.chats.NoteHistory(ctx, chat.ID, at, at); err != nil {
			h.log.Warn("import history call activity", "phone", phone, "error", err)
			continue
		}
		direction := botflota.Inbound
		origin := botflota.MessageOrigin("")
		if !record.GetIsIncoming() {
			direction = botflota.Outbound
			origin = botflota.OriginUnknown
		}
		message := botflota.Message{
			ChatID:            chat.ID,
			Direction:         direction,
			Content:           botflota.CallContent{Label: historyCallLogLabel(record)},
			At:                at,
			ProviderMessageID: record.GetCallID(),
			Ingest:            botflota.IngestSync,
			Origin:            origin,
			Routing:           botflota.RoutingUnknown,
		}
		stats.Received++
		if err := h.messages.Append(ctx, &message); err != nil {
			if errors.Is(err, botflota.ErrDuplicate) {
				stats.Duplicate++
			} else {
				h.log.Warn("import history call", "phone", phone, "error", err)
			}
			continue
		}
		stats.Imported++
	}
}

// importPushNames applies the contact names WhatsApp sends in its own chunk.
// It only names senders Botflota already knows; a contact that never wrote is
// remembered for a sender created by a later chunk, not created here.
func (h *historyImporter) importPushNames(
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
		if err := h.senders.SetDisplayName(ctx, h.scope, phone, name, at); err != nil {
			h.log.Warn("import history push name", "error", err)
		}
	}
}

// importConversation stores the content messages and call attempts of one
// conversation, oldest first, and widens the chat's activity to cover them. A
// conversation registers its sender and chat only when it stored at least one
// message or call: WhatsApp sends a control-only shell for every number the
// account's address book knows, and a chat with no message is not history
// (ADR 0035). It returns how many messages were imported, how many were
// duplicates, how many entries were skipped, and how many entries carried a
// provider id.
func (h *historyImporter) importConversation(
	ctx context.Context, conversation *waHistorySync.Conversation,
) (int, int, int, int, error) {
	phone, ok := historyChatPhone(conversation, h.lookup)
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
		if at := historyEntryAt(conversation, entry); !at.IsZero() {
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
		if record.Direction == botflota.Inbound && pushName != "" {
			name = pushName
		}
		records = append(records, record)
	}
	// A conversation whose entries are all control traffic registers nothing:
	// no sender, no chat, no row. WhatsApp sends such a shell for every number
	// the account's address book knows, and a chat with no message is noise,
	// not history.
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
	sender, _, err := h.senders.Upsert(ctx, h.scope, phone, name, ensureAt)
	if err != nil {
		return 0, 0, skipped, received, fmt.Errorf("sender %s: %w", phone, err)
	}
	chat, err := h.chats.Ensure(ctx, h.scope, h.bot, sender.ID, ensureAt)
	if err != nil {
		return 0, 0, skipped, received, fmt.Errorf("chat %s: %w", phone, err)
	}
	if err := h.chats.NoteHistory(ctx, chat.ID, from, to); err != nil {
		return 0, 0, skipped, received, fmt.Errorf("chat activity %s: %w", phone, err)
	}
	imported, duplicate := 0, 0
	for i := range records {
		records[i].ChatID = chat.ID
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

// historyRecord maps one synced message to a stored message. Every content
// kind is kept, so a chat whose history is all media still gets a row; a
// control or internal entry, and one without a channel id, reports false and is
// skipped (ADR 0035, ADR 0036).
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
		if phone, ok := historyJIDPhone(pn, lookup); ok {
			return phone, true
		}
	}
	id := conversation.GetID()
	if id == "" {
		return "", false
	}
	return historyJIDPhone(id, lookup)
}

// historyJIDPhone resolves a PN or LID string to the domain phone number.
func historyJIDPhone(raw string, lookup pnLookup) (string, bool) {
	jid, err := types.ParseJID(raw)
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
	return historyJIDPhone(id, lookup)
}

// historyCallPhone finds the non-local participant of a 1:1 call record.
func historyCallPhone(
	record *waSyncAction.CallLogRecord, lookup pnLookup, ownPhone string,
) (string, bool) {
	if record == nil {
		return "", false
	}
	if group := record.GetGroupJID(); group != "" {
		if jid, err := types.ParseJID(group); err == nil &&
			jid.Server == types.GroupServer {
			return "", false
		}
	}
	candidates := make([]string, 0, len(record.GetParticipants())+1)
	if record.GetIsIncoming() {
		candidates = append(candidates, record.GetCallCreatorJID())
	}
	for _, participant := range record.GetParticipants() {
		candidates = append(candidates, participant.GetUserJID())
	}
	if !record.GetIsIncoming() {
		candidates = append(candidates, record.GetCallCreatorJID())
	}
	for _, candidate := range candidates {
		phone, ok := historyJIDPhone(candidate, lookup)
		if ok && phone != ownPhone && phone != "0" {
			return phone, true
		}
	}
	return "", false
}

// callLogAt handles both epoch-seconds and epoch-milliseconds in sync records.
func callLogAt(record *waSyncAction.CallLogRecord) time.Time {
	value := record.GetStartTime()
	if value <= 0 {
		return time.Time{}
	}
	if value >= 1_000_000_000_000 {
		return time.UnixMilli(value).UTC()
	}
	return time.Unix(value, 0).UTC()
}

func historyCallLogLabel(record *waSyncAction.CallLogRecord) string {
	missed := false
	switch record.GetCallResult() {
	case waSyncAction.CallLogRecord_MISSED,
		waSyncAction.CallLogRecord_REJECTED,
		waSyncAction.CallLogRecord_FAILED:
		missed = true
	}
	switch {
	case missed && record.GetIsVideo():
		return "Missed video call"
	case missed:
		return "Missed call"
	case record.GetIsVideo():
		return "Video call"
	default:
		return "Call"
	}
}
