package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/felixzsh/botflota/spec"
)

// ReloadPlan separates instance changes from scope changes. ScopeReasons
// identifies a scope whose actor set or channel cannot be changed live.
type ReloadPlan struct {
	Scopes       []string
	ScopeReasons map[string][]string
	Global       []string
}

// PlanReload compares the active configuration with a fully loaded candidate.
// Callers may activate safe scopes while leaving other scopes pending.
func PlanReload(current, next *Config) ReloadPlan {
	plan := ReloadPlan{ScopeReasons: map[string][]string{}}
	catalogs := catalogChangedScopes(current, next)
	plan.Global = globalChanges(current, next, catalogs)

	names := map[string]bool{}
	for name := range current.Scopes {
		names[name] = true
	}
	for name := range next.Scopes {
		names[name] = true
	}
	for _, name := range SortedKeys(names) {
		was, had := current.Scopes[name]
		now, has := next.Scopes[name]
		if had == has && reflect.DeepEqual(was, now) {
			continue
		}
		plan.Scopes = append(plan.Scopes, name)
		if !had {
			plan.ScopeReasons[name] = []string{fmt.Sprintf("scope %q added", name)}
			continue
		}
		if !has {
			plan.ScopeReasons[name] = []string{fmt.Sprintf("scope %q removed", name)}
			continue
		}
		reasons := scopeRuntimeChanges(was, now, name)
		if catalogs[name] {
			// A rotation changes the value each reference already holds, and a
			// removed declaration would resolve from the process environment: a
			// running scope keeps what it has until the owner restarts
			// (ADR 0049).
			reasons = append(reasons, fmt.Sprintf(
				"secret catalog of scope %q changed; restart to adopt it", name))
		}
		plan.ScopeReasons[name] = reasons
	}
	return plan
}

// catalogChangedScopes names the scopes whose own secret catalog changed, in its
// declared path or in the file's content. It is the plan's only comparison of
// the catalogs, for both the global reason and the per-scope hold-back; a scope
// whose catalog did not change is never held back by another's (ADR 0049).
func catalogChangedScopes(current, next *Config) map[string]bool {
	changed := map[string]bool{}
	for _, name := range SortedKeys(current.SecretFiles) {
		changed[name] = true
	}
	for _, name := range SortedKeys(next.SecretFiles) {
		changed[name] = true
	}
	for name := range changed {
		if current.SecretFiles[name] == next.SecretFiles[name] &&
			reflect.DeepEqual(current.catalogs[name], next.catalogs[name]) {
			delete(changed, name)
		}
	}
	return changed
}

func scopeRuntimeChanges(was, now spec.Scope, name string) []string {
	var reasons []string
	for _, bot := range SortedKeys(was.Bots) {
		if _, ok := now.Bots[bot]; !ok {
			reasons = append(reasons,
				fmt.Sprintf("bot %q of scope %q removed", bot, name))
		}
	}
	for _, bot := range SortedKeys(now.Bots) {
		if _, ok := was.Bots[bot]; !ok {
			reasons = append(reasons,
				fmt.Sprintf("bot %q of scope %q added", bot, name))
		}
	}
	for _, bot := range SortedKeys(now.Bots) {
		old, ok := was.Bots[bot]
		if !ok || channelOf(old) == channelOf(now.Bots[bot]) &&
			channelSettingsEqual(old.Settings, now.Bots[bot].Settings) {
			continue
		}
		reasons = append(reasons,
			fmt.Sprintf("channel settings of bot %q in scope %q", bot, name))
	}
	return reasons
}

// channelOf describes a bot's installed implementation and its adapter-owned
// options as the file authored them; a provider, mode, or option change is a
// binding-relevant change, not an ordinary setting edit (ADR 0054).
func channelOf(bot spec.Bot) string {
	encoded, err := json.Marshal(bot.Channel)
	if err != nil {
		// Options that cannot round-trip still differ from the provider and
		// mode alone, so keep the identity part instead of dropping the change.
		return bot.Channel.Provider + "/" + bot.Channel.Mode
	}
	return bot.Channel.Provider + "/" + bot.Channel.Mode + ":" + string(encoded)
}

func globalChanges(current, next *Config, catalogs map[string]bool) []string {
	var reasons []string
	for _, change := range []struct{ name, was, now string }{
		{"address", current.Address, next.Address},
		{"port", fmt.Sprint(current.Port), fmt.Sprint(next.Port)},
		{"log_level", current.LogLevel, next.LogLevel},
		{"log_level_deps", current.LogLevelDeps, next.LogLevelDeps},
		{"default_timeout", fmt.Sprint(current.DefaultTimeout),
			fmt.Sprint(next.DefaultTimeout)},
		{"data_dir", current.DataDir, next.DataDir},
		{"rate_limit", fmt.Sprint(current.RateBudgets()),
			fmt.Sprint(next.RateBudgets())},
		{"exclusive_scopes", fmt.Sprint(current.ExclusiveScopes),
			fmt.Sprint(next.ExclusiveScopes)},
		{"egress", current.Egress, next.Egress},
	} {
		if change.was != change.now {
			reasons = append(reasons, "global setting "+change.name)
		}
	}
	if !reflect.DeepEqual(current.Webhooks, next.Webhooks) {
		reasons = append(reasons, "global setting webhooks")
	}
	// A file root is the owner's bound on one scope's local reads: only the
	// owner's restart adopts it, never a publication or a live reload
	// (ADR 0047).
	if !reflect.DeepEqual(current.FileRoots, next.FileRoots) {
		reasons = append(reasons, "global setting file_roots")
	}
	// A secret catalog is the owner's bound on which names a scope may resolve,
	// so only the owner's restart adopts a change — in the declaration or in
	// the file's rotation (ADR 0049).
	if len(catalogs) > 0 {
		reasons = append(reasons, "global setting secret_files")
	}
	return reasons
}

func channelSettingsEqual(was, now *spec.BotSettings) bool {
	old, current := botSettingsOf(was), botSettingsOf(now)
	// Only the settings that shape the channel and outbox at build time force a
	// restart; contact filtering and the hand-over window are adopted live.
	return old.QueueDelay == current.QueueDelay &&
		old.Jitter == current.Jitter &&
		old.MaxMsgPerMinute == current.MaxMsgPerMinute
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

// WithScopes copies selected scopes and the candidate's file watch metadata
// into an active snapshot without publishing pending instance-level changes.
func WithScopes(current, next *Config, scopes []string) *Config {
	active := *current
	active.Schema = next.Schema
	active.Include = slices.Clone(next.Include)
	active.Scopes = make(map[string]spec.Scope, len(current.Scopes)+len(scopes))
	for name, scope := range current.Scopes {
		active.Scopes[name] = scope
	}
	for _, name := range scopes {
		if scope, ok := next.Scopes[name]; ok {
			active.Scopes[name] = scope
		} else {
			delete(active.Scopes, name)
		}
	}
	active.files = slices.Clone(next.files)
	active.watchPaths = slices.Clone(next.watchPaths)
	return &active
}
