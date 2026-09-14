package whatsapp

import "go.mau.fi/whatsmeow/types"

// pnLookup resolves a LID to its phone number through the LID store (ADR 0013).
type pnLookup func(lid types.JID) (types.JID, bool)

// resolveSender returns the sender's phone number JID. It uses the JID itself
// when it is a phone number, then SenderAlt, then the LID store. It returns
// false when no phone number is known, so the caller drops the message instead
// of keying anything on a LID (ADR 0020).
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
