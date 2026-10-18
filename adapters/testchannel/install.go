package testchannel

import (
	"context"

	"github.com/felixzsh/botflota/channels"
)

// Installation is this adapter's registration with the composition root: the
// pure declaration plus the lifecycle that binds its in-memory provider when a
// bot selects it (ADR 0052, ADR 0054).
func Installation() channels.Installation {
	return channels.Installation{
		Definition: Definition(),
		Install: func(_ context.Context, host channels.Host) (channels.Installed, error) {
			def := Definition()
			deps := Deps{Bindings: host.Bindings, Backend: NewBackend()}
			def.Build = Builder(deps)
			def.Prepare = Prepare(deps)
			def.Setup = Setup(deps)
			return channels.Installed{Definition: def}, nil
		},
	}
}
