# ADR 0029 — Contact identity and first contact

Status: Accepted (2026-09)

## Context

A person used to be only a phone number used as a chat's key (ADR 0026), and
then a `sender` profile. WhatsApp delivers a push name with every inbound
message, and the dashboard, first-contact automations, CRM integrations, and
any contact metric need the person to be durable, not a string.

A person may write to several bots of the same scope; that is normal, not an
exception. The scope remains the isolation boundary: the same phone in two
scopes is two people that never share data (ADR 0016). The word `sender` named
a role in one message, so it was easy to confuse with the recipient; the scope
now holds one vocabulary, and the person is a **contact**.

## Decision

A contact is a first-class identity, keyed by `(scope, phone)`:

```sql
contacts (id, scope, phone, display_name, name_updated_at, first_seen, last_seen)
         unique (scope, phone)
chats    (id, scope, bot, contact_id -> contacts, started_at, updated_at,
         message_count, guards)
         unique (scope, bot, contact_id)
```

- One identity per person per scope, shared by every bot of that scope. The
  bot-person relationship is the chat, one per `(scope, bot, contact)`.
- `display_name` is the WhatsApp push name. It is best effort, may be empty,
  changes freely, and is never an identifier: the phone number is the key. A
  non-empty name is adopted, and `name_updated_at` is stamped only when the
  name actually changes, so a later empty name never erases a known one.
  History and address-book imports only fill an unknown display name; a live
  inbound push name may refresh it.
- `first_seen` is the first live inbound message in the scope and is claimed
  only once; `last_seen` moves with every live inbound. Neither is set when the
  address book (ADR 0037) or an API send (ADR 0044) creates the identity, so
  the first-contact hook fires on the person's first message even when an
  import or an operator send knew the number first.
- `chats.started_at` and `chats.updated_at` give the same values per bot;
  `chats.message_count` is the inbound count for that bot.
- The identity is written on every allowed inbound message, before routing.
  The filter runs first (ADR 0011), so ignored contacts, a contact outside the
  allow list, and ignored groups are never persisted.
- The contact store separates the two writes: `EnsureContact` creates an
  identity without claiming `first_seen`, and `RecordInbound` claims it with a
  conditional update and reports that claim — the first-contact hook. The
  affected rows are the answer, so two messages sharing one timestamp still
  report first only once, and the claim commits with the activity and the
  name, so a failure rolls all three back together (ADR 0045).
- The channel maps the push name into `IncomingMessage`; the engine seeds it as
  `{{contact.name}}` alongside `{{contact.phone}}` (ADR 0018). The stored name
  fills the reference when a message carries none.
- The claimed `first_seen` is the first-contact hook. ADR 0045 delivers it as a
  signed webhook; it changes nothing in this record.

## Consequences

- `chats` no longer carries the phone text; a read that needs it joins
  `contacts`. The chat keeps the key it had conceptually, now by identity id.
- Reads the API and dashboard need are cheap: contacts by last activity, one
  bot's chats, and per-bot or per-scope counts.
- Per-bot blocking and allow/ignore stay configuration for now. If it moves
  into the database later, this key supports both "blocked for this bot"
  (on the chat) and "blocked for the scope" (on the contact).
- An API-origin send to a number that never wrote first creates the identity
  and its chat (ADR 0044), because the send needs them; it never claims
  `first_seen`, which belongs to an inbound message.
- With `ignore_groups` false, a group participant is recorded like any contact;
  telling the two apart is an open decision.
- Push names are third-party personal data; retention, export, and purge follow
  the per-scope rules of a later phase.
- `message_count` is a denormalized counter. When message history is recorded,
  it either stays the cheap count or is reconciled against `messages`.
