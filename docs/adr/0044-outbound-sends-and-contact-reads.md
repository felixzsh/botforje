# ADR 0044 — Outbound sends and peer reads over the API

Status: Accepted (2026-09; P4 public contract approved 2026-10-12)

## Context

A host may need a bot to message a conversation that never wrote first without
impersonating a human or running a graph turn. `TurnStore.CommitTurn` requires
an inbound message and a graph run; an API send needs its own atomic admission
path. The host also needs scope-level peer reads, independent of a bot's
conversation history or directory membership (ADR 0029, ADR 0037).

## Decision

**An outbound send is an API operation, not a turn.**
`POST /api/v1/scopes/{scope}/messages` accepts `bot`, exactly one of `chat_id`
or `target`, optional `recipient`, `body`, `kind`, and optional `client_id`.
It answers `202 {id, status}` with `status: "queued"`; the current API content
is text. The selected adapter validates the target and declared content support
before queue admission (ADR 0051, ADR 0052). `202` promises durable queue
admission, not provider acceptance, recipient delivery, or arbitrary outreach.

- `bot` must exist in the authorized scope. `chat_id` resolves only under that
  scope and bot; an unknown or foreign chat answers 404. It selects the stored
  conversation target, never the sender or a derived phone address.
- `target` is `{provider, kind, thread, topic}`; provider, kind, and thread are
  required, and absent topic uses P1's canonical empty value (ADR 0051). Shared
  code never parses subjects or threads. A mismatched provider, unsupported
  target kind, or known forbidden operation is rejected before persistence.
- `recipient`, when known, is a separate `{provider, subject}` identity, not an
  alternative destination. It is optional; a target with no known participant
  never fabricates one. A chat-ID send uses the chat's known participant when
  present; a supplied recipient must not contradict that association.
- Supplying both selectors, neither selector, the retired string `to`, or a
  phone selector is invalid. No shorthand or compatibility alias is retained.
  Public CLI arguments use `--chat-id` or neutral JSON `--target`, with optional
  JSON `--recipient`, matching these domain names (ADR 0054).
- **A committed binding is required.** Before creating any peer, chat, message,
  or outbox row, no binding answers 409 `binding_required`. The API never stores
  an unbound send for an identity that might be linked later. A disconnected or
  stopped bot with a committed binding is not unbound: transport unavailability
  alone does not reject its durable queue admission. Known authorization and
  eligibility restrictions are checked separately, never inferred from local
  credentials or an unknown observation (ADR 0052, ADR 0053).
- A repeat of an accepted `client_id` answers 409 `duplicate` with the accepted
  message's `id` beside the error, so a lost-response retry can reconcile even
  after the binding changes. Reconciliation creates no new send; only a new
  admission requires a binding. Keys remain per `(scope, bot)`.
- `SendStore.CommitSend` commits the optional known peer identity, chat (created
  when missing), stored message, outbox row, and chat activity in one transaction
  or not at all. `CommitTurn` cannot serve this path. Registration and outbound
  activity do not claim a peer's first allowed live inbound (ADR 0029).
- The message is stored with `direction=out`, `ingest=api`, no graph run,
  `origin=api`, and `routing=unknown`. `OriginAPI` is not operator attribution.
  Public references use `graph_run_id`, never the retired `session_id` spelling
  for graph execution (ADR 0054).
- The bot's existing outbox delivers the send: pacing, retries, and failure
  recording share the reply path (ADR 0011, ADR 0040). Optional effects are used
  only when the selected implementation supports them (ADR 0052).
- Echo correlation covers API sends without treating their own echoes as
  external replies (ADR 0043). A send does not read or change chat control: it
  can be delivered while a chat is held. Taking a hold still cancels pending
  sends for that target, including API-origin ones already queued there.
- Queue admission is the future outbound-budget counting point. No limit-set
  policy or new aggregate ceiling is introduced here (`docs/access-policy.md`).

**Peer identities are read scope-level.**
`GET /api/v1/scopes/{scope}/peers` lists scoped identities newest activity first,
with `since` (unix milliseconds) filtering on `first_seen`, an opaque keyset
cursor, and `next_cursor` absent on the last page. `GET .../peers/{id}` uses the
scope-owned internal peer ID and answers one identity or 404. A subject alone
is never a cross-provider lookup key.

A peer view carries `id`, `provider`, `subject`, `display_name`, `first_seen`,
`last_seen`, `directory_imported`, `has_chat`, and `has_history`. The derived
flags report recorded directory import, a chat owned by a bot in the scope,
and at least one stored sync message, respectively. They do not grant current
membership, reachability, or permission to send. API sends and imported history
do not set `first_seen`.

Bot-owned directory snapshots are read under
`/api/v1/scopes/{scope}/bots/{bot}/directory` (ADR 0037, ADR 0054), not under
`contacts`. Their peer identity and source identity remain separate. A directory
entry is not a peer's chat, an interaction, or a contact-card payload.

## Phase boundary

These P4 contracts are owner-approved, not claims about current handlers.
The no-binding admission decision is closed here; P5 still implements durable
binding snapshots and generation checks at admission/delivery, classification,
message-ID provenance, and receive-processing completion. P4 must not invent
those remaining contracts or let queued sends inherit a replacement identity.

## Consequences

- A host can address a neutral conversation without a prior inbound message or
  known participant, subject to the selected adapter's actual admission rules.
- Message IDs and `client_id` reconciliation survive response loss without a
  second delivery pipeline or graph execution.
- Notices and replies share per-bot FIFO pacing and can delay one another.
- An unbound send is refused without creating data; a disconnected bound bot
  can retain a pending queue without being presented as authorized or connected.
- Legacy phone routes, selectors, and result fields are removed in P4 without
  migration or a compatibility period (ADR 0054).
