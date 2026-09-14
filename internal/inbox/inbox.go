// Package inbox filters inbound messages before the engine sees them
// (ADR 0011). Deduplication lives in the channel adapter, which sees the
// transport event first.
package inbox

import "github.com/felixzsh/botforje/internal/domain"

// Filter holds one bot's inbound rules: ignore groups, and an allow/ignore
// list of sender identities.
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

// matches compares the lists against the sender, its LID, and the chat JID,
// so ids like a broadcast address work too.
func matches(list []string, msg domain.IncomingMessage) bool {
	for _, id := range list {
		if id == msg.Sender || id == msg.SenderLID || id == msg.ReplyTo {
			return true
		}
	}
	return false
}
