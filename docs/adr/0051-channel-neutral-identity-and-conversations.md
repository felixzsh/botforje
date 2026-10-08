# ADR 0051 — Channel-neutral identity and conversations

Status: Accepted (2026-10)

## Context

The runtime keys contacts by phone (ADR 0020, ADR 0029) and a chat by
`(scope, bot, contact)` (ADR 0026). WhatsApp is the only channel today, so a
phone can stand in for identity. Another provider does not have phones:
Telegram identifies a user with its own id, and SimpleX contacts live inside
one local profile's namespace. Treating a phone column as an undocumented
container for those ids would make the second channel a schema lie.

The preparation plan (`docs/channel-plan.md`) needs two values validated at
the adapter boundary: who a message is from, and which conversation it belongs
to. They are not the same. A group has participants and a thread, and the same
person can write in a direct chat and in several groups of one account without
those becoming one thread.

## Decision

Two comparable values, always scope-qualified in durable lookups:

- **Contact key** — `provider + namespace + subject`. The provider names the
  messaging service. The namespace identifies the identity space when it is
  local to one account or profile, and is empty when identities are
  provider-wide and shared by the scope's bots. The subject is the adapter's
  normalized opaque external id. Phone is optional metadata on the contact,
  never identity and never a fallback key.
- **Conversation target** — the owning account binding, a conversation kind,
  an opaque external thread id, and an optional topic. Kinds start with
  `direct` and `group`. An inbound envelope carries both its sender and its
  conversation; a reply defaults to the conversation it arrived on, never to
  the last conversation a sender was seen in.

The account binding is `(scope, bot, provider, adapter, mode, namespace, safe
identity, generation)`. The namespace survives reconnects and a renewal of the
same identity; replacing the identity advances the generation. Chats do not
carry a generation — history survives a relink (ADR 0042) — but queued sends
do (ADR 0040).

WhatsApp keeps its current meaning: provider `whatsapp`, empty namespace, and
subject = the plain PN resolved inside the adapter (ADR 0020). For a direct
chat the participant is the chat's contact. Nothing in WhatsApp's behavior
changes.

Persistence:

- `contacts` is unique by `(scope, provider, namespace, subject)`; `phone` is
  metadata for a channel that has one.
- `chats` is unique by `(scope, bot, provider, namespace, kind, thread,
  topic)`, with the participant's contact when the kind has one.
- `messages` can carry a sender reference, so a shared conversation is
  representable without materializing a group as one person's contact.
- `bot_contacts` is keyed by the bot and the contact's subject, like the
  contact it belongs to.
- The upgrade is additive with a backfill: existing WhatsApp rows get provider
  `whatsapp`, empty namespace, and their phone as subject and thread. Numeric
  ids, sessions, dedup keys, guards, and queued sends survive; the path is
  tested against a pre-change database. Until release the schema is still
  edited in place on open (ADR 0006); the first released schema becomes
  migration 1, and no later change may drop an existing database silently.

API and runtime:

- Representations use opaque string ids, not JavaScript-rounded integers or
  wire addresses. Legacy phone selectors remain for WhatsApp only (ADR 0044),
  and ids resolve under their scope and bot, never by id alone.
- The template context gains `contact.id` (opaque, stable within the scope)
  and keeps `contact.phone`, which is empty when the channel has no phone.
  Missing-path errors do not weaken (ADR 0018).
- Until participant, session, and guard semantics for shared conversations
  are settled, a group is refused or filtered explicitly, never processed as
  a direct chat.
- Never infer the same person across providers or across unrelated
  namespaces; history migration refuses a provider or namespace mismatch
  (ADR 0042).

## Consequences

- The same sender in a direct chat and two groups has three independent chats,
  sessions, holds, and guard states.
- A second channel maps its own ids and does not fabricate a phone; WhatsApp
  keeps PN normalization inside its adapter.
- ADR 0020, 0026, 0027, 0029, 0037, 0040, 0042, 0043, and 0044 are updated in
  place to this vocabulary. Implementation is P1 of `docs/channel-plan.md`.
