package channel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/adapters/whatsapp"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// Installation is this adapter's registration with the composition root: the
// pure declaration config commands validate against, plus the lifecycle that
// opens the device store and the setup runner only when a bot selects it
// (ADR 0052, ADR 0053, ADR 0054).
func Installation() channels.Installation {
	return channels.Installation{Definition: Definition(), Install: install}
}

// install binds this adapter's declaration to one process's resources. Only a
// selected adapter reaches it, so an instance that runs no WhatsApp bot opens
// no device store and starts no setup worker (ADR 0054).
func install(ctx context.Context, host channels.Host) (channels.Installed, error) {
	log := host.Log
	if log == nil {
		log = botflota.DiscardLogger()
	}
	devices, err := whatsapp.OpenDevices(ctx, host.DB, log, host.DepsLogLevel)
	if err != nil {
		return channels.Installed{}, err
	}
	deps := Deps{
		Devices:   devices,
		Bindings:  host.Bindings,
		Peers:     host.Peers,
		Chats:     host.Chats,
		Messages:  host.Messages,
		Directory: host.Directory,
		Outbox:    host.Outbox,
		Pairs: whatsapp.NewPairManager(whatsapp.PairManagerOptions{
			Build:    pairBuild(host, devices),
			OnPaired: host.StartBot,
			Log:      log,
		}),
		DepsLogLevel: host.DepsLogLevel,
	}
	def := Definition()
	def.Build = Builder(deps)
	def.Prepare = Prepare(deps)
	def.Setup = Setup(deps)
	return channels.Installed{
		Definition: def,
		Validate: func(ctx context.Context) error {
			return whatsapp.ValidateAccountOwners(ctx, host.Bindings)
		},
		Close: deps.Pairs.Close,
	}, nil
}

// pairBuild resolves one bot's pairing settings from the snapshot the command
// holds, so a reload reaches a running session and never moves an offline one
// mid-bind (ADR 0025, ADR 0054).
func pairBuild(
	host channels.Host, devices *whatsapp.Devices,
) func(scope, bot string, fullHistory bool) (whatsapp.PairOptions, error) {
	return func(scope, botName string, fullHistory bool) (whatsapp.PairOptions, error) {
		if host.Bot == nil {
			return whatsapp.PairOptions{}, errors.New("whatsapp: no config snapshot")
		}
		bot, err := host.Bot(scope, botName)
		if err != nil {
			return whatsapp.PairOptions{}, err
		}
		options, err := botOptions(bot)
		if err != nil {
			return whatsapp.PairOptions{}, err
		}
		return whatsapp.PairOptions{
			Scope:          scope,
			Bot:            botName,
			Devices:        devices,
			Bindings:       host.Bindings,
			Peers:          host.Peers,
			Chats:          host.Chats,
			Messages:       host.Messages,
			History:        options.History,
			FullHistory:    fullHistory,
			Directory:      host.Directory,
			ImportContacts: options.ImportContacts,
			Outbox:         host.Outbox,
			Log:            host.Log,
			DepsLogLevel:   host.DepsLogLevel,
			DeviceName:     options.DeviceName,
		}, nil
	}
}

// botOptions decodes one bot's adapter-owned options through this adapter's
// own declaration, so the setup path reads exactly what config validation
// accepted (ADR 0054).
func botOptions(bot spec.Bot) (BotOptions, error) {
	value, reasons := Definition().Decode(bot)
	if len(reasons) > 0 {
		return BotOptions{}, errors.New(strings.Join(reasons, "; "))
	}
	options, ok := value.(BotOptions)
	if !ok {
		return BotOptions{}, fmt.Errorf("whatsapp: unexpected options %T", value)
	}
	return options, nil
}
