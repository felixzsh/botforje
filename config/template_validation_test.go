package config

import (
	"strings"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

// templateScope is a valid scope: greet reads a graph variable, lookup captures
// a request, and report reads its result.
func templateScope() spec.Scope {
	return spec.Scope{
		Actions: map[string]spec.Action{
			"greet": {Steps: []spec.Step{{Message: &spec.Message{
				Body: spec.Text{"Hi {{graph.customer_name}}"},
			}}}},
			"lookup": {Steps: []spec.Step{{Request: &spec.Request{
				Name: "call", URL: "https://example.com", Method: "GET",
			}}}},
			"report": {Steps: []spec.Step{{Message: &spec.Message{
				Body: spec.Text{"ok={{node.call.resp.body.id}}"},
			}}}},
		},
		Graphs: map[string]spec.Graph{
			"g": {
				Root:           "start",
				InitialContext: map[string]any{"customer_name": ""},
				Nodes: map[string]spec.Node{
					"start": {OnEnter: []string{"greet", "lookup", "report"}},
				},
			},
		},
	}
}

func setTemplateBody(s *spec.Scope, action, body string) {
	a := s.Actions[action]
	a.Steps = []spec.Step{{Message: &spec.Message{Body: spec.Text{body}}}}
	s.Actions[action] = a
}

func addOnEnter(s *spec.Scope, action string) {
	g := s.Graphs["g"]
	node := g.Nodes["start"]
	node.OnEnter = append(node.OnEnter, action)
	g.Nodes["start"] = node
	s.Graphs["g"] = g
}

func TestValidateTemplates(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*spec.Scope)
		want   string
	}{
		{"valid does not report", func(*spec.Scope) {}, ""},
		{"unknown root", func(s *spec.Scope) {
			setTemplateBody(s, "greet", "{{secret.api_key}}")
		}, "unknown root"},
		{"unterminated reference", func(s *spec.Scope) {
			setTemplateBody(s, "greet", "open {{graph.customer_name")
		}, "unterminated"},
		{"undeclared graph variable", func(s *spec.Scope) {
			setTemplateBody(s, "greet", "{{graph.missing}}")
		}, "graph.missing is not declared"},
		{"undeclared node variable", func(s *spec.Scope) {
			setTemplateBody(s, "greet", "{{node.missing}}")
		}, "node.missing is not declared"},
		{"request result is declared", func(s *spec.Scope) {
			// The base scope already reads node.call, captured by lookup.
		}, ""},
		{"undeclared request result", func(s *spec.Scope) {
			setTemplateBody(s, "report", "{{node.other.resp.body.id}}")
		}, "node.other is not declared"},
		{"undeclared input", func(s *spec.Scope) {
			s.Actions["search"] = spec.Action{
				Inputs: map[string]spec.Input{"query": {Type: "string"}},
				Steps: []spec.Step{{Message: &spec.Message{
					Body: spec.Text{"{{input.other}}"},
				}}},
			}
			addOnEnter(s, "search")
		}, `input "other" is not declared`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope := templateScope()
			tc.mutate(&scope)
			issues := validateTemplates(scope, legacyTemplates{})
			if tc.want == "" {
				if len(issues) != 0 {
					t.Fatalf("issues = %#v, want none", issues)
				}
				return
			}
			for _, issue := range issues {
				if strings.Contains(issue.Err.Error(), tc.want) {
					return
				}
			}
			t.Fatalf("no issue mentions %q; got %#v", tc.want, issues)
		})
	}
}

// TestValidateTemplatesContext checks the fields config needs to attribute a
// finding to the file and entity that declared it.
func TestValidateTemplatesContext(t *testing.T) {
	scope := templateScope()
	setTemplateBody(&scope, "greet", "{{node.missing}}")
	issues := validateTemplates(scope, legacyTemplates{})
	if len(issues) != 1 {
		t.Fatalf("issues = %#v, want exactly one", issues)
	}
	issue := issues[0]
	if issue.Action != "greet" || issue.Graph != "g" || issue.Node != "start" {
		t.Fatalf("issue context = %#v", issue)
	}
	if issue.Site == "" {
		t.Fatal("issue has no site")
	}
	if issue.Err == nil {
		t.Fatal("issue has no error")
	}
}
