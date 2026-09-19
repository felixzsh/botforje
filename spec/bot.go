package spec

// Bot binds one WhatsApp account to a graph, an optional llm, and settings.
type Bot struct {
	Graph    string       `yaml:"graph" json:"graph"`
	LLM      string       `yaml:"llm" json:"llm,omitempty"`
	Settings *BotSettings `yaml:"settings" json:"settings,omitempty"`
}

// BotSettings controls pacing, presence, and filtering (ADR 0011).
type BotSettings struct {
	ReadReceipts     *bool    `yaml:"read_receipts" json:"read_receipts,omitempty"`
	ReadReceiptDelay int      `yaml:"read_receipt_delay" json:"read_receipt_delay,omitempty"`
	ReplyDelay       int      `yaml:"reply_delay" json:"reply_delay,omitempty"`
	SimulateTyping   *bool    `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	QueueDelay       int      `yaml:"queue_delay" json:"queue_delay,omitempty"`
	Jitter           int      `yaml:"jitter" json:"jitter,omitempty"`
	MaxMsgPerMinute  int      `yaml:"max_messages_per_minute" json:"max_messages_per_minute,omitempty"`
	IgnoreGroups     *bool    `yaml:"ignore_groups" json:"ignore_groups,omitempty"`
	AllowedSenders   []string `yaml:"allowed_senders" json:"allowed_senders,omitempty"`
	IgnoredSenders   []string `yaml:"ignored_senders" json:"ignored_senders,omitempty"`
}
