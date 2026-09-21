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
