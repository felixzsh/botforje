# ADR 0037 — Account-directory import and historical provenance

Status: Accepted (2026-09); P1 provenance design aligned on 2026-10-11.
Implementation remains tracked in [the channel phases](../channels/phases.md).

## Context

A channel can expose its bound account's contact list without any prior message
exchange. Importing that data can register scoped identities, but it cannot invent
chats or live-inbound activity. Retained or migrated snapshots can outlive the
association that supplied them; data ownership is not current account membership.

## Decision

`DirectoryStore` persists directory snapshots. Acquiring an upstream directory is
an optional channel capability, distinct from the storage port; no full physical
handset address book or universal acquisition method is implied.

A `DirectoryEntry` carries:

- `Scope` and `Bot`: the current owner of the stored data.
- `PeerKey`: the valid normalized identity being described.
- `SourceIdentity`: the valid normalized identity of the account that supplied
  the snapshot, using the same provider as `PeerKey`.
- `DisplayName`: optional adapter-normalized presentation.
- `UpdatedAt`: the observation/import time, not proof of current membership.

Uniqueness includes `(scope, owner bot, source provider, source subject, peer
subject)`, with the same-provider invariant. Different source accounts retain
independent snapshots of the same peer. Scope and all identities are checked on
reads and writes. The source account identity must be known at import; a missing
source is not replaced with the bot name, device locator, or guessed participant.

`SourceIdentity` is minimal P1 origin provenance. Binding-generation/freshness
metadata remains P3; these records do not claim a current authorized association.
The data owner's bot may change during migration, but source identity and the
observation timestamp do not (ADR 0042). Moving a snapshot is not a fresh import.

### Identity and activity

Import ensures the scoped peer through `PeerStore.EnsurePeer`, with no first-seen
claim, chat, message, graph run, or routing. It may fill an unknown display name,
not replace a newer live name (ADR 0029). Historical import-derived read properties
report that an import supplied data; they do not prove present membership,
reachability, permission to send, or common identity across unrelated local spaces.
Sharing a contact card does not create any directory entry.

Provider-specific raw names, evidence, and precedence stay in adapter-owned state.
The adapter supplies one normalized display name; common persistence has no
`PushName`, `BusinessName`, phone property, or unchecked metadata map.

### WhatsApp acquisition and compatibility

The current WhatsApp adapter's opt-in remains off by default and scoped per bot.
Legacy `settings.import_contacts` and existing routes/commands have separate
compatibility treatment; Go renames do not authorize changing their meaning.

Inside that adapter:

- Request contact-list/app-state events while import is enabled and read the
  cached snapshot once per connection, including when enabled after binding.
- A contact-list event or suitable cached first/full name supplies membership
  evidence. Message push names and business names alone do not establish it;
  those observations can update a known entry only.
- Normalize PN identities using ADR 0020. Skip groups, broadcasts, and unresolved
  hidden users; neither a group nor unresolved address becomes a peer.
- Preserve raw name fields and their precedence in the SDK/adapter state. Emit
  the selected display name to the common store instead of exporting those fields.
- Directory acquisition can occur during setup or runtime without making either
  operation's success mean import completion.

Control-only history shells do not create identities or chats. A real stored
message, including a supported unknown-payload history row, can create its actual
chat (ADR 0026/0036). Directory import and history import are separate operations.

## Consequences

- Retention, deletion, and migration act on stored snapshots, not the provider's
  contact list. The SDK's private cache remains independent.
- Compatible history migration preserves the snapshot's origin and can carry it
  to another data owner, without claiming the receiving account knows that peer.
- Same-peer entries from different source accounts do not merge. Same-origin
  collisions follow ADR 0042's destination-row preference without rewriting origin.
- Removal events/freshness and complete binding provenance need their own consumer
  and P3 policy. P1 does not promise a current-membership view it cannot establish.
- Public directory/first-seen formats and variants remain independently reviewed;
  historical imports never emit the first allowed live-inbound event.
