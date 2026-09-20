package spec

// Action is a reusable pipeline of steps, optionally exposed as a tool.
type Action struct {
	Inputs map[string]Input `yaml:"inputs" json:"inputs,omitempty"`
	Guards *Guards          `yaml:"guards" json:"guards,omitempty"`
	Steps  []Step           `yaml:"steps" json:"steps"`
}

// Input is a named, typed argument of a tool.
type Input struct {
	Type        string `yaml:"type" json:"type,omitempty"`
	Required    bool   `yaml:"required" json:"required,omitempty"`
	Default     any    `yaml:"default" json:"default,omitempty"`
	Description string `yaml:"description" json:"description,omitempty"`
	Bind        string `yaml:"bind" json:"bind,omitempty"`
}

// Guards limit how often an action runs.
type Guards struct {
	Cooldown  *Cooldown  `yaml:"cooldown" json:"cooldown,omitempty"`
	RateLimit *RateLimit `yaml:"rate_limit" json:"rate_limit,omitempty"`
}

// Cooldown blocks the same sender for `duration` seconds, then runs the
// `on_blocked` steps.
type Cooldown struct {
	Duration  int    `yaml:"duration" json:"duration"`
	OnBlocked []Step `yaml:"on_blocked" json:"on_blocked,omitempty"`
}

// RateLimit allows at most `limit` runs per sender in `window` seconds and
// runs the `on_blocked` steps when the sender exceeds it.
type RateLimit struct {
	Limit     int    `yaml:"limit" json:"limit"`
	Window    int    `yaml:"window" json:"window"`
	OnBlocked []Step `yaml:"on_blocked" json:"on_blocked,omitempty"`
}

// Step is exactly one of message, request, or location.
type Step struct {
	Message  *Message  `yaml:"message" json:"message,omitempty"`
	Request  *Request  `yaml:"request" json:"request,omitempty"`
	Location *Location `yaml:"location" json:"location,omitempty"`
}

// Message is a reply: a fixed body, an LLM-realized reply, or both. To is an
// optional recipient override, a phone number or a template; empty means the
// sender.
type Message struct {
	Body   Text   `yaml:"body" json:"body,omitempty"`
	Intent string `yaml:"intent" json:"intent,omitempty"`
	Policy string `yaml:"policy" json:"policy,omitempty"`
	To     string `yaml:"to" json:"to,omitempty"`
}

// Request is an HTTP call whose result is addressable by its `name`. A header
// value is either a template or a secret reference, never both.
type Request struct {
	Name    string            `yaml:"name" json:"name"`
	URL     string            `yaml:"url" json:"url"`
	Method  string            `yaml:"method" json:"method,omitempty"`
	Headers map[string]Secret `yaml:"headers" json:"headers,omitempty"`
	Body    string            `yaml:"body" json:"body,omitempty"`
	Timeout int               `yaml:"timeout" json:"timeout,omitempty"`
	Retry   int               `yaml:"retry" json:"retry,omitempty"`
}

// Location is a map pin.
type Location struct {
	Latitude    float64 `yaml:"latitude" json:"latitude"`
	Longitude   float64 `yaml:"longitude" json:"longitude"`
	Name        string  `yaml:"name" json:"name,omitempty"`
	Address     string  `yaml:"address" json:"address,omitempty"`
	URL         string  `yaml:"url" json:"url,omitempty"`
	Description string  `yaml:"description" json:"description,omitempty"`
}
