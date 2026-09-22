package config

import (
	"fmt"

	"github.com/felixzsh/botflota/spec"
)

// RequiresRestart names the changes a running fleet cannot adopt, so a reload
// keeps them out and says why instead of applying half a document (ADR 0009):
// global settings, the set of scopes and bots, and the bot settings baked
// into a live channel and outbox. Graphs, actions, catalogs, and the bot
// filters adopt at the next message.
func RequiresRestart(current, next *Config) []string {
	reasons := globalChanges(current, next)
	reasons = append(reasons, botSetChanges(current, next)...)
	return append(reasons, channelSettingsChanges(current, next)...)
}

func globalChanges(current, next *Config) []string {
	var reasons []string
	for _, change := range []struct{ name, was, now string }{
		{"address", current.Address, next.Address},
		{"port", fmt.Sprint(current.Port), fmt.Sprint(next.Port)},
		{"log_level", current.LogLevel, next.LogLevel},
		{"log_level_deps", current.LogLevelDeps, next.LogLevelDeps},
		{
			"default_timeout",
			fmt.Sprint(current.DefaultTimeout), fmt.Sprint(next.DefaultTimeout),
		},
		{"data_dir", current.DataDir, next.DataDir},
		{"device_name", current.DeviceName, next.DeviceName},
	} {
		if change.was != change.now {
			reasons = append(reasons, "global setting "+change.name)
		}
	}
	return reasons
}

func botSetChanges(current, next *Config) []string {
	var reasons []string
	for _, scopeName := range SortedKeys(current.Scopes) {
		if _, ok := next.Scopes[scopeName]; !ok {
			reasons = append(reasons, fmt.Sprintf("scope %q removed", scopeName))
		}
	}
	for _, scopeName := range SortedKeys(next.Scopes) {
		now := next.Scopes[scopeName]
		was, existed := current.Scopes[scopeName]
		if !existed {
			reasons = append(reasons, fmt.Sprintf("scope %q added", scopeName))
			continue
		}
		for _, botName := range SortedKeys(was.Bots) {
			if _, ok := now.Bots[botName]; !ok {
				reasons = append(reasons,
					fmt.Sprintf("bot %q of scope %q removed", botName, scopeName))
			}
		}
		for _, botName := range SortedKeys(now.Bots) {
			if _, ok := was.Bots[botName]; !ok {
				reasons = append(reasons,
					fmt.Sprintf("bot %q of scope %q added", botName, scopeName))
			}
		}
	}
	return reasons
}

// channelSettingsChanges reports bots whose settings are wired into the live
// channel and outbox at build time. Only the filter fields adopt live
// (ADR 0009).
func channelSettingsChanges(current, next *Config) []string {
	var reasons []string
	for _, scopeName := range SortedKeys(next.Scopes) {
		wasScope, existed := current.Scopes[scopeName]
		if !existed {
			continue
		}
		for _, botName := range SortedKeys(next.Scopes[scopeName].Bots) {
			wasBot, existed := wasScope.Bots[botName]
			if !existed {
				continue // reported as an added bot
			}
			if !channelSettingsEqual(wasBot.Settings, next.Scopes[scopeName].Bots[botName].Settings) {
				reasons = append(reasons,
					fmt.Sprintf("channel settings of bot %q in scope %q",
						botName, scopeName))
			}
		}
	}
	return reasons
}

func channelSettingsEqual(was, now *spec.BotSettings) bool {
	old, current := botSettingsOf(was), botSettingsOf(now)
	return optionalBoolEqual(old.ReadReceipts, current.ReadReceipts) &&
		old.ReadReceiptDelay == current.ReadReceiptDelay &&
		old.ReplyDelay == current.ReplyDelay &&
		optionalBoolEqual(old.SimulateTyping, current.SimulateTyping) &&
		old.QueueDelay == current.QueueDelay &&
		old.Jitter == current.Jitter &&
		old.MaxMsgPerMinute == current.MaxMsgPerMinute &&
		optionalBoolEqual(old.HistorySync, current.HistorySync) &&
		optionalBoolEqual(old.ImportContacts, current.ImportContacts)
}

func botSettingsOf(settings *spec.BotSettings) spec.BotSettings {
	if settings == nil {
		return spec.BotSettings{}
	}
	return *settings
}

func optionalBoolEqual(was, now *bool) bool {
	if was == nil || now == nil {
		return was == now
	}
	return *was == *now
}
