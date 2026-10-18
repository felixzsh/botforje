package fleet

import "github.com/felixzsh/botflota"

// Filter holds one bot's inbound rules (ADR 0011). Provider is the bot's own
// channel provider: every allow/ignore entry names a subject inside that
// provider's identity space, so a subject from another provider never matches
// one (ADR 0051). A shared conversation has no approved execution policy, so
// it is always refused rather than treated as a direct message.
//
// Redelivery is settled after this filter, not by it: the engine checks the
// provider message id and MessageStore.Append reports the already-stored one,
// so no allow/ignore rule is the durable deduplication gate.
type Filter struct {
	Provider string
	Allowed  []string
	Ignored  []string
}

func (f Filter) Allow(msg botflota.IncomingMessage) bool {
	if msg.Target.Kind != botflota.ChatDirect {
		return false
	}
	if f.matches(f.Ignored, msg) {
		return false
	}
	if len(f.Allowed) > 0 && !f.matches(f.Allowed, msg) {
		return false
	}
	return true
}

// matches reports whether one allow/ignore list names the message's sender. A
// message from another provider can never match: the entries are subjects of
// the bot's own provider, and the same subject string is a different identity
// there.
func (f Filter) matches(list []string, msg botflota.IncomingMessage) bool {
	if msg.Sender.Provider != f.Provider {
		return false
	}
	for _, id := range list {
		if id == msg.Sender.Subject {
			return true
		}
	}
	return false
}
