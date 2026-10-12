# ADR 0044 — Outbound sends and contact reads over the API

Status: Accepted (2026-09)

## Context

An external system that hosts a Botflota instance may need a bot to message a
phone that never wrote first — a status notice to a contact, an alert to an
operator — without impersonating a human and without a graph turn. Today every
outbound message comes from a turn: `TurnStore.CommitTurn` requires a session
and an inbound message, and the engine writes no rows directly (ADR 0040). The
API can hold a chat (ADR 0043) but cannot send from one.

The same system needs to read a scope's contact identities — who wrote, and
when they were first and last seen — which the scope stores (ADR 0029) but the
API does not serve.

## Decision

**An outbound send is an API operation, not a turn.** `POST
/api/v1/scopes/{scope}/messages` accepts `{bot, to, body, kind, client_id}` and
answers `202 {id, status}`: only `text` today; `to` identifies the target in
the bot's channel and account, validated by the adapter after rendering
(ADR 0051), which for WhatsApp is the phone number in plain digits (ADR 0020),
and `bot` must exist in the scope's configuration. A repeat
of an accepted `client_id` answers `409 duplicate` with the accepted message's
`id` beside the error, so a caller that lost the response can reconcile.

- A new store port commits the send as one unit: the contact identity, the chat
  (created when missing), the stored message, its outbox row, and the chat's
  activity land in one transaction or not at all. `CommitTurn` cannot serve —
  it requires a session and an inbound message — and the engine never writes
  these rows; a send outside a turn gets its own port and contract (ADR 0040).
- The message is stored with `direction=out`, `ingest=api`, `session_id=0`,
  `origin=api`, `routing=unknown`. `OriginAPI` joins the provenance vocabulary
  (ADR 0026); it is not `operator`.
- The send enters the bot's outbox and the existing delivery worker delivers
  it: pacing, jitter, typing, read receipts, retries, and failure recording
  are the same path as a reply (ADR 0011, ADR 0040). It is not a second engine.
- The channel pre-generates and registers the outbound id before sending
  (ADR 0043), so the message's own echo never looks like an operator reply and
  never pauses the chat. The send neither reads nor changes a chat's control
  state: it is delivered while a chat is `human`, and it never sets `human`.
- `client_id` is optional and idempotent per `(scope, bot)`. Admission to the
  outbox is where an outbound budget counts a message; when limit sets arrive
  with their own ADR (`docs/access-policy.md`), that admission consults them.
  Until then there are no configured ceilings to respect.

**Contact identities are read scope-level.** `GET
/api/v1/scopes/{scope}/contacts` lists the scope's identities newest activity
first, with `since` (unix milliseconds) filtering on `first_seen`, an opaque
keyset cursor, and `next_cursor` absent on the last page; `GET
.../contacts/{id}` answers one identity or 404. P4 replaces the phone selector
with the neutral peer reference and removes it outright: no legacy phone
selector, output projection, or compatibility alias is retained (ADR 0054,
PLAN.md "no backward compatibility"). This is
the API's first cursor-paginated read; ROADMAP Phase 10 generalizes the
convention rather than replacing it.

The contact read also derives `address_book_imported`, `has_chat`, and
`has_history`. `first_seen` is the live-inbound interaction signal; API sends
and imported history do not set it. `has_history` means at least one stored
message has `ingest=sync`, not merely that an outbound API send created a chat.

## Consequences

- A host can notify a number that never wrote: the contact and the chat are
  created by the same transaction that stores the message, and the read API
  can then list the person.
- The API exposes the message id, so a caller can follow delivery through the
  message history; the outbox row carries the client id for idempotency.
- An API send shares the bot's queue with the graph's replies, so a burst of
  notices delays replies and vice versa; that is the intended pacing boundary.
- A hand-over still cancels every pending reply of the chat (ADR 0043),
  including an API send queued before it: a human taking the chat owns it.
- A send to a stopped or unpaired bot is accepted and waits durably; unlinking
  the account fails it with the rest of the queue (ADR 0040).
- Outbound limits are not enforced until their policy lands; the admission
  point exists so enforcement is one check, not a new path.
