package ingest

import "github.com/felixzsh/botflota"

// Provider is the catalog identifier of the WhatsApp adapter (ADR 0051).
const Provider = "whatsapp"

// PeerKey normalizes a phone-number user into the opaque comparable identity
// the core stores. PN subjects are provider-wide, so they need no local
// prefix; the digits are an opaque subject, not telephone metadata the common
// code may parse (ADR 0020, ADR 0051).
func PeerKey(phone string) botflota.IdentityKey {
	return botflota.IdentityKey{Provider: Provider, Subject: phone}
}

// DirectTarget normalizes a phone-number user into the direct conversation
// target. Its thread equals the subject because this adapter establishes that
// mapping for direct chats; common code never derives it (ADR 0051).
func DirectTarget(phone string) botflota.ChatTarget {
	return botflota.ChatTarget{
		Provider: Provider, Kind: botflota.ChatDirect, Thread: phone,
	}
}
