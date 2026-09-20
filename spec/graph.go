package spec

// Graph is a conversation state machine.
type Graph struct {
	Root           string          `yaml:"root" json:"root"`
	Timeout        int             `yaml:"timeout" json:"timeout,omitempty"`
	Fallback       string          `yaml:"fallback" json:"fallback,omitempty"`
	Routing        string          `yaml:"routing" json:"routing,omitempty"`
	InitialContext map[string]any  `yaml:"initial_context" json:"initial_context,omitempty"`
	Nodes          map[string]Node `yaml:"nodes" json:"nodes"`
}

// Node is one state of a graph.
type Node struct {
	Goal           string         `yaml:"goal" json:"goal,omitempty"`
	Policy         string         `yaml:"policy" json:"policy,omitempty"`
	OnEnter        []string       `yaml:"on_enter" json:"on_enter,omitempty"`
	OnExit         []string       `yaml:"on_exit" json:"on_exit,omitempty"`
	InitialContext map[string]any `yaml:"initial_context" json:"initial_context,omitempty"`
	Tools          []string       `yaml:"tools" json:"tools,omitempty"`
	Edges          []Edge         `yaml:"edges" json:"edges,omitempty"`
}

// Edge is a transition. An empty Intent marks the default (catch-all) edge.
type Edge struct {
	Intent   string   `yaml:"intent" json:"intent,omitempty"`
	Triggers []string `yaml:"triggers" json:"triggers,omitempty"`
	Goto     string   `yaml:"goto" json:"goto"`
}
