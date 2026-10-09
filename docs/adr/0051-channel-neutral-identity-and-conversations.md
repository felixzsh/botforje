# ADR 0051 — Channel-neutral identity and conversations

Status: Accepted (2026-10)

## Context

The runtime keys contacts by phone (ADR 0020, ADR 0029) and a chat by
`(scope, bot, contact)` (ADR 0026). WhatsApp is the only channel today, so a
phone can stand in for identity. Another provider does not have phones:
Telegram identifies a user with its own id, and SimpleX ids are only meaningful
inside one local profile. Treating a phone column as an undocumented container
for those ids would make the second channel a schema lie.

The preparation plan (`docs/channel-plan.md`) needs two values validated at
the adapter boundary: who a message is from, and which conversation it belongs
to. They are not the same. A group has participants and a thread, and the same
person can write in a direct chat and in several groups of one account without
those becoming one thread.

## Decision

Two comparable values, always scope-qualified in durable lookups:

- **Contact key** — `provider + subject`. The provider names the messaging
  service. The subject is the adapter's normalized opaque external id. When a
  provider's ids are only meaningful inside one store or profile, the adapter
  composes that space's stable identifier into the subject itself, so the core
  carries no second identity field and the boundary that maps the provider
  documents the composition. Phone is optional metadata on the contact, never
  identity and never a fallback key.
- **Conversation target** — a conversation kind, an opaque external thread id,
  and an optional topic. The thread carries the same provider-local space the
  subject does. Kinds start with `direct` and `group`. An inbound envelope
  carries both its sender and its conversation; a reply defaults to the
  conversation it arrived on, never to the last conversation a sender was seen
  in.

The account binding is `(scope, bot, provider, adapter, mode, namespace, safe
identity, generation)`: the namespace is the account's own identity space, the
one its adapter folds into the subjects and threads it produces. It survives
reconnects and a renewal of the same identity; replacing the identity advances
the generation. Chats do not carry a generation — history survives a relink
(ADR 0042) — but queued sends do (ADR 0040).

Physical endpoints and an auxiliary service's current profile are not binding
identity. Temporary execution-context selection changes neither the verified
identity nor its generation. Observations belong to the binding that produced
them; retiring/replacing a binding must not reassign its pending observations
to the replacement. A logical channel may share a backend without sharing its
bot's identity (ADR 0004).

Provider message IDs are scoped by their actual destination and message space,
including account context where the provider requires it. Source update IDs
and receive cursors are separate adapter values, not message IDs. Retained or
migrated history must not suppress a new binding's unrelated message with the
same ID. Reconnect and reauthorization of the same identity preserve its space;
binding generation is not automatically that space. The normalized provenance
and durable dedup key remain to be settled with ADR 0026 before P5 closes;
this decision does not select a universal new namespace field or SQL index.

WhatsApp keeps its current meaning: provider `whatsapp` and subject = the plain
PN resolved inside the adapter (ADR 0020), which is already provider-wide, so
nothing is composed into it. For a direct chat the participant is the chat's
contact. Nothing in WhatsApp's behavior changes.

Persistence:

- `contacts` is unique by `(scope, provider, subject)`; `phone` is metadata for
  a channel that has one.
- `chats` is unique by `(scope, bot, provider, kind, thread, topic)`, with the
  participant's contact when the kind has one.
- `messages` can carry a sender reference, so a shared conversation is
  representable without materializing a group as one person's contact.
- `bot_contacts` is keyed by the bot and the contact, like the contact it
  belongs to.
- The pre-release schema is edited in place on open (ADR 0006): the owner
  decision in `../PLAN.md` prefers the smallest coherent channel-neutral
  schema over preserving the development database, so there is no backfill and
  no pre-change-database compatibility gate. `make dev-reset` recreates a
  development database explicitly, the first released schema becomes
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
- Never infer the same person across providers or across unrelated local
  spaces; history migration refuses a provider mismatch and compares the two
  bots' bindings before moving anything (ADR 0042).

## Consequences

- The same sender in a direct chat and two groups has three independent chats,
  sessions, holds, and guard states.
- A second channel maps its own ids and does not fabricate a phone; WhatsApp
  keeps PN normalization inside its adapter. A provider with store-local ids
  documents and composes its own space into the subject and thread rather than
  asking the core for a second identity field.
- ADR 0020, 0026, 0027, 0029, 0037, 0040, 0042, 0043, and 0044 are updated in
  place to this vocabulary. Implementation is P1 of `docs/channel-plan.md`.
