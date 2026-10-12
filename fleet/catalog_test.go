package fleet

import (
	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
)

// fakeCatalog installs one fake channel definition, registered exactly like an
// adapter is, so fleet tests exercise the same lookup and builder path as the
// composition root (ADR 0052, ADR 0054). build receives the neutral per-bot
// values; nil leaves a definition without a builder.
func fakeCatalog(
	build func(scope, bot string, options any) (botflota.MessageChannel, error),
) *channels.Catalog {
	def := channels.Definition{
		Provider: "whatsapp",
		Kind:     "fake",
		Modes:    []string{"native"},
		Capabilities: channels.Capabilities{Send: []botflota.MessageKind{
			botflota.KindText,
			botflota.KindImage, botflota.KindVideo, botflota.KindAudio,
			botflota.KindDocument, botflota.KindSticker,
			botflota.KindLocation, botflota.KindContact, botflota.KindPoll,
		}},
	}
	if build != nil {
		def.Build = func(bind channels.Bind) (botflota.MessageChannel, error) {
			return build(bind.Scope, bind.Bot, bind.Options)
		}
	}
	catalog, err := channels.New(def)
	if err != nil {
		panic(err)
	}
	return catalog
}
