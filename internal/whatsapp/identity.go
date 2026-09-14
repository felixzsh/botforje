package whatsapp

import "go.mau.fi/whatsmeow/types"

// pnLookup resolves a LID to its phone number through the LID store (ADR 0013).
type pnLookup func(lid types.JID) (types.JID, bool)

// resolveSender returns the session sender and, when known, its LID. The
// sender is a phone number whenever one can be resolved; otherwise it stays
// the LID and the session is unresolved (ADR 0013).
func resolveSender(info types.MessageInfo, lookup pnLookup) (sender, lid types.JID) {
	from := info.Sender
	alt := info.SenderAlt

	if from.Server == types.DefaultUserServer {
		if alt.Server == types.HiddenUserServer && !alt.IsEmpty() {
			return from, alt
		}
		return from, types.JID{}
	}
	if alt.Server == types.DefaultUserServer && !alt.IsEmpty() {
		return alt, from
	}
	if lookup != nil {
		if pn, ok := lookup(from); ok && !pn.IsEmpty() {
			return pn, from
		}
	}
	if from.Server == types.HiddenUserServer {
		return from, from
	}
	return from, types.JID{}
}
