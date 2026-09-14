# ADR 0024 — CLI command vocabulary

Status: Accepted (2026-09)

## Context

Commands mixed verbs and nouns without a stated rule. `auth` named device
pairing, but the domain, database, and `status` already use "pair", and the same
word is needed for administration authentication in the API phase.

## Decision

- Action commands use a verb (`init`, `validate`, `pair`, `unpair`, `reload`).
- Query commands use a noun (`schema`, `status`, `messages`); the long-running
  process command is `daemon`.
- Device pairing is `pair`; removing the local pairing is `unpair`. `link` and
  `unlink` are rejected because `unpair` is local and does not unlink the
  companion in WhatsApp.
- `auth` is reserved for authentication (admin key, sessions) and never names
  channel pairing.

## Consequences

- `pair`/`unpair` match the domain and the `paired` value in `status`.
- The API-phase authentication can claim `auth` without collision.
- New commands follow one predictable verb or noun shape.
