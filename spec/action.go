package spec

// Action is a reusable pipeline of steps, optionally exposed as a tool.
type Action struct {
	// DisplayName is an optional label for people-facing interfaces.
	DisplayName string           `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Inputs      map[string]Input `yaml:"inputs" json:"inputs,omitempty"`
	Guards      *Guards          `yaml:"guards" json:"guards,omitempty"`
	Steps       []Step           `yaml:"steps" json:"steps"`
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

// Cooldown blocks the same contact for `duration` seconds, then runs the
// `on_blocked` steps.
type Cooldown struct {
	Duration  int    `yaml:"duration" json:"duration"`
	OnBlocked []Step `yaml:"on_blocked" json:"on_blocked,omitempty"`
}

// RateLimit allows at most `limit` runs per contact in `window` seconds and
// runs the `on_blocked` steps when the contact exceeds it.
type RateLimit struct {
	Limit     int    `yaml:"limit" json:"limit"`
	Window    int    `yaml:"window" json:"window"`
	OnBlocked []Step `yaml:"on_blocked" json:"on_blocked,omitempty"`
}

// Step is exactly one of message, request, location, media, contact, or poll.
type Step struct {
	Message  *Message  `yaml:"message" json:"message,omitempty"`
	Request  *Request  `yaml:"request" json:"request,omitempty"`
	Location *Location `yaml:"location" json:"location,omitempty"`
	Media    *Media    `yaml:"media" json:"media,omitempty"`
	Contact  *Contact  `yaml:"contact" json:"contact,omitempty"`
	Poll     *Poll     `yaml:"poll" json:"poll,omitempty"`
}

// Message is a reply: a fixed body, an LLM-realized reply, or both. To is an
// optional recipient override, a phone number or a template; empty means the
// contact.
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

// Media declares an outbound attachment. Kind is image, video, audio,
// document, or sticker. Source it from a templated url or a local path; the
// channel reads the bytes. Name is the file name (documents), Caption the text
// shown with it, and MIME overrides the content type.
type Media struct {
	Kind    string `yaml:"kind" json:"kind"`
	URL     string `yaml:"url" json:"url,omitempty"`
	Path    string `yaml:"path" json:"path,omitempty"`
	Name    string `yaml:"name" json:"name,omitempty"`
	Caption string `yaml:"caption" json:"caption,omitempty"`
	MIME    string `yaml:"mime" json:"mime,omitempty"`
	To      string `yaml:"to" json:"to,omitempty"`
}

// Contact shares one contact card. Name and Phone are required and may be
// templates.
type Contact struct {
	Name  string `yaml:"name" json:"name"`
	Phone string `yaml:"phone" json:"phone"`
	To    string `yaml:"to" json:"to,omitempty"`
}

// Poll creates a poll. Question and at least two distinct Options are
// required; Selectable is how many a voter may pick, zero meaning one.
type Poll struct {
	Question   string   `yaml:"question" json:"question"`
	Options    []string `yaml:"options" json:"options"`
	Selectable int      `yaml:"selectable" json:"selectable,omitempty"`
	To         string   `yaml:"to" json:"to,omitempty"`
}
