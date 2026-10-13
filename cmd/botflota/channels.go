package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/felixzsh/botflota/adapters/whatsapp/channel"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/config"
	"github.com/felixzsh/botflota/spec"
)

// pureCatalog is the catalog config-only commands validate against: the
// installed definitions without builders, so validation opens no store and
// starts no transport (ADR 0052, ADR 0054).
func pureCatalog() (*channels.Catalog, error) {
	return channels.New(channel.Definition())
}

// runtimeCatalog binds the installed definitions to the process-wide
// dependencies of a running instance.
func runtimeCatalog(deps channel.Deps) (*channels.Catalog, error) {
	def := channel.Definition()
	def.Build = channel.Builder(deps)
	def.Prepare = channel.Prepare(deps)
	def.Setup = channel.Setup(deps)
	return channels.New(def)
}

// whatsappSelected reports whether the config runs a bot on the WhatsApp
// adapter. Only a selected adapter's dependencies are opened (ADR 0054).
func whatsappSelected(cfg *config.Config) bool {
	for _, scopeName := range config.SortedKeys(cfg.Scopes) {
		for _, botName := range config.SortedKeys(cfg.Scopes[scopeName].Bots) {
			bot := cfg.Scopes[scopeName].Bots[botName]
			if bot.Channel.Provider == channel.Provider &&
				bot.Channel.Mode == channel.Mode {
				return true
			}
		}
	}
	return false
}

// whatsappOptions decodes one bot's adapter-owned options from the running
// snapshot. The pairing path still runs only for WhatsApp bots until the
// neutral setup lifecycle lands (ADR 0053, ADR 0054).
func whatsappOptions(bot spec.Bot) (channel.BotOptions, error) {
	value, reasons := channel.Definition().Decode(bot)
	if len(reasons) > 0 {
		return channel.BotOptions{}, errors.New(strings.Join(reasons, "; "))
	}
	options, ok := value.(channel.BotOptions)
	if !ok {
		return channel.BotOptions{}, fmt.Errorf("whatsapp: unexpected options %T", value)
	}
	return options, nil
}
