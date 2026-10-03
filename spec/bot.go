package spec

// Bot binds one WhatsApp account to a graph, an optional llm, and settings.
type Bot struct {
	// DisplayName is an optional label for people-facing interfaces.
	DisplayName string       `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Graph       string       `yaml:"graph" json:"graph"`
	LLM         string       `yaml:"llm" json:"llm,omitempty"`
	Settings    *BotSettings `yaml:"settings" json:"settings,omitempty"`
}

// BotSettings controls pacing, presence, filtering, and history import.
type BotSettings struct {
	ReadReceipts     *bool    `yaml:"read_receipts" json:"read_receipts,omitempty"`
	ReadReceiptDelay int      `yaml:"read_receipt_delay" json:"read_receipt_delay,omitempty"`
	ReplyDelay       int      `yaml:"reply_delay" json:"reply_delay,omitempty"`
	SimulateTyping   *bool    `yaml:"simulate_typing" json:"simulate_typing,omitempty"`
	QueueDelay       int      `yaml:"queue_delay" json:"queue_delay,omitempty"`
	Jitter           int      `yaml:"jitter" json:"jitter,omitempty"`
	MaxMsgPerMinute  int      `yaml:"max_messages_per_minute" json:"max_messages_per_minute,omitempty"`
	IgnoreGroups     *bool    `yaml:"ignore_groups" json:"ignore_groups,omitempty"`
	AllowedContacts  []string `yaml:"allowed_contacts" json:"allowed_contacts,omitempty"`
	IgnoredContacts  []string `yaml:"ignored_contacts" json:"ignored_contacts,omitempty"`
	// HistorySync imports the messages WhatsApp delivers when the account is
	// linked. It defaults to true; false stores none of that backlog.
	HistorySync *bool `yaml:"history_sync" json:"history_sync,omitempty"`
	// ImportContacts copies the linked account's address book into the
	// instance's own contacts, keyed by (scope, bot, phone). It defaults to
	// false: the book may hold people this account never wrote to, and it can
	// overlap with the address book of a personal account on the same phone.
	ImportContacts *bool `yaml:"import_contacts" json:"import_contacts,omitempty"`
	// ExternalReplyPause is the milliseconds one chat stays handed to a human
	// after a message leaves the linked account without Botflota sending it.
	// Zero or absent uses the 15-minute default; every chat of the bot shares
	// the value.
	ExternalReplyPause int `yaml:"external_reply_pause" json:"external_reply_pause,omitempty"`
}
