package channel

import (
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// historySyncRequestID returns the request session id copied into a response.
func historySyncRequestID(event *events.HistorySync) string {
	if event == nil || event.Notification == nil {
		return ""
	}
	return event.Notification.GetPeerDataRequestSessionID()
}

func historySyncCompleteFor(
	event *events.HistorySync, phone string, lookup ingest.PNLookup,
) bool {
	if event == nil || event.Data == nil {
		return false
	}
	if event.Data.GetProgress() >= 100 ||
		(event.Notification != nil && event.Notification.GetProgress() >= 100) {
		return true
	}
	for _, conversation := range event.Data.GetConversations() {
		resolved, ok := ingest.HistoryChatPhone(conversation, lookup)
		if !ok || resolved != phone {
			continue
		}
		switch conversation.GetEndOfHistoryTransferType() {
		case waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY,
			waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_BUT_MORE_MSG_REMAIN_ON_PRIMARY,
			waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS:
			return true
		}
	}
	return false
}

// historySyncExhausted reports the phone's nothing-more-to-give marker, so
// paging would only repeat itself.
func historySyncExhausted(
	event *events.HistorySync, phone string, lookup ingest.PNLookup,
) bool {
	if event == nil || event.Data == nil {
		return false
	}
	for _, conversation := range event.Data.GetConversations() {
		resolved, ok := ingest.HistoryChatPhone(conversation, lookup)
		if !ok || resolved != phone {
			continue
		}
		switch conversation.GetEndOfHistoryTransferType() {
		case waHistorySync.Conversation_COMPLETE_AND_NO_MORE_MESSAGE_REMAIN_ON_PRIMARY,
			waHistorySync.Conversation_COMPLETE_ON_DEMAND_SYNC_WITH_MORE_MSG_ON_PRIMARY_BUT_NO_ACCESS:
			return true
		}
	}
	return false
}

// historyEventPage reads the requested chat out of one response chunk: how
// many entries it carried and the oldest among them. Entries for other
// conversations are metadata, not this chat's page.
func historyEventPage(
	event *events.HistorySync, phone string, lookup ingest.PNLookup,
) (int, *historyCursor) {
	if event == nil || event.Data == nil {
		return 0, nil
	}
	count := 0
	var next *historyCursor
	for _, conversation := range event.Data.GetConversations() {
		resolved, ok := ingest.HistoryChatPhone(conversation, lookup)
		if !ok || resolved != phone {
			continue
		}
		for _, entry := range conversation.GetMessages() {
			id := entry.GetMessage().GetKey().GetID()
			if id == "" {
				continue
			}
			count++
			at := ingest.HistoryEntryAt(conversation, entry)
			if at.IsZero() {
				continue
			}
			cursor := &historyCursor{
				ID:     id,
				FromMe: entry.GetMessage().GetKey().GetFromMe(),
				At:     at,
			}
			if next == nil || cursor.At.Before(next.At) {
				next = cursor
			}
		}
	}
	// A top-level call record is not a position in the chat's message list, so
	// it never counts towards a page's usable entries.
	return count, next
}
