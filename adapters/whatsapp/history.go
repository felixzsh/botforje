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
// appends the text messages; a repeated chunk is deduplicated through the
// channel message id.
type historyImporter struct {
	scope    string
	bot      string
	senders  botflota.SenderStore
	chats    botflota.ChatStore
	messages botflota.MessageStore
	lookup   pnLookup
	log      *slog.Logger
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
		messages: messages, lookup: lookup, log: log,
	}
}

// Handle imports one history-sync chunk and logs its progress. Conversation
// and message errors are logged and skipped; one bad row never aborts a sync.
func (h *historyImporter) Handle(ctx context.Context, event *events.HistorySync) {
	if event == nil || event.Data == nil {
		return
	}
	data := event.Data
	var conversations, imported, duplicate, skipped int
	for _, conversation := range data.GetConversations() {
		conversations++
		got, dup, skip, err := h.importConversation(ctx, conversation)
		if err != nil {
			h.log.Warn("import history conversation", "error", err)
		}
		imported += got
		duplicate += dup
		skipped += skip
	}
	h.log.Info("history sync imported",
		"sync_type", data.GetSyncType().String(),
		"chunk", data.GetChunkOrder(),
		"progress", data.GetProgress(),
		"conversations", conversations,
		"imported", imported,
		"duplicate", duplicate,
		"skipped", skipped)
}

// importConversation stores the text messages of one conversation, oldest
// first. It returns how many messages were imported, how many were duplicates,
// and how many entries were skipped.
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
	var activityAt time.Time
	for _, entry := range ordered {
		record, pushName, ok := historyRecord(conversation, entry)
		if !ok {
			skipped++
			continue
		}
		if record.Direction == botflota.Inbound && pushName != "" {
			name = pushName
		}
		if record.At.After(activityAt) {
			activityAt = record.At
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return 0, 0, skipped, nil
	}
	if activityAt.IsZero() {
		activityAt = time.Unix(int64(conversation.GetLastMsgTimestamp()), 0).UTC()
	}
	sender, _, err := h.senders.Upsert(ctx, h.scope, phone, name, activityAt)
	if err != nil {
		return 0, 0, skipped, fmt.Errorf("sender %s: %w", phone, err)
	}
	chat, err := h.chats.Ensure(ctx, h.scope, h.bot, sender.ID, activityAt)
	if err != nil {
		return 0, 0, skipped, fmt.Errorf("chat %s: %w", phone, err)
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

// historyRecord maps one synced message to a stored message. It reports false
// for anything the import does not keep: media, reactions, edits, deletions,
// and rows without a text body or a channel id.
func historyRecord(
	conversation *waHistorySync.Conversation, entry *waHistorySync.HistorySyncMsg,
) (botflota.Message, string, bool) {
	web := entry.GetMessage()
	if web == nil || web.GetKey() == nil {
		return botflota.Message{}, "", false
	}
	body := messageText(web.GetMessage())
	if body == "" {
		return botflota.Message{}, "", false
	}
	providerID := web.GetKey().GetID()
	if providerID == "" {
		return botflota.Message{}, "", false
	}
	direction := botflota.Inbound
	origin := botflota.MessageOrigin("")
	if web.GetKey().GetFromMe() {
		direction = botflota.Outbound
		origin = botflota.OriginUnknown
	}
	at := time.Unix(int64(web.GetMessageTimestamp()), 0).UTC()
	if web.GetMessageTimestamp() == 0 {
		at = time.Unix(int64(conversation.GetLastMsgTimestamp()), 0).UTC()
	}
	return botflota.Message{
		Direction:         direction,
		Kind:              botflota.KindText,
		Body:              body,
		At:                at,
		ProviderMessageID: providerID,
		Ingest:            botflota.IngestSync,
		Origin:            origin,
		Routing:           botflota.RoutingUnknown,
	}, web.GetPushName(), true
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
