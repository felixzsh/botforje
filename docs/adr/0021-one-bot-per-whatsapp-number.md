# ADR 0021 — One BotForje bot per WhatsApp phone number

Status: Accepted (2026-09)

## Context

WhatsApp permits several linked companion devices for one phone number. BotForje
models a bot as one account, graph, session namespace, and channel actor. Two
independent bots using the same number would both receive the account's traffic
and could send duplicate or conflicting replies.

## Decision

One normalized WhatsApp phone number may belong to only one `(scope, bot)` in a
BotForje instance. The `bot pair` command rejects a number already owned by
another bot, and daemon startup rejects duplicate legacy records. A bot may
replace its own pairing with `bot pair --force`.

The `unpair` command removes local credentials and account metadata without
deleting sessions or message history. Remote linked-device removal remains an
explicit operation in WhatsApp.

## Consequences

- A fleet has one clear owner for each WhatsApp account.
- Existing duplicate records must be resolved before the daemon starts.
- Intentional multi-bot use of one number is not supported; it needs a future
  shared-account channel design with explicit routing and deduplication.
