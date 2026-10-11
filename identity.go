package botflota

// IdentityKey names one comparable external identity: the provider catalog
// identifier and the adapter-normalized subject (ADR 0051). Subjects are
// opaque and case-sensitive; common code compares and persists them without
// trimming, digit validation, suffix removal, or protocol parsing. Identity
// equality is not destination equality: a comparable id is not proof that
// another authorization can reach it.
type IdentityKey struct {
	Provider string
	Subject  string
}

// Valid reports whether both required parts are present.
func (k IdentityKey) Valid() bool {
	return k.Provider != "" && k.Subject != ""
}

// ChatKind is the shape of a conversation. Representing a kind does not
// enable its runtime policy or channel support (ADR 0051).
type ChatKind string

const (
	ChatDirect ChatKind = "direct"
	ChatGroup  ChatKind = "group"
)

// Valid reports whether the kind is one of the two modeled shapes.
func (k ChatKind) Valid() bool {
	return k == ChatDirect || k == ChatGroup
}

// ChatTarget addresses one durable conversation under one provider: the
// adapter-normalized thread and, for providers that thread conversations, its
// topic. An empty topic means no topic, and it is persisted as the same
// canonical empty value Go compares (ADR 0051).
type ChatTarget struct {
	Provider string
	Kind     ChatKind
	Thread   string
	Topic    string
}

// Valid reports whether provider, kind, and thread are present.
func (t ChatTarget) Valid() bool {
	return t.Provider != "" && t.Kind.Valid() && t.Thread != ""
}
