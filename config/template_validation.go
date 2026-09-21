package config

import (
	"fmt"
	"maps"
	"slices"

	"github.com/felixzsh/botflota"
	"github.com/felixzsh/botflota/spec"
)

// templateIssue is one template problem in a scope. Action names the action
// that owns the site, or the referenced one for a Graph or Node check.
type templateIssue struct {
	Action string
	Graph  string
	Node   string
	Site   string
	Err    error
}

type templateSite struct {
	where string
	text  string
}

// validateTemplates checks every template string a scope declares: syntax,
// known roots, and declared inputs, graph and node variables, and request
// results (ADR 0018). The caller adds file and entity context.
func validateTemplates(scope spec.Scope) []templateIssue {
	var issues []templateIssue
	for _, name := range slices.Sorted(maps.Keys(scope.Actions)) {
		for _, site := range templateSites(scope.Actions[name]) {
			if _, err := botflota.ParseTemplate(site.text); err != nil {
				issues = append(issues, templateIssue{Action: name, Site: site.where, Err: err})
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

func validateNodeTemplates(
	scope spec.Scope, graphName, nodeName string, g spec.Graph,
) []templateIssue {
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

	var issues []templateIssue
	check := func(ref string) {
		action, ok := scope.Actions[ref]
		if !ok {
			return // the unknown action or tool is reported by validation
		}
		for _, site := range templateSites(action) {
			tmpl, err := botflota.ParseTemplate(site.text)
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

func checkReference(
	action spec.Action, path, site string,
	nodeVars, graphVars, requests map[string]bool,
) *templateIssue {
	root, segments, err := botflota.ParseTemplatePath(path)
	if err != nil {
		return nil // reported as a syntax error
	}
	issue := &templateIssue{Site: site}
	name := segments[0]
	switch root {
	case botflota.RootInput:
		if _, ok := action.Inputs[name]; !ok {
			issue.Err = fmt.Errorf("input %q is not declared", name)
		}
	case botflota.RootGraph:
		if !graphVars[name] {
			issue.Err = fmt.Errorf("graph.%s is not declared in initial_context", name)
		}
	case botflota.RootNode:
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
					header := step.Request.Headers[name]
					if header.HasRef() {
						continue // a secret reference is not a template (ADR 0014)
					}
					add(where+" request header "+name, header.Raw())
				}
			}
			if step.Location != nil {
				add(where+" location name", step.Location.Name)
				add(where+" location address", step.Location.Address)
				add(where+" location url", step.Location.URL)
				add(where+" location description", step.Location.Description)
			}
			if step.Media != nil {
				add(where+" media url", step.Media.URL)
				add(where+" media path", step.Media.Path)
				add(where+" media name", step.Media.Name)
				add(where+" media caption", step.Media.Caption)
				add(where+" media to", step.Media.To)
			}
			if step.Contact != nil {
				add(where+" contact name", step.Contact.Name)
				add(where+" contact phone", step.Contact.Phone)
				add(where+" contact to", step.Contact.To)
			}
			if step.Poll != nil {
				add(where+" poll question", step.Poll.Question)
				for j, option := range step.Poll.Options {
					add(fmt.Sprintf("%s poll option %d", where, j), option)
				}
				add(where+" poll to", step.Poll.To)
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
