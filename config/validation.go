package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/channels"
	"github.com/felixzsh/botflota/spec"
)

// ValidationError names the file, the entity, and the reason. Scope is the
// scope the problem belongs to, empty for an instance-level problem: an API
// caller is shown only the problems inside its own authority (docs/api.md).
type ValidationError struct {
	Scope  string
	File   string
	Entity string
	Reason string
}

type addError func(file, entity, format string, args ...any)

func (e ValidationError) Error() string {
	file := e.File
	if file == "" {
		file = "<config>"
	}
	if e.Entity == "" {
		return fmt.Sprintf("%s: %s", file, e.Reason)
	}
	return fmt.Sprintf("%s: %s: %s", file, e.Entity, e.Reason)
}

// Validate runs the semantic checks that a JSON Schema cannot express
// (ADR 0007, ADR 0033, ADR 0010, ADR 0030, ADR 0016, ADR 0052, ADR 0054). The
// catalog holds the installed channel definitions every bot is checked
// against; it is pure, so validation opens no store and starts no transport.
func Validate(cfg *Config, catalog *channels.Catalog) []ValidationError {
	if cfg == nil {
		return []ValidationError{{Reason: "nil config"}}
	}
	if catalog == nil {
		return []ValidationError{{Reason: "no channel catalog"}}
	}
	var errs []ValidationError
	// addScope reports one scope's problems; add reports instance-level ones,
	// which no scoped caller may see (docs/api.md).
	addScope := func(scope string) addError {
		return func(file, entity, format string, args ...any) {
			errs = append(errs, ValidationError{
				Scope:  scope,
				File:   file,
				Entity: entity,
				Reason: fmt.Sprintf(format, args...),
			})
		}
	}
	add := addScope("")

	switch cfg.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		add("", "config", "invalid log_level %q", cfg.LogLevel)
	}
	switch cfg.LogLevelDeps {
	case "", "debug", "info", "warn", "error", "off":
	default:
		add("", "config", "invalid log_level_deps %q", cfg.LogLevelDeps)
	}
	if cfg.RateLimit != nil {
		if v := cfg.RateLimit.CredentialPerMinute; v != nil && *v < 0 {
			add("", "config", "rate_limit.credential_per_minute must be >= 0")
		}
		if v := cfg.RateLimit.FailedPerMinute; v != nil && *v < 0 {
			add("", "config", "rate_limit.failed_per_minute must be >= 0")
		}
	}
	validateWebhooks(cfg.Webhooks, add)
	validateLabels(cfg, addScope)
	for _, scopeName := range SortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		scoped := addScope(scopeName)
		for _, name := range SortedKeys(scope.LLMs) {
			validateLLM(cfg, scopeName, name, scope.LLMs[name], scoped)
			checkSecretRefs(cfg, scoped, "llm", scopeName, name, scope.LLMs[name])
		}
		for _, name := range SortedKeys(scope.Actions) {
			validateAction(cfg, scopeName, name, scope.Actions[name], scoped)
			checkSecretRefs(cfg, scoped, "action", scopeName, name, scope.Actions[name])
		}
		for _, name := range SortedKeys(scope.Graphs) {
			validateGraph(cfg, scopeName, scope, name, scope.Graphs[name], scoped)
			checkSecretRefs(cfg, scoped, "graph", scopeName, name, scope.Graphs[name])
		}
		for _, name := range SortedKeys(scope.Bots) {
			validateBot(cfg, catalog, scopeName, scope, name, scope.Bots[name], scoped)
			checkSecretRefs(cfg, scoped, "bot", scopeName, name, scope.Bots[name])
		}
		validateScopeTemplates(cfg, scopeName, scope, scoped)
	}
	return errs
}

// validateScopeTemplates runs the template checks and attributes each finding
// to the file and entity that declared it (ADR 0018, ADR 0034).
func validateScopeTemplates(
	cfg *Config, scopeName string, scope spec.Scope, add addError,
) {
	for _, issue := range validateTemplates(scope) {
		if issue.Graph != "" {
			file := cfg.originOf("graph", scopeName, issue.Graph)
			entity := fmt.Sprintf("graph %q (scope %q) node %q",
				issue.Graph, scopeName, issue.Node)
			add(file, entity, "%s: %v", issue.Site, issue.Err)
			continue
		}
		file := cfg.originOf("action", scopeName, issue.Action)
		entity := fmt.Sprintf("action %q (scope %q)", issue.Action, scopeName)
		add(file, entity, "%s: %v", issue.Site, issue.Err)
	}
}

// webhookEvents is the outbound event vocabulary an instance may subscribe to
// (ADR 0045).
var webhookEvents = map[string]bool{botflota.EventPeerFirstSeen: true}

// validateWebhooks checks the instance-level webhook list: an absolute http or
// https URL, a signing secret, and known event names, with no URL repeated
// (ADR 0045).
func validateWebhooks(webhooks []WebhookSettings, add addError) {
	urls := map[string]bool{}
	for i, entry := range webhooks {
		entity := fmt.Sprintf("webhook %d", i)
		switch {
		case entry.URL == "":
			add("", entity, "url is required")
		default:
			parsed, err := url.Parse(entry.URL)
			if err != nil || parsed.Host == "" ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") {
				add("", entity, "url %q must be an absolute http or https URL", entry.URL)
			}
			if urls[entry.URL] {
				add("", entity, "duplicate url %q", entry.URL)
			}
			urls[entry.URL] = true
		}
		if entry.Secret.IsZero() {
			add("", entity, "secret is required")
		}
		if len(entry.Events) == 0 {
			add("", entity, "events must name at least one event")
		}
		for _, event := range entry.Events {
			if !webhookEvents[event] {
				add("", entity, "unknown event %q", event)
			}
		}
	}
}

func validateLLM(cfg *Config, scope, name string, llm spec.LLM, add addError) {
	file := cfg.originOf("llm", scope, name)
	entity := fmt.Sprintf("llm %q (scope %q)", name, scope)
	if llm.Provider == "" {
		add(file, entity, "provider is required")
	}
	if llm.Model == "" {
		add(file, entity, "model is required")
	}
	switch llm.Routing {
	case "", "off", "fallback", "always":
	default:
		add(file, entity, "invalid routing %q (want off, fallback, or always)", llm.Routing)
	}
	switch llm.Realization {
	case "", "off", "auto":
	default:
		add(file, entity, "invalid realization %q (want off or auto)", llm.Realization)
	}
	if llm.Bounds != nil {
		if llm.Bounds.MaxToolsPerMessage < 0 {
			add(file, entity, "max_tools_per_message must be >= 0")
		}
		if llm.Bounds.Timeout < 0 {
			add(file, entity, "timeout must be >= 0")
		}
	}
}

var inputTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true}
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

func validateAction(cfg *Config, scope, name string, a spec.Action, add addError) {
	file := cfg.originOf("action", scope, name)
	entity := fmt.Sprintf("action %q (scope %q)", name, scope)

	for _, in := range SortedKeys(a.Inputs) {
		input := a.Inputs[in]
		if input.Type != "" && !inputTypes[input.Type] {
			add(file, entity, "input %q has invalid type %q", in, input.Type)
		}
		if input.Bind != "" {
			if _, _, ok := splitBind(input.Bind); !ok {
				add(file, entity, "input %q bind %q must be node.<var> or graph.<var>", in, input.Bind)
			}
		}
	}
	if len(a.Steps) == 0 {
		add(file, entity, "no steps")
	}
	validateSteps(file, entity, a.Steps, add, "")
	validateGuards(file, entity, a.Guards, add)
}

func validateGuards(file, entity string, guards *spec.Guards, add addError) {
	if guards == nil {
		return
	}
	if cd := guards.Cooldown; cd != nil {
		if cd.Duration <= 0 {
			add(file, entity, "cooldown duration must be > 0")
		}
		validateSteps(file, entity, cd.OnBlocked, add, "cooldown.on_blocked")
	}
	if rl := guards.RateLimit; rl != nil {
		if rl.Limit <= 0 {
			add(file, entity, "rate_limit limit must be > 0")
		}
		if rl.Window <= 0 {
			add(file, entity, "rate_limit window must be > 0")
		}
		validateSteps(file, entity, rl.OnBlocked, add, "rate_limit.on_blocked")
	}
}

func validateSteps(file, entity string, steps []spec.Step, add addError, where string) {
	prefix := ""
	if where != "" {
		prefix = where + ": "
	}
	requestNames := map[string]bool{}
	for i, step := range steps {
		count := 0
		if step.Message != nil {
			count++
			validateMessage(file, entity, prefix, i, *step.Message, add)
		}
		if step.Request != nil {
			count++
			validateRequest(file, entity, prefix, i, *step.Request, requestNames, add)
		}
		if step.Location != nil {
			count++
		}
		if step.Media != nil {
			count++
			validateMedia(file, entity, prefix, i, *step.Media, add)
		}
		if step.Contact != nil {
			count++
			validateContact(file, entity, prefix, i, *step.Contact, add)
		}
		if step.Poll != nil {
			count++
			validatePoll(file, entity, prefix, i, *step.Poll, add)
		}
		if count != 1 {
			add(file, entity,
				"%sstep %d must be exactly one of message, request, location, media, contact, or poll",
				prefix, i)
		}
	}
}

// mediaKinds is the sendable media vocabulary (ADR 0036).
var mediaKinds = map[string]bool{
	"image": true, "video": true, "audio": true, "document": true, "sticker": true,
}

func validateMedia(file, entity, prefix string, i int, m spec.Media, add addError) {
	if !mediaKinds[m.Kind] {
		add(file, entity,
			"%sstep %d: media kind %q must be image, video, audio, document, or sticker",
			prefix, i, m.Kind)
	}
	switch {
	case m.URL == "" && m.Path == "":
		add(file, entity, "%sstep %d: media needs a url or a path", prefix, i)
	case m.URL != "" && m.Path != "":
		add(file, entity, "%sstep %d: media cannot set both url and path", prefix, i)
	}
}

func validateContact(file, entity, prefix string, i int, c spec.Contact, add addError) {
	if c.Name == "" {
		add(file, entity, "%sstep %d: contact needs a name", prefix, i)
	}
	if len(c.Entries) == 0 {
		add(file, entity, "%sstep %d: contact needs at least one entry", prefix, i)
	}
	for j, e := range c.Entries {
		if !spec.ContactEntryKind(e.Kind).Valid() {
			add(file, entity,
				"%sstep %d: contact entry %d kind %q must be phone, email, address, or url",
				prefix, i, j, e.Kind)
		}
		if e.Value == "" {
			add(file, entity, "%sstep %d: contact entry %d needs a value", prefix, i, j)
		}
	}
}

func validatePoll(file, entity, prefix string, i int, p spec.Poll, add addError) {
	if p.Question == "" {
		add(file, entity, "%sstep %d: poll needs a question", prefix, i)
	}
	if len(p.Options) < 2 {
		add(file, entity, "%sstep %d: poll needs at least two options", prefix, i)
	}
	if p.Selectable < 0 || p.Selectable > len(p.Options) {
		add(file, entity,
			"%sstep %d: poll selectable must be between 0 and the option count", prefix, i)
	}
}

func validateMessage(file, entity, prefix string, i int, m spec.Message, add addError) {
	if len(m.Body) == 0 && m.Intent == "" {
		add(file, entity, "%sstep %d: message needs body or intent", prefix, i)
	}
	if m.Policy != "" && m.Intent == "" {
		add(file, entity, "%sstep %d: policy requires intent", prefix, i)
	}
	if len(m.Body) > 1 && m.Intent != "" {
		add(file, entity, "%sstep %d: body variants cannot combine with intent", prefix, i)
	}
}

func validateRequest(
	file, entity, prefix string, i int, r spec.Request, names map[string]bool, add addError,
) {
	if r.Name == "" {
		add(file, entity, "%sstep %d: request needs a name", prefix, i)
	} else if names[r.Name] {
		add(file, entity, "%sstep %d: duplicate request name %q", prefix, i, r.Name)
	} else {
		names[r.Name] = true
	}
	if r.URL == "" {
		add(file, entity, "%sstep %d: request %q needs a url", prefix, i, r.Name)
	}
	if r.Method != "" && !httpMethods[r.Method] {
		add(file, entity, "%sstep %d: request %q has invalid method %q", prefix, i, r.Name, r.Method)
	}
	if r.Timeout < 0 || r.Retry < 0 {
		add(file, entity, "%sstep %d: request %q timeout and retry must be >= 0", prefix, i, r.Name)
	}
}

func validateGraph(
	cfg *Config, scopeName string, scope spec.Scope, name string, g spec.Graph, add addError,
) {
	file := cfg.originOf("graph", scopeName, name)
	entity := fmt.Sprintf("graph %q (scope %q)", name, scopeName)

	switch g.Routing {
	case "", "off", "fallback", "always":
	default:
		add(file, entity, "invalid routing %q (want off, fallback, or always)", g.Routing)
	}
	if g.Root == "" {
		add(file, entity, "no root node")
	} else if _, ok := g.Nodes[g.Root]; !ok {
		add(file, entity, "root %q does not exist", g.Root)
	}
	if len(g.Nodes) == 0 {
		add(file, entity, "no nodes")
		return
	}
	if g.Fallback != "" {
		if _, ok := g.Nodes[g.Fallback]; !ok {
			add(file, entity, "fallback %q does not exist", g.Fallback)
		}
	}

	for _, nodeName := range SortedKeys(g.Nodes) {
		node := g.Nodes[nodeName]
		nodeEntity := fmt.Sprintf("%s node %q", entity, nodeName)
		validateNodeRefs(scope, g, node, file, nodeEntity, add)
		defaults := 0
		for _, edge := range node.Edges {
			if edge.Intent == "" {
				defaults++
			}
			if _, ok := g.Nodes[edge.Goto]; !ok {
				add(file, nodeEntity, "edge goes to unknown node %q", edge.Goto)
			}
		}
		if defaults > 1 {
			add(file, nodeEntity, "has %d default edges; at most one is allowed", defaults)
		}
	}

	if g.Root != "" {
		seen := reachable(g)
		for _, nodeName := range SortedKeys(g.Nodes) {
			if !seen[nodeName] {
				add(file, entity, "node %q is unreachable from root %q", nodeName, g.Root)
			}
		}
	}
}

func validateNodeRefs(
	scope spec.Scope, g spec.Graph, node spec.Node, file, entity string, add addError,
) {
	for _, group := range []struct {
		label  string
		values []string
	}{{"on_enter", node.OnEnter}, {"on_exit", node.OnExit}} {
		for _, ref := range group.values {
			if _, reason := lookupAction(scope, ref); reason != "" {
				add(file, entity, "%s action %q: %s", group.label, ref, reason)
			}
		}
	}
	for _, tool := range node.Tools {
		action, reason := lookupAction(scope, tool)
		if reason != "" {
			add(file, entity, "tool %q: %s", tool, reason)
			continue
		}
		for _, in := range SortedKeys(action.Inputs) {
			input := action.Inputs[in]
			if input.Bind == "" {
				continue
			}
			kind, varName, ok := splitBind(input.Bind)
			if !ok {
				continue // reported by validateAction
			}
			declared := false
			switch kind {
			case "node":
				_, declared = node.InitialContext[varName]
			case "graph":
				_, declared = g.InitialContext[varName]
			}
			if !declared {
				add(file, entity,
					"input %q of tool %q binds %s, not declared in initial_context",
					in, tool, input.Bind)
			}
		}
	}
}

func validateBot(
	cfg *Config, catalog *channels.Catalog, scopeName string, scope spec.Scope,
	name string, b spec.Bot, add addError,
) {
	file := cfg.originOf("bot", scopeName, name)
	entity := fmt.Sprintf("bot %q (scope %q)", name, scopeName)
	if b.Graph == "" {
		add(file, entity, "no graph")
	} else if _, reason := lookupGraph(scope, b.Graph); reason != "" {
		add(file, entity, "graph %q: %s", b.Graph, reason)
	}
	if b.LLM != "" {
		if _, reason := lookupLLM(scope, b.LLM); reason != "" {
			add(file, entity, "llm %q: %s", b.LLM, reason)
		}
	}
	validateBotSettings(file, entity, b.Settings, add)

	def, key, err := catalog.Resolve(b.Channel)
	if err != nil {
		add(file, entity, "%v", err)
		return
	}
	validateBotAdapterSettings(file, entity, b, def, key, add)
	validateBotCapabilities(scope, b, def, key, file, entity, add)
}

// validateBotSettings checks the settings every channel shares; an
// adapter-owned key is reported by validateBotAdapterSettings.
func validateBotSettings(file, entity string, s *spec.BotSettings, add addError) {
	if s == nil {
		return
	}
	for _, v := range []struct {
		field string
		value int
	}{
		{"queue_delay", s.QueueDelay},
		{"jitter", s.Jitter},
		{"max_messages_per_minute", s.MaxMsgPerMinute},
		{"external_reply_pause", s.ExternalReplyPause},
	} {
		if v.value < 0 {
			add(file, entity, "%s must be >= 0", v.field)
		}
	}
}

// validateBotAdapterSettings hands the bot to its definition: the selector's
// options and the adapter-owned keys of the settings block are decoded and
// validated purely, with the file and entity the author can act on (ADR 0052).
func validateBotAdapterSettings(
	file, entity string, b spec.Bot, def channels.Definition, key channels.Key, add addError,
) {
	if b.Settings != nil && b.Settings.ExternalReplyPause != 0 &&
		!def.Capabilities.HasFeature(channels.FeatureExternalReply) {
		add(file, entity,
			"external_reply_pause needs channel %s/%s to observe external replies",
			key.Provider, key.Mode)
	}
	if def.Decode == nil {
		if !b.Channel.Options.IsZero() {
			add(file, entity, "channel %s/%s accepts no options", key.Provider, key.Mode)
		}
		return
	}
	if _, reasons := def.Decode(b); len(reasons) > 0 {
		for _, reason := range reasons {
			add(file, entity, "%s", reason)
		}
	}
}

// validateBotCapabilities checks every action the bot's graph can reach
// against what its installed definition declares, in the context of this bot:
// one shared action may fit a capable channel and not a text-only one
// (ADR 0052).
func validateBotCapabilities(
	scope spec.Scope, b spec.Bot, def channels.Definition, key channels.Key,
	file, entity string, add addError,
) {
	graph, ok := scope.Graphs[b.Graph]
	if !ok {
		return
	}
	validate := def.ValidateContent
	if validate == nil {
		validate = channels.ValidateDeclaredContent(def.Capabilities)
	}
	seen := map[string]bool{}
	check := func(actionName string) {
		if actionName == "" || seen[actionName] {
			return
		}
		seen[actionName] = true
		action, ok := scope.Actions[actionName]
		if !ok {
			return
		}
		for _, reason := range validate(key, actionName, action) {
			add(file, entity, "%s", reason)
		}
	}
	for _, nodeName := range SortedKeys(graph.Nodes) {
		node := graph.Nodes[nodeName]
		for _, name := range node.OnEnter {
			check(name)
		}
		for _, name := range node.OnExit {
			check(name)
		}
		for _, name := range node.Tools {
			check(name)
		}
	}
}

func lookupAction(scope spec.Scope, name string) (spec.Action, string) {
	a, ok := scope.Actions[name]
	if !ok {
		return spec.Action{}, "not found"
	}
	return a, ""
}

func lookupGraph(scope spec.Scope, name string) (spec.Graph, string) {
	g, ok := scope.Graphs[name]
	if !ok {
		return spec.Graph{}, "not found"
	}
	return g, ""
}

func lookupLLM(scope spec.Scope, name string) (spec.LLM, string) {
	l, ok := scope.LLMs[name]
	if !ok {
		return spec.LLM{}, "not found"
	}
	return l, ""
}

func splitBind(bind string) (kind, name string, ok bool) {
	kind, name, found := strings.Cut(bind, ".")
	if !found || name == "" || strings.Contains(name, ".") {
		return "", "", false
	}
	if kind != "node" && kind != "graph" {
		return "", "", false
	}
	return kind, name, true
}

func reachable(g spec.Graph) map[string]bool {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		node, ok := g.Nodes[name]
		if !ok {
			return
		}
		for _, edge := range node.Edges {
			visit(edge.Goto)
		}
	}
	if g.Root != "" {
		visit(g.Root)
	}
	return seen
}
