package config

import (
	"fmt"
	"strings"
)

// ValidationError names the file, the entity, and the reason (Phase 1 gate).
type ValidationError struct {
	File   string
	Entity string
	Reason string
}

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

func (c *Config) fileOf(kind, scope, name string) string {
	return c.originOf(kind, scope, name)
}

// Validate runs the semantic checks that a JSON Schema cannot express
// (ADR 0007, ADR 0008, ADR 0010, ADR 0015).
func Validate(cfg *Config) []ValidationError {
	if cfg == nil {
		return []ValidationError{{Reason: "nil config"}}
	}
	var errs []ValidationError
	add := func(file, entity, format string, args ...any) {
		errs = append(errs, ValidationError{File: file, Entity: entity, Reason: fmt.Sprintf(format, args...)})
	}

	if cfg.Version != SchemaVersion {
		add("", "config", "unsupported version %d (want %d)", cfg.Version, SchemaVersion)
	}
	switch cfg.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		add("", "config", "invalid log_level %q", cfg.LogLevel)
	}
	if cfg.LLM != nil {
		validateLLM(cfg.LLM, add)
	}
	for _, name := range sortedKeys(cfg.Actions) {
		validateAction(cfg, name, cfg.Actions[name], add)
	}
	for _, name := range sortedKeys(cfg.Graphs) {
		validateGraph(cfg, name, cfg.Graphs[name], add)
	}
	for _, name := range sortedKeys(cfg.Bots) {
		validateBot(cfg, name, cfg.Bots[name], add)
	}
	return errs
}

func validateLLM(llm *LLM, add func(file, entity, format string, args ...any)) {
	switch llm.Routing {
	case "", "off", "fallback", "always":
	default:
		add("", "llm", "invalid routing %q (want off, fallback, or always)", llm.Routing)
	}
	switch llm.Realization {
	case "", "off", "auto":
	default:
		add("", "llm", "invalid realization %q (want off or auto)", llm.Realization)
	}
	if llm.Agent != nil {
		if llm.Agent.MaxToolsPerMessage < 0 {
			add("", "llm.agent", "max_tools_per_message must be >= 0")
		}
		if llm.Agent.Timeout < 0 {
			add("", "llm.agent", "timeout must be >= 0")
		}
	}
}

var inputTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true}
var httpMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true,
	"PATCH": true, "HEAD": true, "OPTIONS": true,
}

func validateAction(cfg *Config, name string, a Action, add func(file, entity, format string, args ...any)) {
	file := cfg.fileOf("action", a.Scope, name)
	entity := fmt.Sprintf("action %q (scope %q)", name, scopeOf(a.Scope))

	for _, in := range sortedKeys(a.Inputs) {
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
	if a.Guards != nil && a.Guards.Cooldown != nil {
		cd := a.Guards.Cooldown
		if cd.Duration <= 0 {
			add(file, entity, "cooldown duration must be > 0")
		}
		validateSteps(file, entity, cd.OnBlocked, add, "cooldown.on_blocked")
	}
}

func validateSteps(file, entity string, steps []Step, add func(string, string, string, ...any), where string) {
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
		if count != 1 {
			add(file, entity, "%sstep %d must be exactly one of message, request, or location", prefix, i)
		}
	}
}

func validateMessage(file, entity, prefix string, i int, m Message, add func(string, string, string, ...any)) {
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

func validateRequest(file, entity, prefix string, i int, r Request, names map[string]bool, add func(string, string, string, ...any)) {
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

func validateGraph(cfg *Config, name string, g Graph, add func(file, entity, format string, args ...any)) {
	file := cfg.fileOf("graph", g.Scope, name)
	entity := fmt.Sprintf("graph %q (scope %q)", name, scopeOf(g.Scope))

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

	for _, nodeName := range sortedKeys(g.Nodes) {
		node := g.Nodes[nodeName]
		nodeEntity := fmt.Sprintf("%s node %q", entity, nodeName)
		validateNodeRefs(cfg, g, node, file, nodeEntity, add)
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
		for _, nodeName := range sortedKeys(g.Nodes) {
			if !seen[nodeName] {
				add(file, entity, "node %q is unreachable from root %q", nodeName, g.Root)
			}
		}
	}
}

func validateNodeRefs(cfg *Config, g Graph, node Node, file, entity string, add func(string, string, string, ...any)) {
	for _, group := range []struct {
		label  string
		values []string
	}{{"on_enter", node.OnEnter}, {"on_exit", node.OnExit}} {
		for _, ref := range group.values {
			if _, reason := lookupAction(cfg, g.Scope, ref); reason != "" {
				add(file, entity, "%s action %q: %s", group.label, ref, reason)
			}
		}
	}
	for _, tool := range node.Tools {
		action, reason := lookupAction(cfg, g.Scope, tool)
		if reason != "" {
			add(file, entity, "tool %q: %s", tool, reason)
			continue
		}
		for _, in := range sortedKeys(action.Inputs) {
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
				add(file, entity, "input %q of tool %q binds %s, not declared in initial_context", in, tool, input.Bind)
			}
		}
	}
}

func validateBot(cfg *Config, name string, b Bot, add func(file, entity, format string, args ...any)) {
	file := cfg.fileOf("bot", b.Scope, name)
	entity := fmt.Sprintf("bot %q (scope %q)", name, scopeOf(b.Scope))
	if b.Graph == "" {
		add(file, entity, "no graph")
	} else if _, reason := lookupGraph(cfg, b.Scope, b.Graph); reason != "" {
		add(file, entity, "graph %q: %s", b.Graph, reason)
	}
	if s := b.Settings; s != nil {
		for _, v := range []struct {
			field string
			value int
		}{
			{"queue_delay", s.QueueDelay},
			{"queue_jitter", s.QueueJitter},
			{"typing_delay", s.TypingDelay},
			{"max_messages_per_minute", s.MaxMessagesPerMinute},
		} {
			if v.value < 0 {
				add(file, entity, "%s must be >= 0", v.field)
			}
		}
	}
}

func (c *Config) actionInScope(scope, name string) (Action, string) {
	a, ok := c.Actions[name]
	if !ok {
		return Action{}, "not found"
	}
	if scopeOf(a.Scope) != scopeOf(scope) {
		return a, fmt.Sprintf("defined in scope %q, not %q", scopeOf(a.Scope), scopeOf(scope))
	}
	return a, ""
}

func (c *Config) graphInScope(scope, name string) (Graph, string) {
	g, ok := c.Graphs[name]
	if !ok {
		return Graph{}, "not found"
	}
	if scopeOf(g.Scope) != scopeOf(scope) {
		return g, fmt.Sprintf("defined in scope %q, not %q", scopeOf(g.Scope), scopeOf(scope))
	}
	return g, ""
}

func lookupAction(cfg *Config, scope, name string) (Action, string) {
	return cfg.actionInScope(scope, name)
}
func lookupGraph(cfg *Config, scope, name string) (Graph, string) {
	return cfg.graphInScope(scope, name)
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

func reachable(g Graph) map[string]bool {
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
