package engine

import (
	"fmt"
	"maps"
	"slices"

	"github.com/felixzsh/botflota/spec"
)

// TemplateIssue is one template problem in a scope. Action names the action
// that owns the site. Graph and Node name the node-level check, where the
// action is only referenced; in that case Action holds the referenced action.
type TemplateIssue struct {
	Action string
	Graph  string
	Node   string
	Site   string
	Err    error
}

// templateSite is one string in an action that may contain references.
type templateSite struct {
	where string
	text  string
}

// ValidateTemplates checks every template string a scope declares: syntax,
// known roots, and declared inputs, graph and node variables, and request
// results (ADR 0018). The caller adds file and entity context.
func ValidateTemplates(scope spec.Scope) []TemplateIssue {
	var issues []TemplateIssue
	for _, name := range slices.Sorted(maps.Keys(scope.Actions)) {
		for _, site := range templateSites(scope.Actions[name]) {
			if _, err := ParseTemplate(site.text); err != nil {
				issues = append(issues, TemplateIssue{Action: name, Site: site.where, Err: err})
			}
		}
	}
	for _, graphName := range slices.Sorted(maps.Keys(scope.Graphs)) {
		g := scope.Graphs[graphName]
		for _, nodeName := range slices.Sorted(maps.Keys(g.Nodes)) {
			issues = append(issues, validateNodeTemplates(scope, graphName, nodeName, g)...)
		}
	}
	return issues
}

// validateNodeTemplates resolves the references of every action a node runs,
// on entry, exit, or as a tool, against the variables the graph and node
// declare and the request results available in the node.
func validateNodeTemplates(
	scope spec.Scope, graphName, nodeName string, g spec.Graph,
) []TemplateIssue {
	node := g.Nodes[nodeName]
	graphVars := keySet(g.InitialContext)
	nodeVars := keySet(node.InitialContext)
	requests := map[string]bool{}
	for _, ref := range append(append([]string{}, node.OnEnter...), node.OnExit...) {
		if action, ok := scope.Actions[ref]; ok {
			mergeKeys(requests, requestNames(action))
		}
	}
	for _, tool := range node.Tools {
		if action, ok := scope.Actions[tool]; ok {
			mergeKeys(requests, requestNames(action))
		}
	}

	var issues []TemplateIssue
	check := func(ref string) {
		action, ok := scope.Actions[ref]
		if !ok {
			return // the unknown action or tool is reported by validation
		}
		for _, site := range templateSites(action) {
			tmpl, err := ParseTemplate(site.text)
			if err != nil {
				continue // reported by the action pass
			}
			for _, path := range tmpl.References() {
				issue := checkReference(action, path, site.where, nodeVars, graphVars, requests)
				if issue == nil {
					continue
				}
				issue.Action = ref
				issue.Graph = graphName
				issue.Node = nodeName
				issues = append(issues, *issue)
			}
		}
	}
	for _, ref := range node.OnEnter {
		check(ref)
	}
	for _, ref := range node.OnExit {
		check(ref)
	}
	for _, tool := range node.Tools {
		check(tool)
	}
	return issues
}

// checkReference returns a problem when path is not declared in the action
// inputs, the node and graph variables, or the node's request results.
func checkReference(
	action spec.Action, path, site string,
	nodeVars, graphVars, requests map[string]bool,
) *TemplateIssue {
	root, segments, err := ParseTemplatePath(path)
	if err != nil {
		return nil // reported as a syntax error
	}
	issue := &TemplateIssue{Site: site}
	name := segments[0]
	switch root {
	case RootInput:
		if _, ok := action.Inputs[name]; !ok {
			issue.Err = fmt.Errorf("input %q is not declared", name)
		}
	case RootGraph:
		if !graphVars[name] {
			issue.Err = fmt.Errorf("graph.%s is not declared in initial_context", name)
		}
	case RootNode:
		if !nodeVars[name] && !requests[name] {
			issue.Err = fmt.Errorf("node.%s is not declared in initial_context", name)
		}
	}
	if issue.Err == nil {
		return nil
	}
	return issue
}

// templateSites collects every template string of an action, including the
// cooldown's on_blocked steps.
func templateSites(a spec.Action) []templateSite {
	var sites []templateSite
	add := func(where, text string) {
		if text != "" {
			sites = append(sites, templateSite{where: where, text: text})
		}
	}
	collect := func(prefix string, steps []spec.Step) {
		for i, step := range steps {
			where := fmt.Sprintf("%s step %d", prefix, i)
			if step.Message != nil {
				for j, variant := range step.Message.Body {
					add(fmt.Sprintf("%s message body %d", where, j), variant)
				}
				add(where+" message to", step.Message.To)
			}
			if step.Request != nil {
				add(where+" request url", step.Request.URL)
				add(where+" request body", step.Request.Body)
				for _, name := range slices.Sorted(maps.Keys(step.Request.Headers)) {
					add(where+" request header "+name, step.Request.Headers[name])
				}
			}
			if step.Location != nil {
				add(where+" location name", step.Location.Name)
				add(where+" location address", step.Location.Address)
				add(where+" location url", step.Location.URL)
				add(where+" location description", step.Location.Description)
			}
		}
	}
	collect("action", a.Steps)
	if a.Guards != nil && a.Guards.Cooldown != nil {
		collect("action on_blocked", a.Guards.Cooldown.OnBlocked)
	}
	if a.Guards != nil && a.Guards.RateLimit != nil {
		collect("action rate_limit on_blocked", a.Guards.RateLimit.OnBlocked)
	}
	return sites
}

// requestNames collects the request names an action produces, which are the
// node references its own or later actions may read.
func requestNames(a spec.Action) map[string]bool {
	names := map[string]bool{}
	collect := func(steps []spec.Step) {
		for _, step := range steps {
			if step.Request != nil && step.Request.Name != "" {
				names[step.Request.Name] = true
			}
		}
	}
	collect(a.Steps)
	if a.Guards != nil && a.Guards.Cooldown != nil {
		collect(a.Guards.Cooldown.OnBlocked)
	}
	if a.Guards != nil && a.Guards.RateLimit != nil {
		collect(a.Guards.RateLimit.OnBlocked)
	}
	return names
}

func keySet[V any](values map[string]V) map[string]bool {
	set := make(map[string]bool, len(values))
	for key := range values {
		set[key] = true
	}
	return set
}

func mergeKeys(dst, src map[string]bool) {
	for key := range src {
		dst[key] = true
	}
}
