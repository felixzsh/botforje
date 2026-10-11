package whatsapp

import (
	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp/ingest"
)

// waKey builds the normalized WhatsApp identity these tests assert on from the
// plain subject they use.
func waKey(subject string) botflota.IdentityKey {
	return botflota.IdentityKey{Provider: ingest.Provider, Subject: subject}
}
