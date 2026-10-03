package config

import (
	"strings"
	"testing"

	"github.com/felixzsh/botflota/spec"
)

func labelsOf(t *testing.T, cfg *Config) []string {
	t.Helper()
	problems := Validate(cfg)
	labels := make([]string, 0, len(problems))
	for _, problem := range problems {
		if strings.Contains(problem.Reason, "already used") {
			labels = append(labels, problem.Error())
		}
	}
	return labels
}

// graphWithNode is a valid graph, so a label test never trips another check.
func graphWithNode(root, displayName string) spec.Graph {
	return spec.Graph{
		DisplayName: displayName,
		Root:        root,
		Nodes:       map[string]spec.Node{root: {Goal: "greet"}},
	}
}

func TestLabelsAreUniquePerNamespace(t *testing.T) {
	cases := []struct {
		name      string
		cfg       *Config
		wantCount int
	}{
		{
			name: "equal scope labels collide",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {DisplayName: "Sales", Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
				"two": {DisplayName: "Sales", Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
			}},
			wantCount: 1,
		},
		{
			name: "a missing scope label collides with an explicit name",
			cfg: &Config{Scopes: map[string]spec.Scope{
				// The first key is the label "support"; the second names itself
				// "support" too, so a person reads one name twice.
				"support": {Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
				"other":   {DisplayName: "support", Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
			}},
			wantCount: 1,
		},
		{
			name: "case and spacing do not make a label distinct",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {DisplayName: "Sales", Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
				"two": {DisplayName: "  sALes ", Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
			}},
			wantCount: 1,
		},
		{
			name: "equal labels in different scopes are fine",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {
					DisplayName: "One",
					Bots:        map[string]spec.Bot{"assistant": {Graph: "g"}},
					Graphs:      map[string]spec.Graph{"g": graphWithNode("a", "")},
				},
				"two": {
					DisplayName: "Two",
					Bots:        map[string]spec.Bot{"assistant": {Graph: "g"}},
					Graphs:      map[string]spec.Graph{"g": graphWithNode("a", "")},
				},
			}},
			wantCount: 0,
		},
		{
			name: "equal labels in different categories are fine",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {
					Bots:   map[string]spec.Bot{"same": {DisplayName: "Same", Graph: "g"}},
					Graphs: map[string]spec.Graph{"g": graphWithNode("a", "Same")},
				},
			}},
			wantCount: 0,
		},
		{
			name: "equal bot labels in one scope collide",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {
					Bots: map[string]spec.Bot{
						"first":  {DisplayName: "Sales assistant", Graph: "g"},
						"second": {DisplayName: "sales assistant", Graph: "g"},
					},
					Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")},
				},
			}},
			wantCount: 1,
		},
		{
			name: "a bot label colliding with its own key is caught",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {
					Bots: map[string]spec.Bot{
						"desk":  {Graph: "g"},
						"other": {DisplayName: "desk", Graph: "g"},
					},
					Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")},
				},
			}},
			wantCount: 1,
		},
		{
			name: "equal node labels in one graph collide",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {Graphs: map[string]spec.Graph{"g": {
					Root: "a",
					Nodes: map[string]spec.Node{
						"a": {DisplayName: "Menu", Goal: "greet"},
						"b": {DisplayName: "menu", Goal: "greet"},
					},
				}}},
			}},
			wantCount: 1,
		},
		{
			name: "equal node labels in different graphs are fine",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {Graphs: map[string]spec.Graph{
					"g1": {
						Root: "a",
						Nodes: map[string]spec.Node{
							"a": {DisplayName: "Menu", Goal: "greet"},
						},
					},
					"g2": {
						Root: "a",
						Nodes: map[string]spec.Node{
							"a": {DisplayName: "Menu", Goal: "greet"},
						},
					},
				}},
			}},
			wantCount: 0,
		},
		{
			name: "equal llm labels in one scope collide",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {LLMs: map[string]spec.LLM{
					"primary": {DisplayName: "Main", Provider: "openai", Model: "m"},
					"backup":  {DisplayName: "main", Provider: "openai", Model: "m"},
				}},
			}},
			wantCount: 1,
		},
		{
			name: "equal action labels in one scope collide",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {Actions: map[string]spec.Action{
					"greet":  {DisplayName: "Greeting"},
					"salute": {DisplayName: "greeting"},
				}},
			}},
			wantCount: 1,
		},
		{
			name: "an empty label is the key, and a key cannot repeat",
			cfg: &Config{Scopes: map[string]spec.Scope{
				"one": {Bots: map[string]spec.Bot{
					"desk": {Graph: "g"},
				}, Graphs: map[string]spec.Graph{"g": graphWithNode("a", "")}},
			}},
			wantCount: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := labelsOf(t, tc.cfg)
			if len(got) != tc.wantCount {
				t.Fatalf("found %d label problems, want %d: %v",
					len(got), tc.wantCount, got)
			}
		})
	}
}

// TestScopeLabelCollisionNamesNoOtherScope holds the isolation promise for the
// one finding that spans scopes: the message may not name the entry in the
// other scope that holds the label (docs/api.md).
func TestScopeLabelCollisionNamesNoOtherScope(t *testing.T) {
	cfg := &Config{Scopes: map[string]spec.Scope{
		"first":  {DisplayName: "Shared"},
		"second": {DisplayName: "shared"},
	}}
	var found []ValidationError
	for _, problem := range Validate(cfg) {
		if strings.Contains(problem.Reason, "already used") {
			found = append(found, problem)
		}
	}
	if len(found) != 1 {
		t.Fatalf("found %d label problems, want 1: %v", len(found), found)
	}
	if found[0].Scope != "second" {
		t.Fatalf("scope = %q, want the finding attributed to its own scope",
			found[0].Scope)
	}
	if strings.Contains(found[0].Reason, "first") {
		t.Fatalf("reason = %q, want no other scope named", found[0].Reason)
	}
}

func TestEffectiveLabel(t *testing.T) {
	cases := []struct {
		name        string
		displayName string
		key         string
		want        string
	}{
		{"a label wins over its key", "Sales", "scope_a7f3", "Sales"},
		{"a missing label falls back to the key", "", "scope_a7f3", "scope_a7f3"},
		{"blank space is no label at all", "   ", "scope_a7f3", "scope_a7f3"},
		{"the label keeps its own spacing", " Sales desk ", "s", " Sales desk "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveLabel(tc.displayName, tc.key); got != tc.want {
				t.Fatalf("EffectiveLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLabelFindingNamesTheGraphOfANode(t *testing.T) {
	cfg := &Config{Scopes: map[string]spec.Scope{
		"one": {Graphs: map[string]spec.Graph{"menu": {
			Root: "a",
			Nodes: map[string]spec.Node{
				"a": {DisplayName: "Menu", Goal: "greet"},
				"b": {DisplayName: "menu", Goal: "greet"},
			},
		}}},
	}}
	got := labelsOf(t, cfg)
	if len(got) != 1 {
		t.Fatalf("found %d problems, want 1: %v", len(got), got)
	}
	for _, want := range []string{"graph", "menu", "node", "b", "a"} {
		if !strings.Contains(got[0], want) {
			t.Fatalf("finding %q does not name %q", got[0], want)
		}
	}
}
