# ADR 0029 — Peer identity and first live inbound

Status: Accepted (2026-09); P1 design aligned and implemented on 2026-10-11.
Implementation is tracked in [the channel phases](../channels/phases.md).

## Context

The runtime needs a durable scoped identity, not a phone string or the sender role
of one message. Directory import and API sends can introduce an identity before
its first live inbound. That must not consume the first-seen event or invent a
conversation. Identity, directory provenance, and chat activity are separate.

## Decision

`Peer` owns an internal ID, scope, `Key` (`IdentityKey`), normalized display name,
name-update time, and first/last live-inbound times. The durable external key is
`(scope, provider, subject)` (ADR 0051), not a phone column. No common telephone
property, getter, or parsing remains in the target contract.

- Bots in one scope may know the same peer. Unrelated providers, local identity
  spaces, and scopes do not merge merely because IDs or names look alike.
- `DisplayName` is optional normalized presentation supplied by the adapter, not
  a push-name or business-name contract. Common code does not select between
  provider-specific raw name fields.
- A nonempty live name may refresh a known display name; a later empty name does
  not erase it. Name-update time changes only when the value changes. Inert
  history and directory imports only fill an unknown name.
- `FirstSeen` is the first allowed live inbound in the scope and is claimed once;
  `LastSeen` advances on allowed live inbound. Both remain unset when import or
  API activity merely ensures the identity. Root time values use the existing
  zero/unset convention; adapters encode absence appropriately.
- Filtering precedes recording. Ignored traffic does not create a peer, claim
  first-seen, or enter graph execution.

### Store operations

`PeerStore.EnsurePeer` accepts a scope, valid `IdentityKey`, and observation time.
It creates or finds the identity without claiming live activity or creating a chat.
`PeerStore.RecordInbound` accepts the same scoped key, normalized display name,
and live-inbound time, returning the peer and whether first-seen was claimed.

The claim is a conditional durable update, not a comparison of timestamps. Activity,
name update, and first-seen claim commit together; rollback leaves no claim behind.
Two events with the same timestamp still claim first only once (ADR 0045).
Lookups and mutations validate scope and the complete key, never a bare subject or
an internal ID from another scope.

Chats belong to targets, not peers. `Chat.PeerID` is an optional explicit
participant relation; incoming sender identity is independently recorded. Ensuring
an API destination need not register a recipient when no participant is known.
When a valid known recipient is supplied, it can be ensured without live activity;
the actual destination remains separate (ADR 0051).

### Runtime and events

The live sender supplies the read-only `peer` template values: `id`, `provider`,
`subject`, and `name`. `id` is the stable internal identity ID rendered as an opaque
string; `subject` remains opaque. The stored name fills an absent live name.
The peer root does not denote an alternate outbound destination. Approved legacy
translation is per consuming bot (ADR 0018/0020), not a phone field in the context.

`PeerFirstSeen` names the first-live-inbound notification. Directory membership,
sharing a contact card, and API sends do not trigger it. Renaming its Go types does
not change public event topics or signed retry bytes without ADR 0045's explicit
compatibility decision.

## Consequences

- Contact-card entries describe payload values, not peer registrations or directory
  memberships. Sharing a card creates neither a chat nor first-seen activity.
- Directory-derived properties report historical import provenance, not permission
  or proven current account membership (ADR 0037).
- Chat activity, holds, guards, and bounded graph execution resolve by full target
  (ADR 0026), even when one sender participates in several conversations.
- Shared-conversation execution remains refused until its policy is approved.
  Recording a participant as if the shared thread were their direct chat is not
  a neutral implementation.
- Read projections expose normalized identities/presentation without universal
  phone, push-name, or business-name properties. Public compatibility and personal
  data retention remain separate owning decisions, not permission for this design
  task to rewrite outputs or implement runtime behavior.
