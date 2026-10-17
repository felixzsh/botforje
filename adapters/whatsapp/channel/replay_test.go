package channel

import (
	"testing"

	"github.com/felixzsh/botflota"
)

// TestAdapterLeavesReceiveConfirmationUnimplemented pins the P5 decision
// that WhatsApp confirms no inbound source: live events arrive without a
// confirmable cursor, the on-demand backfill imports inert history, and
// read receipts are user-visible presence rather than processing
// completion (ADR 0004, ADR 0040). The fleet settles nothing for a channel
// without the optional port.
func TestAdapterLeavesReceiveConfirmationUnimplemented(t *testing.T) {
	var channel any = (*Adapter)(nil)
	if _, ok := channel.(botflota.ReceiveConfirmer); ok {
		t.Fatal("whatsapp adapter implements botflota.ReceiveConfirmer; " +
			"remove it and leave the source unconfirmed")
	}
}
