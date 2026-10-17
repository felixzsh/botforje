package fleet

import (
	"fmt"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// admitReplies validates one rendered reply against the consuming bot's
// installed definition before queue admission: declared content support and
// the definition's target rules (ADR 0044, ADR 0052). A nil catalog skips
// the check, so tests that drive the engine without definitions keep their
// behavior.
func admitReplies(
	catalog *channels.Catalog, sel spec.Channel,
) func(botflota.ChatTarget, botflota.SendableContent) error {
	return func(
		target botflota.ChatTarget, content botflota.SendableContent,
	) error {
		if catalog == nil {
			return nil
		}
		def, _, err := catalog.Resolve(sel)
		if err != nil {
			return err
		}
		if reasons := channels.AdmitSend(def, target, content); len(reasons) > 0 {
			return fmt.Errorf("fleet: reply not admitted: %s", reasons[0])
		}
		return nil
	}
}

// deliveryCheck re-invokes the same admission at delivery time, separately
// from the static declaration checked when the reply was queued. An
// eligibility change since admission fails the entry permanently: no silent
// downgrade, no useless retries (ADR 0052).
func deliveryCheck(
	catalog *channels.Catalog, sel spec.Channel,
) func(botflota.OutboxEntry) error {
	admit := admitReplies(catalog, sel)
	return func(entry botflota.OutboxEntry) error {
		return admit(entry.Target, entry.Content)
	}
}
