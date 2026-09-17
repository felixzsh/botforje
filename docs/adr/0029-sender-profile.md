# ADR 0029 — Sender profile and first contact

Status: Accepted (2026-09)

## Context

A sender was only a phone number used as a chat's key (ADR 0026). WhatsApp
delivers a push name with every inbound message, and Botflota dropped it. The
dashboard, first-contact automations, CRM integrations, and any contact
metric need the person to be durable, not a string.

A person may write to several bots of the same scope; that is normal, not an
exception. The scope remains the isolation boundary: the same phone in two
scopes is two people that never share data (ADR 0016).

## Decision

A sender is a first-class record, keyed by `(scope, phone)`:

```sql
senders (id, scope, phone, display_name, name_updated_at, first_seen, last_seen)
        unique (scope, phone)
chats   (id, scope, bot, sender_id -> senders, started_at, updated_at,
         message_count, guards)
        unique (scope, bot, sender_id)
```

- One profile per person per scope, shared by every bot of that scope. The
  bot-person relationship is the chat, one per `(scope, bot, sender)`.
- `display_name` is the WhatsApp push name. It is best effort, may be empty,
  changes freely, and is never an identifier: the phone number is the key. A
  non-empty name is adopted, and `name_updated_at` is stamped only when the
  name actually changes, so a later empty name never erases a known one.
- `first_seen` and `last_seen` are person-level; `chats.started_at` and
  `chats.updated_at` give the same values per bot. `chats.message_count` is the
  inbound count for that bot.
- The profile is written on every allowed inbound message, before routing. The
  filter runs first (ADR 0011), so ignored senders, a sender outside the allow
  list, and ignored groups are never persisted.
- The channel maps the push name into `IncomingMessage`; the engine seeds it as
  `{{sender.name}}` alongside `{{sender.phone}}` (ADR 0018). The stored name
  fills the reference when a message carries none.
- The `created` result of the upsert is the first-contact hook. Delivering it
  as a webhook is a later phase; it is not part of this record.

## Consequences

- `chats` no longer carries the phone text; a read that needs it joins
  `senders`. The chat keeps the key it had conceptually, now by profile id.
- Reads the API and dashboard need are cheap: contacts by last activity, one
  bot's contacts, and per-bot or per-scope counts.
- Per-bot blocking and allow/ignore stay configuration for now. If it moves
  into the database later, this key supports both "blocked for this bot"
  (on the chat) and "blocked for the scope" (on the sender).
- A send to a number that never wrote first (an outbound-only contact) does not
  create a profile yet.
- With `ignore_groups` false, a group participant is recorded like any sender;
  telling the two apart is an open decision.
- Push names are third-party personal data; retention, export, and purge follow
  the per-scope rules of a later phase.
- `message_count` is a denormalized counter. When message history is recorded,
  it either stays the cheap count or is reconciled against `messages`.
