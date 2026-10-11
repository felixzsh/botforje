package stores_test

import "github.com/felixzsh/botflota"

// waKey and waTarget build the neutral values these tests assert on from the
// plain WhatsApp subjects they use; the provider is opaque to the runtime.
func waKey(subject string) botflota.IdentityKey {
	return botflota.IdentityKey{Provider: "whatsapp", Subject: subject}
}

func waTarget(subject string) botflota.ChatTarget {
	return botflota.ChatTarget{
		Provider: "whatsapp", Kind: botflota.ChatDirect, Thread: subject,
	}
}

// waPeerPtr is the optional known recipient of an API send.
func waPeerPtr(subject string) *botflota.IdentityKey {
	key := waKey(subject)
	return &key
}
