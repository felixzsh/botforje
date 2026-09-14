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

// addError records one validation failure for a file and entity.
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

func (c *Config) fileOf(kind, scope, name string) string {
	return c.originOf(kind, scope, name)
}

// Validate runs the semantic checks that a JSON Schema cannot express
// (ADR 0007, ADR 0008, ADR 0010, ADR 0015, ADR 0016).
func Validate(cfg *Config) []ValidationError {
	if cfg == nil {
		return []ValidationError{{Reason: "nil config"}}
	}
	var errs []ValidationError
	add := func(file, entity, format string, args ...any) {
		errs = append(errs, ValidationError{
			File:   file,
			Entity: entity,
			Reason: fmt.Sprintf(format, args...),
		})
	}

	if cfg.Version != SchemaVersion {
		add("", "config", "unsupported version %d (want %d)", cfg.Version, SchemaVersion)
	}
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
	for _, scopeName := range sortedKeys(cfg.Scopes) {
		scope := cfg.Scopes[scopeName]
		for _, name := range sortedKeys(scope.LLMs) {
			validateLLM(cfg, scopeName, name, scope.LLMs[name], add)
		}
		for _, name := range sortedKeys(scope.Actions) {
			validateAction(cfg, scopeName, name, scope.Actions[name], add)
		}
		for _, name := range sortedKeys(scope.Graphs) {
			validateGraph(cfg, scopeName, scope, name, scope.Graphs[name], add)
		}
		for _, name := range sortedKeys(scope.Bots) {
			validateBot(cfg, scopeName, scope, name, scope.Bots[name], add)
		}
	}
	return errs
}

func validateLLM(cfg *Config, scope, name string, llm LLM, add addError) {
	file := cfg.fileOf("llm", scope, name)
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

func validateAction(cfg *Config, scope, name string, a Action, add addError) {
	file := cfg.fileOf("action", scope, name)
	entity := fmt.Sprintf("action %q (scope %q)", name, scope)

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
	validateTemplateSyntax(file, entity, a, add)
}

func validateSteps(file, entity string, steps []Step, add addError, where string) {
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

func validateMessage(file, entity, prefix string, i int, m Message, add addError) {
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
	file, entity, prefix string, i int, r Request, names map[string]bool, add addError,
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

func validateGraph(cfg *Config, scopeName string, scope Scope, name string, g Graph, add addError) {
	file := cfg.fileOf("graph", scopeName, name)
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

	for _, nodeName := range sortedKeys(g.Nodes) {
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
		for _, nodeName := range sortedKeys(g.Nodes) {
			if !seen[nodeName] {
				add(file, entity, "node %q is unreachable from root %q", nodeName, g.Root)
			}
		}
	}
}

func validateNodeRefs(scope Scope, g Graph, node Node, file, entity string, add addError) {
	graphVars := keySet(g.InitialContext)
	nodeVars := keySet(node.InitialContext)
	requests := map[string]bool{}
	for _, ref := range append(append([]string{}, node.OnEnter...), node.OnExit...) {
		if action, reason := lookupAction(scope, ref); reason == "" {
			mergeKeys(requests, requestNames(action))
		}
	}
	for _, tool := range node.Tools {
		if action, reason := lookupAction(scope, tool); reason == "" {
			mergeKeys(requests, requestNames(action))
		}
	}

	for _, group := range []struct {
		label  string
		values []string
	}{{"on_enter", node.OnEnter}, {"on_exit", node.OnExit}} {
		for _, ref := range group.values {
			action, reason := lookupAction(scope, ref)
			if reason != "" {
				add(file, entity, "%s action %q: %s", group.label, ref, reason)
				continue
			}
			validateTemplateVars(file, entity, action, nodeVars, graphVars, requests, add)
		}
	}
	for _, tool := range node.Tools {
		action, reason := lookupAction(scope, tool)
		if reason != "" {
			add(file, entity, "tool %q: %s", tool, reason)
			continue
		}
		validateTemplateVars(file, entity, action, nodeVars, graphVars, requests, add)
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
				add(file, entity,
					"input %q of tool %q binds %s, not declared in initial_context",
					in, tool, input.Bind)
			}
		}
	}
}

func validateBot(cfg *Config, scopeName string, scope Scope, name string, b Bot, add addError) {
	file := cfg.fileOf("bot", scopeName, name)
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
	if s := b.Settings; s != nil {
		for _, v := range []struct {
			field string
			value int
		}{
			{"queue_delay", s.QueueDelay},
			{"queue_jitter", s.QueueJitter},
			{"typing_delay", s.TypingDelay},
			{"max_messages_per_minute", s.MaxMsgPerMinute},
		} {
			if v.value < 0 {
				add(file, entity, "%s must be >= 0", v.field)
			}
		}
	}
}

func lookupAction(scope Scope, name string) (Action, string) {
	a, ok := scope.Actions[name]
	if !ok {
		return Action{}, "not found"
	}
	return a, ""
}

func lookupGraph(scope Scope, name string) (Graph, string) {
	g, ok := scope.Graphs[name]
	if !ok {
		return Graph{}, "not found"
	}
	return g, ""
}

func lookupLLM(scope Scope, name string) (LLM, string) {
	l, ok := scope.LLMs[name]
	if !ok {
		return LLM{}, "not found"
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
