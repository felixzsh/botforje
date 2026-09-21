package ingest

import (
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

// PNLookup resolves a LID to its phone number through the LID store (ADR 0020).
type PNLookup func(lid types.JID) (types.JID, bool)

func historyJIDPhone(raw string, lookup PNLookup) (string, bool) {
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

// HistoryChatPhone prefers the conversation's phone JID over its chat id; a
// group, broadcast, or unresolved hidden-user chat reports false.
func HistoryChatPhone(
	conversation *waHistorySync.Conversation, lookup PNLookup,
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

// historyPushNamePhone handles an entry that addresses the contact either by
// phone number or by hidden user id.
func historyPushNamePhone(entry *waHistorySync.Pushname, lookup PNLookup) (string, bool) {
	id := entry.GetID()
	if id == "" {
		return "", false
	}
	return historyJIDPhone(id, lookup)
}
