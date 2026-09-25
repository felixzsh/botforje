# ADR 0042 — Account history: keep, delete, migrate

Status: Accepted (2026-09)

## Context

Unlinking a bot already removes its credentials, its account record, its pairing
state, and the replies the old link still owed, and leaves everything else
alone (ADR 0021, ADR 0040). Two further operations were sketched but not
settled: an explicit deletion of one bot's history, and a migration that hands
it to another bot (`docs/current-plan.md`).

The stored shapes are what make them delicate. A chat is keyed
`(scope, bot, sender_id)`; sessions and messages cascade from their chat;
messages deduplicate on `(chat_id, provider_message_id)`; contacts are keyed
`(scope, bot, phone)`; and a queued reply hangs off its message. Senders are
keyed `(scope, phone)` and shared by the scope's bots (ADR 0029), so they belong
to no single bot and must survive both operations.

Migration cannot be a plain re-key either: the destination is often a peer bot
that has already talked to the same people, so the same sender can exist on both
sides.

## Decision

- **Unlinking is not deletion.** It stays what ADR 0021 and ADR 0040 describe:
  credentials, the account record, the pairing state, and the replies the old
  link owed go; chats, sessions, messages, sender profiles, and contacts stay,
  and the next pairing finds them. Neither operation below runs as a side
  effect of pairing, unpairing, or a config edit.

- **Deletion is its own explicit operation** on one `(scope, bot)`, confirmed by
  the caller: `DELETE /api/v1/scopes/{scope}/bots/{bot}/history` requires
  `confirm=<scope>/<bot>`, repeating the target exactly, so a mistyped or
  scripted call cannot erase the wrong bot. It removes that bot's chats and,
  through the cascades, their sessions, messages, and queued replies, plus its
  contacts. It never touches the scope's senders, another bot, or the linked
  account — dropping a link is unpair, a different operation with a different
  guarantee. The whole removal is one transaction, so a crash leaves all of it
  or none of it. A repeat removes nothing. It works on an unpaired bot, whose
  history outlives the link.

- **Deletion reports what it removed**: the chats, sessions, messages, and
  contacts it deleted. Those counts, not a second implementation, are what
  release the storage a limit set accounts for (`docs/access-policy.md`).

- **Migration is a move** to another bot of the same scope:
  `POST /api/v1/scopes/{scope}/bots/{bot}/history/migrations` with
  `{"to": "<bot>"}`. Scope never changes, because references and sessions never
  cross scopes (ADR 0016); a destination outside the scope, or the source
  itself, is refused. Senders need no transfer: the scope already shares them.

- **The destination absorbs a chat it already has.** For each source chat, by
  sender:
  - the destination has no chat with that sender: the source chat is re-keyed
    to the destination, sessions and messages following it;
  - the destination already has one: the source's messages and sessions move
    into it, a message whose provider id the destination chat already stores is
    skipped (the unique index would refuse it, and it is the same echo), the
    destination keeps its own `guards`, `started_at`, and identity, and its
    activity grows by what arrived. The emptied source chat is removed, so the
    same history is never stored twice.
  Contacts merge by phone: a phone both sides know keeps the row with the later
  `updated_at`, and a field that row leaves empty keeps the other row's text.

- **Live session state does not cross.** A run still active at the source is
  expired before anything moves, so the destination never appears to resume
  another bot's graph at a node it never reached. Historical runs move with
  their chat; nothing is deleted, so messages keep the session they belong to.

- **The source's queue fails first.** Pending replies are ended through
  `FailPending` (ADR 0040) before the move: the old link must never send what it
  no longer owns, and a queued row follows the message it belongs to.

- **Each operation stops what it must and puts it back.** The affected bot or
  bots are stopped for the duration — a running actor would keep writing the
  rows being moved — and one that was running is started again once the change
  has committed. The pause belongs to the operation, not to the caller's
  workflow, and it never stops the whole fleet.

- **One port owns both operations.** They span chats, sessions, messages, and
  contacts at once and must be atomic across all of them, so they do not belong
  to any one of the existing stores. A `HistoryStore` port exposes deletion and
  migration for one `(scope, bot)`; its SQLite adapter is the single
  transaction that makes them all-or-nothing. Storage accounting and any later
  retention work build on it rather than reaching into four tables.

- **Authority follows the scope, and failure is opaque across it.** Owner or a
  token authorized for the scope may act; an unknown bot, or one outside the
  caller's authority, answers 404 like every other scope-addressed route
  (`docs/api.md`). A confirmation that does not match its target is refused.

- **Retries need no request key.** Both operations empty their source, so a
  repeat cannot duplicate history: a deletion reports zero counts and a
  migration finds nothing left to move. This is why neither route takes an
  idempotency key where owner provisioning does.

## Consequences

- History is either deleted or moved, never copied. There is no operation that
  leaves two bots holding the same conversation, and no operation that splits a
  conversation across two bots.
- Deletion is irreversible: there is no archive and no soft delete. A caller
  that needs a copy must export first, and export is Phase 18 (ROADMAP), not a
  prerequisite of this operation.
- A peer bot that already talked to the same people gains their shared
  conversation while keeping its own guards, session boundaries, and identity,
  so what merges is history and never conversation state.
- Storage stops growing in a second place: the moved rows keep one home, and the
  counts a deletion returns are the same ones a limit set would release.
- The `confirm` parameter guards against acting on the wrong bot; it is not
  authorization, which stays with the principal and the scope.
