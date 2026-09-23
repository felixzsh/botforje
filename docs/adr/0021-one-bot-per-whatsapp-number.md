# ADR 0021 — One Botflota bot per WhatsApp phone number

Status: Accepted (2026-09)

## Context

WhatsApp permits several linked companion devices for one phone number. Botflota
models a bot as one account, graph, session namespace, and channel actor. Two
independent bots using the same number would both receive the account's traffic
and could send duplicate or conflicting replies.

## Decision

One normalized WhatsApp phone number may belong to only one `(scope, bot)` in a
Botflota instance. The `bot pair` command rejects a number already owned by
another bot, and fleet startup rejects duplicate legacy records. A bot may
replace its own pairing with `bot pair --force`.

`unpair` and `bot pair --force` run the same removal: the linked-account row,
the pairing status, and the channel's device row with everything that hangs off
it — its keys, sessions, app-state cache, hidden-user map, contact cache, and
message secrets. The bot's queued replies end failed with the reason, so a
re-pair never sends them from another account. Botflota's own senders, chats,
sessions, messages, and imported contacts are never touched.

The phone keeps listing the removed companion until it is unlinked there, so a
re-pair adds a linked device instead of replacing one. A fresh registration is
also the only way to change what the registration carries, such as the requested
history window (ADR 0035), because WhatsApp reads it once, at linking time.
Remote linked-device removal remains an explicit operation in WhatsApp.

## Consequences

- A fleet has one clear owner for each WhatsApp account.
- Existing duplicate records must be resolved before the fleet starts.
- Intentional multi-bot use of one number is not supported; it needs a future
  shared-account channel design with explicit routing and deduplication.
