package fleet

import "github.com/felixzsh/botflota"

// Filter holds one bot's inbound rules (ADR 0011). Deduplication lives in the
// channel adapter, which sees the transport event first. A shared conversation
// has no approved execution policy, so it is always refused rather than
// treated as a direct message (ADR 0051).
type Filter struct {
	Allowed []string
	Ignored []string
}

func (f Filter) Allow(msg botflota.IncomingMessage) bool {
	if msg.Target.Kind != botflota.ChatDirect {
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

func matches(list []string, msg botflota.IncomingMessage) bool {
	for _, id := range list {
		if id == msg.Sender.Subject {
			return true
		}
	}
	return false
}
