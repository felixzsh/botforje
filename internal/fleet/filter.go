package fleet

import "github.com/felixzsh/botflota/internal/domain"

// Filter holds one bot's inbound rules (ADR 0011): ignore groups, and an
// allow/ignore list of sender phone numbers. Deduplication lives in the
// channel adapter, which sees the transport event first.
type Filter struct {
	IgnoreGroups bool
	Allowed      []string
	Ignored      []string
}

// Allow reports whether the message reaches the engine.
func (f Filter) Allow(msg domain.IncomingMessage) bool {
	if msg.IsGroup && f.IgnoreGroups {
		return false
	}
	if matches(f.Ignored, msg) {
		return false
	}
	if len(f.Allowed) > 0 && !matches(f.Allowed, msg) {
		return false
	}
	return true
}

// matches compares the lists against the sender phone number.
func matches(list []string, msg domain.IncomingMessage) bool {
	for _, id := range list {
		if id == msg.Sender {
			return true
		}
	}
	return false
}
