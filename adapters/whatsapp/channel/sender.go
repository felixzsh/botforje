package channel

import (
	"context"

	"go.mau.fi/whatsmeow/types"

	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// resolveSender returns the sender's phone number JID, trying the JID itself,
// then SenderAlt, then the LID store. False means no phone number is known:
// the caller drops the message rather than key anything on a LID (ADR 0020).
func resolveSender(info types.MessageInfo, lookup ingest.PNLookup) (types.JID, bool) {
	return resolveJID(info.Sender, info.SenderAlt, lookup)
}

// resolveJID returns the phone number JID behind a sender or chat, trying the
// JID itself, then its alternate, then the LID store. False means no phone
// number is known.
func resolveJID(jid, alt types.JID, lookup ingest.PNLookup) (types.JID, bool) {
	if jid.Server == types.DefaultUserServer {
		return jid, true
	}
	if alt.Server == types.DefaultUserServer && !alt.IsEmpty() {
		return alt, true
	}
	if lookup != nil {
		if pn, ok := lookup(jid); ok && !pn.IsEmpty() {
			return pn, true
		}
	}
	return types.JID{}, false
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
