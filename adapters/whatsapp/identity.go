package whatsapp

import (
	"context"
	"strings"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

// Number strips the device suffix and the server from a stored account JID
// for display: the JID itself stays internal (ADR 0020).
func Number(jid string) string {
	number := jid
	if at := strings.IndexByte(number, '@'); at >= 0 {
		number = number[:at]
	}
	if colon := strings.IndexByte(number, ':'); colon >= 0 {
		number = number[:colon]
	}
	return number
}

// pnLookup resolves a LID to its phone number through the LID store (ADR 0020).
type pnLookup func(lid types.JID) (types.JID, bool)

// resolveSender returns the sender's phone number JID, trying the JID itself,
// then SenderAlt, then the LID store. False means no phone number is known:
// the caller drops the message rather than key anything on a LID (ADR 0020).
func resolveSender(info types.MessageInfo, lookup pnLookup) (types.JID, bool) {
	if info.Sender.Server == types.DefaultUserServer {
		return info.Sender, true
	}
	if info.SenderAlt.Server == types.DefaultUserServer && !info.SenderAlt.IsEmpty() {
		return info.SenderAlt, true
	}
	if lookup != nil {
		if pn, ok := lookup(info.Sender); ok && !pn.IsEmpty() {
			return pn, true
		}
	}
	return types.JID{}, false
}

// devicePhone is the paired account's own number, which call-log routing uses
// to tell the local side from the peer.
func devicePhone(device *store.Device) string {
	if device == nil || device.GetJID().Server != types.DefaultUserServer {
		return ""
	}
	return device.GetJID().User
}

func (a *Adapter) lookupPN(lid types.JID) (types.JID, bool) {
	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil || client.Store == nil || client.Store.LIDs == nil {
		return types.JID{}, false
	}
	pn, err := client.Store.LIDs.GetPNForLID(context.Background(), lid)
	if err != nil || pn.IsEmpty() {
		return types.JID{}, false
	}
	return pn, true
}

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

// historyChatPhone prefers the conversation's phone JID over its chat id; a
// group, broadcast, or unresolved hidden-user chat reports false.
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

// historyPushNamePhone handles an entry that addresses the contact either by
// phone number or by hidden user id.
func historyPushNamePhone(entry *waHistorySync.Pushname, lookup pnLookup) (string, bool) {
	id := entry.GetID()
	if id == "" {
		return "", false
	}
	return historyJIDPhone(id, lookup)
}
