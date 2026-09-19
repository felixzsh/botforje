package config

import (
	"fmt"

	"github.com/felixzsh/botflota/template"
)

// templateSite is one string in an action that may contain references.
type templateSite struct {
	where string
	text  string
}

// templateSites collects every template string of an action, including the
// cooldown's on_blocked steps.
func templateSites(a Action) []templateSite {
	var sites []templateSite
	add := func(where, text string) {
		if text != "" {
			sites = append(sites, templateSite{where: where, text: text})
		}
	}
	collect := func(prefix string, steps []Step) {
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
				for _, name := range SortedKeys(step.Request.Headers) {
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

// validateTemplateSyntax parses every template string of an action and rejects
// unknown roots and malformed paths (ADR 0018).
func validateTemplateSyntax(file, entity string, a Action, add addError) {
	for _, site := range templateSites(a) {
		if _, err := template.Parse(site.text); err != nil {
			add(file, entity, "%s: %v", site.where, err)
		}
	}
}

// validateTemplateVars resolves an action's references against the node and
// graph contexts, the request results available in the node, and the action
// inputs (ADR 0018).
func validateTemplateVars(
	file, entity string, a Action,
	nodeVars, graphVars, requests map[string]bool, add addError,
) {
	for _, site := range templateSites(a) {
		tmpl, err := template.Parse(site.text)
		if err != nil {
			continue // reported by validateTemplateSyntax
		}
		for _, ref := range tmpl.References() {
			root, segments, err := template.ParsePath(ref)
			if err != nil {
				continue
			}
			name := segments[0]
			switch root {
			case template.RootInput:
				if _, ok := a.Inputs[name]; !ok {
					add(file, entity, "%s: input %q is not declared", site.where, name)
				}
			case template.RootGraph:
				if !graphVars[name] {
					add(file, entity,
						"%s: graph.%s is not declared in initial_context", site.where, name)
				}
			case template.RootNode:
				if !nodeVars[name] && !requests[name] {
					add(file, entity,
						"%s: node.%s is not declared in initial_context", site.where, name)
				}
			}
		}
	}
}

// requestNames collects the request names an action produces, which are the
// node references its own or later actions may read.
func requestNames(a Action) map[string]bool {
	names := map[string]bool{}
	collect := func(steps []Step) {
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
