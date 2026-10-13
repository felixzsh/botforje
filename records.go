package botflota

import "time"

// Persisted shapes: one struct per table, mirroring the columns the SQLite
// adapter reads and writes. Keys, constraints, and encoding live with the DDL
// in adapters/sqlite (ADR 0006, ADR 0026); the ports that read and write them
// live in ports.go.

// BotRef names one bot as its scope and bot key together, never a bare name
// (ADR 0016). It is comparable so registries can key on the pair without
// concatenating it: scope and bot names are free strings and may each contain
// any separator a string join would make ambiguous.
type BotRef struct {
	Scope string
	Bot   string
}

// Binding is a bot's committed association with its messaging account: the
// neutral metadata the fleet inspects and the normalized own identity the
// adapter mapped (ADR 0051, ADR 0053). Provider names the catalog provider,
// Adapter the adapter kind, and Mode the account mode the bot selected; the
// three identify the installed implementation, never a bare provider string.
// Namespace carries the stable identity-space context when the provider's
// ids are local to one store or profile, and stays empty for provider-wide
// ids the adapter needs no space for. Generation starts at one and advances
// on every identity replacement; renewing the same identity keeps it. The
// SDK device locator and the credentials stay in adapter-owned storage; the
// binding records the association, never a copy of their schema.
type Binding struct {
	Scope      string
	Bot        string
	Provider   string
	Adapter    string
	Mode       string
	Namespace  string
	Identity   IdentityKey
	Generation int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Peer is one person's durable identity in a scope (ADR 0029). Key is the
// comparable provider identity; knowing a peer never requires a message
// exchange. DisplayName is a best-effort push name: possibly empty, never an
// identifier. FirstSeen and LastSeen are the first and last live inbound
// message; both stay zero when the identity was first known through the
// directory or an API send.
type Peer struct {
	ID            int64
	Scope         string
	Key           IdentityKey
	DisplayName   string
	NameUpdatedAt time.Time
	FirstSeen     time.Time
	LastSeen      time.Time
	// These properties are derived from the bot-owned directory and chat
	// history; they are never stored on the identity row.
	AddressBookImported bool
	HasChat             bool
	HasHistory          bool
}

// DirectoryEntry is one entry of a bot's imported account directory
// (ADR 0037, ADR 0051): PeerKey identifies the peer and SourceIdentity the
// normalized account that supplied the snapshot, both identities of the same
// provider. (Scope, Bot) identifies the current owner of the stored data, not
// necessarily the account that originally imported it, and a move preserves
// SourceIdentity (ADR 0037/0042). DisplayName is already normalized by the
// adapter; membership proves neither current reachability nor send permission.
type DirectoryEntry struct {
	Scope          string
	Bot            string
	PeerKey        IdentityKey
	SourceIdentity IdentityKey
	DisplayName    string
	UpdatedAt      time.Time
}

// Chat is the durable thread between one bot and one conversation; it never
// ends (ADR 0026). Target is the complete conversation address, and PeerID is
// the known participant, zero when the conversation has no known one. A direct
// thread does not prove its participant, and a group is not one person's
// identity (ADR 0051). MessageCount is the live inbound count, not the stored
// history; Guards is the per-action guard state (ADR 0027).
type Chat struct {
	ID           int64
	Scope        string
	Bot          string
	Target       ChatTarget
	PeerID       int64
	StartedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int64
	Guards       map[string]GuardState
}

type Direction string

const (
	Inbound  Direction = "in"
	Outbound Direction = "out"
)

// Ingest is how a stored message entered Botflota. Unknown covers rows from
// before the distinction existed.
type Ingest string

const (
	IngestLive    Ingest = "live"
	IngestSync    Ingest = "sync"
	IngestAPI     Ingest = "api"
	IngestUnknown Ingest = "unknown"
)

// MessageOrigin is who produced an outbound message: empty for an inbound
// one, unknown and never guessed for an imported one (ADR 0026).
type MessageOrigin string

const (
	OriginPreset   MessageOrigin = "preset"
	OriginLLM      MessageOrigin = "llm"
	OriginOperator MessageOrigin = "operator"
	// OriginAPI is a message the API asked a bot to deliver outside a turn
	// (ADR 0044); it is not a human reply.
	OriginAPI     MessageOrigin = "api"
	OriginUnknown MessageOrigin = "unknown"
)

// RoutingMethod is how the engine chose the transition that handled an inbound
// message (ADR 0010, ADR 0019). Unknown covers an imported row or a run where
// no edge applied.
type RoutingMethod string

const (
	RoutingUnknown         RoutingMethod = "unknown"
	RoutingRoot            RoutingMethod = "root"
	RoutingTriggerExact    RoutingMethod = "trigger_exact"
	RoutingTriggerSequence RoutingMethod = "trigger_sequence"
	RoutingTriggerWords    RoutingMethod = "trigger_words"
	RoutingTriggerFuzzy    RoutingMethod = "trigger_fuzzy"
	RoutingSystemOne       RoutingMethod = "system_one"
	RoutingDefault         RoutingMethod = "default"
	RoutingFallback        RoutingMethod = "fallback"
)

// Message is one message kept for history (ADR 0026). The store derives the
// kind and visible text columns from Content (ADR 0036). SessionID is zero
// when no session run handled it. Target is the destination the message
// actually belongs to, which for an alternate recipient differs from the
// triggering chat, and ProviderMessageID is the channel's id, scoped by that
// destination for dedup (ADR 0051).
type Message struct {
	ID                int64
	ChatID            int64
	SessionID         int64
	Target            ChatTarget
	Direction         Direction
	Content           MessageContent
	At                time.Time
	ProviderMessageID string
	Ingest            Ingest
	Origin            MessageOrigin
	Routing           RoutingMethod
}

// HistoryCounts is what a history operation removed or moved: one bot's chats
// and their cascaded sessions, messages, and queued replies, plus its
// directory. The counts are a report of the operation; freeing storage is not
// part of it, and docs/access-policy.md owns the resource policy (ADR 0042).
type HistoryCounts struct {
	Chats     int64
	GraphRuns int64
	Messages  int64
	Directory int64
}

// HistoryChat is one chat as an export reads it, with its peer's identity
// beside it so the exported file stands alone (ADR 0042).
type HistoryChat struct {
	Chat
	PeerKey  IdentityKey
	PeerName string
}

// ChatControlMode is the effective automation of one chat (ADR 0043). It is
// live runtime state, never a stored column.
type ChatControlMode string

const (
	ChatAuto   ChatControlMode = "auto"
	ChatPaused ChatControlMode = "paused"
	ChatHuman  ChatControlMode = "human"
)

// ChatControl reports one chat's live control state for the read API: the
// effective mode, and the automatic pause deadline while it is counting down.
type ChatControl struct {
	Mode       ChatControlMode
	PausedTill time.Time
}

// ChatSummary is a chat joined with its peer identity, for the read API.
// Messages, Sync, and Live count the stored rows by ingest kind; the embedded
// MessageCount stays the live inbound count (ADR 0035).
type ChatSummary struct {
	Chat
	PeerKey     IdentityKey
	DisplayName string
	Messages    int64
	Sync        int64
	Live        int64
}

// Session is one bounded run of the graph for a chat; a chat has many over
// time (ADR 0026).
type Session struct {
	ID           int64
	ChatID       int64
	Node         string
	VisitedNodes []string
	GraphContext map[string]any
	NodeContext  map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ExpiresAt    time.Time
}

// Expired reports whether the session is over at t; a zero expiry never
// expires.
func (s *Session) Expired(t time.Time) bool {
	return !s.ExpiresAt.IsZero() && !t.Before(s.ExpiresAt)
}

// GuardState is one action's guard state, stored in the chat keyed by action
// name (ADR 0027).
type GuardState struct {
	Cooldown  *CooldownState  `json:"cooldown,omitempty"`
	RateLimit *RateLimitState `json:"rate_limit,omitempty"`
}

type CooldownState struct {
	LastRun time.Time `json:"last_run"`
}

type RateLimitState struct {
	Count       int       `json:"count"`
	WindowStart time.Time `json:"window_start"`
}

type PairingState string

const (
	PairingPaired    PairingState = "paired"
	PairingNotPaired PairingState = "not paired"
	PairingUnpaired  PairingState = "unpaired"
)

// PairingStatus records the last confirmed transition of a bot's pairing.
type PairingStatus struct {
	Scope      string
	Bot        string
	State      PairingState
	LastChange time.Time
}

// OwnerToken is the instance owner's credential (ADR 0025). Only the hash is
// stored; the clear token is written once to a 0600 file under data_dir and
// never kept here.
type OwnerToken struct {
	Hash      []byte
	CreatedAt time.Time
}

// AccessToken is a scoped bearer credential for the API (ROADMAP Phase 13),
// hash only. An empty ExpiresAt never expires; an empty RevokedAt is active.
type AccessToken struct {
	ID        int64
	Name      string
	Hash      []byte
	Scopes    []string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt time.Time
}

func (t *AccessToken) Active(at time.Time) bool {
	if !t.RevokedAt.IsZero() {
		return false
	}
	return t.ExpiresAt.IsZero() || at.Before(t.ExpiresAt)
}
