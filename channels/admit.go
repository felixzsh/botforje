package channels

import (
	"fmt"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// AdmitSend validates one rendered outbound message before queue admission:
// the definition's declared content support and its target rules (ADR 0044,
// ADR 0052). It is pure: no store opens and no transport starts. API sends
// and engine replies share it, and delivery re-invokes it independently, so
// admission and delivery agree without trusting each other. A non-empty
// result refuses the send; callers fail it permanently, never downgrade or
// split it. Selector ownership stays with the caller: the API matches the
// target provider against the bot's selector first, and a definition that
// only serves its own provider refuses the rest through ValidateTarget.
func AdmitSend(
	def Definition, target botflota.ChatTarget, content botflota.SendableContent,
) []string {
	if !target.Valid() {
		return []string{"target needs provider, kind, and thread"}
	}
	kind := botflota.ContentKind(content)
	if !def.Capabilities.SupportsSend(kind) {
		return []string{fmt.Sprintf(
			"channel %s cannot send %s", def.Provider, kind)}
	}
	if card, ok := content.(botflota.ContactContent); ok {
		for _, entry := range card.Entries {
			variant := spec.ContactEntryKind(entry.Kind)
			if variant.Valid() &&
				!def.Capabilities.SupportsCardVariant(variant) {
				return []string{fmt.Sprintf(
					"channel %s cannot compose contact entry kind %q",
					def.Provider, entry.Kind)}
			}
		}
	}
	if def.ValidateTarget != nil {
		if reasons := def.ValidateTarget(target); len(reasons) > 0 {
			return reasons
		}
	}
	return nil
}
