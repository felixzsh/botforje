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
messages deduplicate on `(chat_id, provider_message_id)`; address-book entries
are keyed by the bot and the contact's subject (ADR 0037); and a queued reply
hangs off its message. Contact identities are keyed by the scope's subject
and shared by the scope's bots (ADR 0029), so they belong to no single bot and
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

- **Deletion reports what it removed**: the chats, sessions, messages, and
  address-book entries it deleted. Those counts, not a second implementation, are what
  release the storage a limit set accounts for (`docs/access-policy.md`).

- **History can be exported before it is deleted.**
  `GET /api/v1/scopes/{scope}/bots/{bot}/history/export` answers one `zip`
  holding a CSV per table the deletion removes — chats, sessions, messages, and
  the address book — with a header even when the table holds no row, its rows in
  stored order, a JSON-valued column as the JSON text it holds, and the
  contact's phone and name beside each chat so the archive stands alone. The archive streams
  while it is read, so a conversation of several megabytes costs constant
  memory, and the whole read is one transaction, so an arrival mid-export cannot
  tear it. Queued replies are not exported: they are the delivery state of
  messages the export already carries. It is a read — no confirmation, no
  request key, the same authority and 404 as any scope-addressed route — and
  deletion does not require it, but it is the safety net that makes an
  irreversible deletion an informed choice.

- **Migration is a move** to another bot of the same scope:
  `POST /api/v1/scopes/{scope}/bots/{bot}/history/migrations` with
  `{"to": "<bot>"}`. Scope never changes, because references and sessions never
  cross scopes (ADR 0016); a destination outside the scope, or the source
  itself, is refused. Contact identities need no transfer: the scope already
  shares them.

- **Only a compatible binding can receive the history.** The provider and the
  identity space of the two bots' account bindings must match: a chat
  referencing a store-local target (a SimpleX profile, for example) can never
  become a valid target of a bot with another binding, and history does not
  migrate across providers or local spaces (ADR 0051).

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

- **Address-book entries keep the destination's own row for a phone it knows.**
  The address book belongs to the account (ADR 0037), not to history, so an
  empty destination may still hold entries while holding no chat. A moved entry
  lands unless the destination already knows that phone, in which case the
  destination's row wins: its own account is the newer truth about that person.
  No field is merged and no row is lost — the source is emptied either way.

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

- **Address-book-only sources still have history to move.** Migration counts
  address-book entries independently of chats, so a bot with no conversations
  but an imported book is not mistaken for an already-empty source.

## Consequences

- History is either deleted or moved, never copied or merged. No operation
  leaves two bots holding the same conversation, and no operation splits one.
- Deletion is irreversible: there is no archive and no soft delete. The export
  is what makes that a decision instead of an accident, and it stays a separate
  operation so a caller who is sure never pays for it.
- A migrated chat keeps its own `started_at`, activity, guards, and session
  runs, because it is the same row under a new bot. A replacement bot therefore
  resumes exactly the conversation the lost account had.
- Storage stops growing in a second place: the moved rows keep one home, and the
  counts a deletion returns are the same ones a limit set would release.
- The `confirm` parameter guards against acting on the wrong bot; it is not
  authorization, which stays with the principal and the scope. A UI is expected
  to confirm the target in its own flow as well.
