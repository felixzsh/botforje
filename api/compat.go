package api

import "github.com/felixzsh/botflota"

// whatsappProvider is the catalog identifier of the adapter this API version
// addresses. Neutral references carry their own provider; the legacy selector
// below belongs to WhatsApp alone (ADR 0051, ADR 0054).
const whatsappProvider = "whatsapp"

// legacyWhatsAppPeer reads the plain selector the API and CLI have always
// addressed — a WhatsApp phone-number subject — as a comparable peer key. It
// is the named WhatsApp compatibility boundary ADR 0054 keeps while neutral
// references are added, not a universal identity derivation.
func legacyWhatsAppPeer(subject string) botflota.IdentityKey {
	return botflota.IdentityKey{Provider: whatsappProvider, Subject: subject}
}

// legacyWhatsAppTarget reads the same selector as the direct conversation it
// addresses. Its thread equals the peer subject because the WhatsApp adapter
// establishes that mapping; common code never derives a target from an
// identity outside this boundary (ADR 0051).
func legacyWhatsAppTarget(subject string) botflota.ChatTarget {
	return botflota.ChatTarget{
		Provider: whatsappProvider, Kind: botflota.ChatDirect, Thread: subject,
	}
}
