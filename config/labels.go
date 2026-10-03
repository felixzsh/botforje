package config

import (
	"fmt"
	"strings"
)

// EffectiveLabel is the label a person reads for an entry: its display name
// when it says something, otherwise its YAML key (ADR 0016). It is not an
// identity: two entries can never share an effective label, but changing one
// never changes a key, a reference, or stored runtime identity.
func EffectiveLabel(displayName, key string) string {
	if strings.TrimSpace(displayName) == "" {
		return key
	}
	return displayName
}

// normalizedLabel is how labels are compared. Surrounding space is dropped,
// inner runs collapse to one space, and case is ignored, so " Sales " and
// "sales" cannot both exist where a person would read them as the same name.
// Comparison is the only thing normalized: the saved label keeps its text.
func normalizedLabel(label string) string {
	return strings.ToLower(strings.Join(strings.Fields(label), " "))
}

// validateLabels rejects two entries that a person would read as the same name
// in one UI namespace: scopes across the instance, each entity category within
// a scope, and nodes within a graph. Equal labels in different categories or
// different scopes stay valid, and a missing label is compared as its key, so it
// cannot collide with an explicitly named entry unnoticed (ADR 0016). Every
// finding belongs to one scope, and a finding about scopes never names the
// other scope: an error may not disclose one to a caller of another
// (docs/api.md).
func validateLabels(cfg *Config, addScope func(string) addError) {
	scopeNames := make([]string, 0, len(cfg.Scopes))
	for _, name := range SortedKeys(cfg.Scopes) {
		scopeNames = append(scopeNames, name)
	}
	checkLabels(cfg, addScope, labelNamespace{kind: "scope"}, scopeNames,
		func(name string) string { return cfg.Scopes[name].DisplayName })

	for _, scopeName := range scopeNames {
		scope := cfg.Scopes[scopeName]
		checkLabels(cfg, addScope, labelNamespace{kind: "llm", scope: scopeName},
			SortedKeys(scope.LLMs),
			func(name string) string { return scope.LLMs[name].DisplayName })
		checkLabels(cfg, addScope, labelNamespace{kind: "action", scope: scopeName},
			SortedKeys(scope.Actions),
			func(name string) string { return scope.Actions[name].DisplayName })
		checkLabels(cfg, addScope, labelNamespace{kind: "graph", scope: scopeName},
			SortedKeys(scope.Graphs),
			func(name string) string { return scope.Graphs[name].DisplayName })
		checkLabels(cfg, addScope, labelNamespace{kind: "bot", scope: scopeName},
			SortedKeys(scope.Bots),
			func(name string) string { return scope.Bots[name].DisplayName })
		for _, graphName := range SortedKeys(scope.Graphs) {
			graph := scope.Graphs[graphName]
			checkLabels(cfg, addScope, labelNamespace{
				kind: "node", scope: scopeName, graph: graphName,
			}, SortedKeys(graph.Nodes),
				func(name string) string { return graph.Nodes[name].DisplayName })
		}
	}
}

// labelNamespace is where labels must be unique, and how a finding names one:
// a scope, an entity category inside a scope, or the nodes of one graph.
type labelNamespace struct {
	kind  string
	scope string
	graph string
}

// checkLabels reports every entry whose effective label another entry in the
// same namespace already uses. Names come sorted, so the finding is stable: the
// first key in order keeps the label and the later one is refused.
func checkLabels(
	cfg *Config, addScope func(string) addError, where labelNamespace,
	names []string, displayName func(string) string,
) {
	seen := make(map[string]string, len(names))
	for _, name := range names {
		label := EffectiveLabel(displayName(name), name)
		first, taken := seen[normalizedLabel(label)]
		if !taken {
			seen[normalizedLabel(label)] = name
			continue
		}
		// The scope namespace spans the instance, so the entry that holds the
		// label is in another scope and is not named here.
		if where.kind == "scope" {
			addScope(name)(cfg.originOf(where.kind, where.scope, name),
				where.entity(name), "label %q is already used", label)
			continue
		}
		addScope(where.scope)(cfg.originOf(where.kind, where.scope, name),
			where.entity(name), "label %q is already used by %q", label, first)
	}
}

// entity names one entry the way the rest of validation does.
func (w labelNamespace) entity(name string) string {
	switch {
	case w.kind == "scope":
		return fmt.Sprintf("scope %q", name)
	case w.graph != "":
		return fmt.Sprintf("graph %q (scope %q) node %q", w.graph, w.scope, name)
	default:
		return fmt.Sprintf("%s %q (scope %q)", w.kind, name, w.scope)
	}
}
