package api

import (
	"testing"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// testCatalog is the installed set the API tests publish against: one fake
// definition registered like an adapter, so a publication is validated against
// declared channel shapes rather than a provider switch (ADR 0052, ADR 0054).
func testCatalog(t *testing.T) *channels.Catalog {
	t.Helper()
	catalog, err := channels.New(channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{Send: []botflota.MessageKind{
			botflota.KindText,
			botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
			botflota.KindDocument, botflota.KindSticker,
			botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
		}},
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return catalog
}
