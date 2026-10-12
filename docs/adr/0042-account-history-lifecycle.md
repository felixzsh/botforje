# ADR 0042 — Account history: keep, delete, migrate

Status: Accepted (2026-09)

## Context

Unlinking a bot already removes its credentials, its account record, its pairing
state, and the replies the old link still owed, and leaves everything else
alone (ADR 0021, ADR 0040). Two further operations were sketched but not
settled: an explicit deletion of one bot's history, and a migration that hands
it to another bot (`docs/current-plan.md`).

The stored shapes are what make them delicate. A chat is keyed by its
conversation (ADR 0051); sessions and messages cascade from their chat;
messages deduplicate in their actual destination and provider message space
(ADR 0051/0026); migration preserves that provenance instead of assigning old
IDs to the destination binding's message space. Directory snapshots
are keyed by data owner, source account identity, and peer identity (ADR 0037);
a queued reply hangs off its message. Peer identities are scoped provider/subject
keys shared by the scope's bots (ADR 0029), so they belong to no single bot and
must survive both operations.

Migration cannot be a plain re-key either: the destination is often a peer bot
that has already talked to the same people, so the same contact can exist on
both sides.

## Decision

- **Unlinking is not deletion.** It stays what ADR 0021 and ADR 0040 describe:
  credentials, the account record, the pairing state, and the replies the old
  link owed go; chats, sessions, messages, contact identities, and address-book
  entries stay, and the next pairing finds them. Neither operation below runs as a side
  effect of pairing, unpairing, or a config edit.

- **Deletion is its own explicit operation** on one `(scope, bot)`, confirmed by
  the caller: `DELETE /api/v1/scopes/{scope}/bots/{bot}/history` requires
  `confirm=<scope>/<bot>`, repeating the target exactly, so a mistyped or
  scripted call cannot erase the wrong bot. It removes that bot's chats and,
  through the cascades, their sessions, messages, and queued replies, plus its
  address-book entries. It never touches the scope's contact identities,
  another bot, or the linked
  account — dropping a link is unpair, a different operation with a different
  guarantee. The whole removal is one transaction, so a crash leaves all of it
  or none of it. A repeat removes nothing. It works on an unpaired bot, whose
  history outlives the link.

- **Deletion reports what it removed**: `chats`, `graph_runs`, `messages`, and
  `directory` counts, using the selected domain terms rather than retired
  session/address-book names. History-move reports use the same grouping.
  Those counts, not a second implementation, are what release the storage a
  limit set accounts for (`docs/access-policy.md`).

- **History can be exported before it is deleted.**
  `GET /api/v1/scopes/{scope}/bots/{bot}/history/export` answers a ZIP of CSVs
  over the domain sections `chats`, `graph_runs`, `messages`, and `directory`.
  Sections describe history, not SQL tables (owner decision 2026-10-12).
  The format adapter owns physical filenames and CSV encoding; stores and core
  history ports expose sections, never table or archive filenames. The selected
  CSV contract is below. Every section has its header even when empty, rows keep
  stored order, and JSON-valued columns carry JSON text. The export streams in
  constant memory and the whole read is one snapshot, so an arrival mid-export
  cannot tear it. Queued replies, credentials, and setup-session inputs are not
  exported. It is a read: no confirmation or request key, and the same authority
  and 404 as any scope-addressed route. Deletion does not require an export.

- **Migration is a move** to another bot of the same scope:
  `POST /api/v1/scopes/{scope}/bots/{bot}/history/migrations` with
  `{"to": "<bot>"}`. Scope never changes, because references and sessions never
  cross scopes (ADR 0016); a destination outside the scope, or the source
  itself, is refused. Contact identities need no transfer: the scope already
  shares them.

- **Only a compatible binding can receive the history.** Provider and durable
  identity space must be compatible (ADR 0051). Matching provider names or
  internal IDs alone is not proof; common code does not parse opaque subjects
  or assume generation identifies the space. The adapters' documented spaces
  must establish compatibility before any move; unavailable proof is refused.
  A store-local target cannot become usable under an unrelated profile merely
  because its history moved. Access references remain adapter-owned and sending
  still requires the destination account's authorization.

- **The destination must be empty.** Migration exists for a bot that replaces
  a lost account, so the destination is a bot with no chat history; if it holds
  any chat, the operation is refused and names it, and the caller empties it
  with an explicit deletion or picks another bot. Nothing is absorbed and
  nothing is merged. Without that rule a chat would have to hold two bots'
  messages, and it cannot: a message stores no bot, reads order by id rather
  than by time (so moved older rows would surface at the end of the thread),
  and guards live per chat, so the destination's would silently replace the
  source's. A chat moves whole or not at all.

- **Live session state does not cross.** A run still active at the source is
  expired before anything moves, so the destination never appears to resume
  another bot's graph at a node it never reached. Historical runs move with
  their chat; nothing is deleted, so messages keep the session they belong to.

- **The source's queue goes with its history.** Its queued replies are removed,
  not carried and not left behind: the old link must never send what it no
  longer owns (ADR 0040), and a `failed` row would otherwise point at a message
  another bot now holds, with no bot left to diagnose it against. The source is
  stopped with its queue dropped, and the rows leave in the same transaction as
  the move.

- **Directory snapshots move with their historical origin intact.** An empty
  destination may already hold directory data. The move changes the stored
  data's owner bot, never its `SourceIdentity`, peer key, or observation time
  (ADR 0037). Another account's snapshot does not become current membership
  of the receiving account and conveys no reachability or send permission.
  Different source identities retain separate rows even for the same peer.
  Only a collision of the complete owner/source/peer key prefers the existing
  destination row; it does not merge fields or rewrite origin. The source
  directory rows leave in the same transaction, preserving move/retry semantics.
  Full binding-generation provenance remains P3, not a freshness guarantee
  inferred from this minimal P1 source identity.

- **Each operation stops what it must and puts it back.** The affected bot or
  bots are stopped for the duration — a running actor would keep writing the
  rows being moved — and one that was running is started again once the change
  has committed. A bot that was not running is not touched, and a pause that
  fails is reported without abandoning the work. The pause belongs to the
  operation, not to the caller's workflow, and it never stops the whole fleet.

- **One port owns both operations.** They span chats, sessions, messages, and
  the address book at once and must be atomic across all of them, so they do not
  belong to any one of the existing stores. A `HistoryStore` port exposes
  deletion and migration for one `(scope, bot)`; its SQLite adapter is the
  single transaction that makes them all-or-nothing. Storage accounting and any
  later retention work build on it rather than reaching into those tables.

- **Authority follows the scope, and failure is opaque across it.** Owner or a
  token authorized for the scope may act; an unknown bot, or one outside the
  caller's authority, answers 404 like every other scope-addressed route
  (`docs/api.md`). A confirmation that does not match its target is refused.

- **Retries need no request key.** Both operations empty their source, so a
  repeat cannot duplicate history: a deletion reports zero counts and a
  migration finds nothing left to move. This is why neither route takes an
  idempotency key where owner provisioning does.

- **Directory-only sources still have history to move.** Migration counts
  historical directory snapshots independently of chats, so a bot with no
  conversations but imported entries is not mistaken for an empty source.

## P4 export contract (owner-approved 2026-10-12)

The ZIP/CSV format adapter emits these files and ordered headers. Their names
follow the selected domain vocabulary (ADR 0054), independently of SQLite names:

- `chats.csv`: `id`, `scope`, `bot`, `target_provider`, `target_kind`,
  `target_thread`, `target_topic`, `peer_id`, `peer_provider`, `peer_subject`,
  `peer_name`, `started_at`, `updated_at`, `message_count`, `guards`.
- `graph_runs.csv`: `id`, `chat_id`, `node`, `visited_nodes`, `graph_context`,
  `node_context`, `created_at`, `updated_at`, `expires_at`.
- `messages.csv`: `id`, `chat_id`, `graph_run_id`, `target_provider`,
  `target_kind`, `target_thread`, `target_topic`, `direction`, `kind`, `body`,
  `content`, `at`, `provider_message_id`, `ingest`, `origin`, `routing`.
- `directory.csv`: `scope`, `bot`, `peer_provider`, `peer_subject`,
  `source_provider`, `source_subject`, `display_name`, `updated_at`.

A chat's optional peer is not its destination. Export its persisted target even
when no participant is known; unknown optional peer fields are empty. A message
exports its actual target independently of the history chat and has an empty
`graph_run_id` when no graph execution owns it. Directory rows preserve their
peer identity and historical source identity without claiming current membership.
There is no universal phone column; `phone` may occur only inside an opaque
payload such as a contact-card entry (ADR 0036).

Structured columns use their neutral stored codecs; timestamps use UTC
RFC3339Nano and absent optional values are empty. Use only modeled, persisted
provenance: this format does not invent P5 message namespaces, receive cursors,
or setup secrets. No legacy reader, old CSV header, or compatibility archive is
retained, and no old-database migration is required (ADR 0054).

P4 implements this public format and enforces binding compatibility before a
history move. P5 still owns retained message spaces and replacement dedup; P6
checks conformance and documentation against the implemented format. Approval
of this contract does not close those implementation gates.

## Consequences

- History is either deleted or moved, never copied or merged. No operation
  leaves two bots holding the same conversation, and no operation splits one.
- Deletion is irreversible: there is no automatic recovery archive or soft
  delete. The explicit export makes it an informed decision and stays a separate
  operation so a caller who is sure never pays for it.
- A migrated chat keeps its own `started_at`, activity, guards, and session
  runs, because it is the same row under a new bot. A replacement bot therefore
  resumes exactly the conversation the lost account had.
- Storage stops growing in a second place: the moved rows keep one home, and the
  counts a deletion returns are the same ones a limit set would release.
- The `confirm` parameter guards against acting on the wrong bot; it is not
  authorization, which stays with the principal and the scope. A UI is expected
  to confirm the target in its own flow as well.
