package spec

// Scope is an isolation namespace holding its own entities (ADR 0016).
type Scope struct {
	LLMs    map[string]LLM    `yaml:"llms" json:"llms,omitempty"`
	Actions map[string]Action `yaml:"actions" json:"actions,omitempty"`
	Graphs  map[string]Graph  `yaml:"graphs" json:"graphs,omitempty"`
	Bots    map[string]Bot    `yaml:"bots" json:"bots,omitempty"`
}

// LLM is a named, scoped model profile referenced by a bot (ADR 0031).
type LLM struct {
	Provider    string  `yaml:"provider" json:"provider"`
	Model       string  `yaml:"model" json:"model"`
	APIKey      Secret  `yaml:"api_key" json:"api_key,omitempty"`
	BaseURL     string  `yaml:"base_url" json:"base_url,omitempty"`
	Routing     string  `yaml:"routing" json:"routing,omitempty"`
	Realization string  `yaml:"realization" json:"realization,omitempty"`
	Bounds      *Bounds `yaml:"bounds" json:"bounds,omitempty"`
}

// Bounds limits the tool loop inside a node (ADR 0033).
type Bounds struct {
	MaxToolsPerMessage int `yaml:"max_tools_per_message" json:"max_tools_per_message,omitempty"`
	Timeout            int `yaml:"timeout" json:"timeout,omitempty"`
}
