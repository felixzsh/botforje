# ADR 0051 — Channel-neutral identity and conversations

Status: Accepted (2026-10); P1 design concretized on 2026-10-11.
Implementation is tracked in [the channel phases](../channels/phases.md).

## Context

The current runtime uses phone strings for identities and destinations and stores
an SDK device JID as its linked account. Those values happen to work for the
current adapter; they cannot define the common contracts. An identity, a durable
conversation, and an address usable under an authorization are different roles.
A person's identity need not equal a direct thread's identifier, and a shared
thread is not one person's identity.

## Decision

### Comparable identity and destination values

The core owns these standard-library-only value types:

```go
type IdentityKey struct {
    Provider string
    Subject  string
}

type ChatKind string

const (
    ChatDirect ChatKind = "direct"
    ChatGroup  ChatKind = "group"
)

type ChatTarget struct {
    Provider string
    Kind     ChatKind
    Thread   string
    Topic    string
}
```

- Provider identifiers are open, stable catalog identifiers, not an enum of
  installed services. Subjects, threads, and topics are opaque, case-sensitive
  strings: common code compares and persists them without trimming, digit
  validation, suffix removal, or protocol parsing.
- Provider and subject are required for a valid identity. Provider, kind, and
  thread are required for a valid target. The initial kinds are direct and group;
  representing a kind does not enable its runtime policy or channel support.
- Empty topic means no topic. Persistence uses the same canonical empty value,
  not nullable topic keys whose uniqueness could differ from Go equality.
- Scope is outside both values. Peer lookups use `(scope, provider, subject)`;
  chat lookups use `(scope, bot, provider, kind, thread, topic)`. References and
  writes must validate scope and provider, never trust an internal ID alone.
- Identity equality is not destination equality. A comparable external ID is not
  a credential or proof that a different authorization can use it. Resolution,
  access references, eligibility, and credential-bearing state remain adapter-owned.

### Durable local identity spaces

For IDs local to a store or profile, the adapter incorporates that space's stable
identity into its normalized subjects and threads. It documents an unambiguous
encoding; common code never splits or recomposes it. Topic IDs are interpreted
within their complete target, not as global IDs.

The space survives reconnect, restart, and renewal of the same identity. Replacing
an unrelated store or profile must not recycle its old normalized keys. A bot name,
filesystem path, endpoint, port, active-profile selection, or reconnect counter is
not the space's identity. No second namespace field is added to `IdentityKey` or
`ChatTarget`; binding metadata may expose its namespace for compatibility checks.
Binding generation is not automatically an identity space or a message-ID space.

### Sender, participant, peer, and chat

- A normalized live inbound routed to the graph carries a valid `Sender`
  (`IdentityKey`) and a valid `Target` (`ChatTarget`) for the selected provider.
  Traffic whose sender cannot be normalized is not assigned a fabricated peer;
  support for routing such traffic needs its own consumer and policy. History may
  retain an unknown sender without claiming live activity (ADR 0026).
- `Peer` owns a scoped `Key` (`IdentityKey`), an internal identity ID, and optional
  normalized presentation. Knowing a peer does not require a message exchange.
- `Chat` owns its `Target` and an optional `PeerID`. A direct thread does not prove
  its participant. Known participant identity comes from an explicit adapter
  mapping or supplied identity, not generic thread-to-subject conversion.
- Optional internal peer references use the existing Go convention: zero means
  absent; SQLite stores NULL, not a fabricated peer or an ID of zero. Sender
  identity and the chat's participant reference are separate data.
- Resolve graph execution, holds, and guards by chat. One sender in two supported
  conversations has independent state. A reply defaults to the inbound target,
  never a sender lookup, cached last conversation, or inferred direct address.
- Shared conversations remain explicitly filtered/refused until their execution
  policy is approved. Nullable participant references do not enable group routing.

### P1 store-operation contract

The identity operations retain their existing activity distinction (ADR 0029):

```go
EnsurePeer(ctx context.Context, scope string, key IdentityKey, at time.Time) (*Peer, error)
RecordInbound(
    ctx context.Context, scope string, key IdentityKey, name string, at time.Time,
) (*Peer, bool, error)
```

The boolean reports the durable first-seen claim, not whether registration was new.
Chat creation and lookup use the target, with optional known participant metadata:

```go
Ensure(
    ctx context.Context, scope, bot string, target ChatTarget, peerID int64, at time.Time,
) (*Chat, error)
ForTarget(ctx context.Context, scope, bot string, target ChatTarget) (*Chat, error)
```

`Ensure` creates/finds by complete target. A supplied nonzero peer ID must resolve
under the same scope/provider. An unknown relation may be filled by explicit
participant evidence; conflicting known participants are rejected rather than
silently rewriting the association. Sending or recording an API target without
known participant metadata leaves the reference absent. These signatures describe
methods on `PeerStore` and `ChatStore`, not standalone core functions.

The sender-only `ChatStore.ForContact` operation is replaced by `ForTarget`;
peer-based collection reads cannot choose a reply destination. Directory ports
consume normalized entries carrying owner, source identity, peer key, display name,
and observation time, with no phone/raw-name parameters (ADR 0037).

### Accounts and directory provenance

P1 persists a bot's normalized own `Identity` (`IdentityKey`), not its device JID
or a phone property. SDK locators and credentials remain in adapter-owned storage;
use the existing SDK store and only add an adapter-owned bot-to-device relation
when needed to restore the correct device after restart.

The full binding remains `(scope, bot, provider, adapter, mode, namespace, safe
identity, generation)` and belongs to P3 (ADR 0053). Temporary backend-context
selection does not replace its verified identity. Observations belong to the
association that produced them; replacement cannot silently reassign pending work.
A logical per-bot channel may share a physical resource (ADR 0004).

`DirectoryEntry` records a known peer's imported account-directory snapshot:
`PeerKey` identifies the peer; `SourceIdentity` identifies the normalized account
that supplied it. Both are valid identities of the same provider. Its `(scope,
bot)` identifies the current owner of the stored data, not necessarily the account
that originally imported it. A move preserves `SourceIdentity` (ADR 0037/0042).

Directory uniqueness includes owner, source identity, and peer identity. Snapshots
from two accounts do not collapse into one membership; adapter-specific names are
normalized into `DisplayName` before common persistence. Source identity is minimal
P1 provenance, not a complete binding-generation snapshot or proof of freshness.
P3 adds the necessary association provenance. Import or migration never proves
current membership, reachability, or send permission.

### Outbound targets and message provenance

Every outbound operation carries its actual `Target`. `SendRecord.Recipient` is
an optional `*IdentityKey`: nil means no known recipient, and a present value must
be valid. It is separate from required `SendRecord.Target`. Inbound `Sender` is a
valid value, not an empty key standing for an unknown identity. Do not derive either
recipient or target from the other in common code. An alternate destination can
retain the triggering chat's history association, but its record and queued
entry persist the exact destination used for sending.

Provider message IDs are opaque and scoped by actual destination and the provider's
message space, including account context where required. They are not source
update IDs or receive cursors. P1 proves destination separation, including IDs
that overlap those in the triggering chat; full retained-history/replacement
provenance and the durable dedup key remain P5 decisions with ADR 0026. Neither
binding generation nor a global unique message-ID index is an automatic solution.

### Common values and compatibility

No universal phone field, parameter, column, helper, or template path belongs in
these identities, targets, peer/directory projections, or account records. The
`phone` contact-card entry label remains opaque payload data, not identity or a
routing instruction. Existing setup/result cleanup remains P3, not completed P1.

Templates use neutral peer values. Approved legacy template translation is scoped
to the consuming bot and its compatible adapter (ADR 0018/0020). Missing paths
remain errors. Internal renames do not authorize changing selectors, JSON/YAML,
routes, event topics, or archives; each affected boundary needs its explicit
compatibility decision before implementation (ADR 0054).

WhatsApp retains provider `whatsapp` and adapter-normalized PN subjects, which are
provider-wide and need no local prefix. Its direct thread may equal the subject
only because the adapter establishes that mapping. PN/LID resolution stays inside
the adapter (ADR 0020); the core treats the resulting digits as an opaque subject.

## Consequences

- `PeerStore` ensures scoped identities without claiming first-live-inbound
  activity; `RecordInbound` uses the same typed key (ADR 0029).
- `ChatStore` creates/finds by the complete target. A sender-only lookup cannot
  pick one conversation; any peer-based listing must allow multiple chats.
- Foreign keys and scoped store operations preserve the identity/conversation
  separation. P1 changes schema, indexes, and consumers together under the
  explicit pre-release reset policy (ADR 0006), with no legacy database backfill.
- Migration must prove provider and identity-space compatibility through the
  adapters' documented spaces, not parsing opaque subjects or guessing from a
  matching provider alone. Unknown compatibility is refused (ADR 0042).
- P1 is complete only after propagation, WhatsApp mapping, destination dedup tests,
  and a real close/reopen/restart without a sender cache. This ADR selects the
  design, not a passing gate or approval to implement P3/P5 early.
