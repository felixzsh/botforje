package engine_test

import (
	"testing"

	"github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/channels"
)

// testCatalog is the installed set the engine tests validate fixtures against:
// the real WhatsApp declaration without its builder (ADR 0052, ADR 0054).
func testCatalog(t *testing.T) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(channel.Definition())
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}
