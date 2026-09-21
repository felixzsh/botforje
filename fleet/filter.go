package fleet

import "github.com/felixzsh/botflota"

// Filter holds one bot's inbound rules (ADR 0011). Deduplication lives in the
// channel adapter, which sees the transport event first.
type Filter struct {
	IgnoreGroups bool
	Allowed      []string
	Ignored      []string
}

func (f Filter) Allow(msg botflota.IncomingMessage) bool {
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

func matches(list []string, msg botflota.IncomingMessage) bool {
	for _, id := range list {
		if id == msg.SenderPhone {
			return true
		}
	}
	return false
}
