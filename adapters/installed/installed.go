// Package installed lists the channel adapters this distribution ships. It is
// the composition root's registry: onboarding an adapter adds its own package
// files and one entry in Channels, and no shared command, API, or runtime file
// changes (ADR 0052, ADR 0054).
package installed

import (
	"github.com/felixzsh/botflota/adapters/testchannel"
	whatsappchannel "github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/channels"
)

// Channels returns one installation per shipped channel adapter.
func Channels() []channels.Installation {
	return []channels.Installation{
		whatsappchannel.Installation(),
		testchannel.Installation(),
	}
}
