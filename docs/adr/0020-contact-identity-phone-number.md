# ADR 0020 — WhatsApp identity and address normalization

Status: Accepted (2026-09); P1 boundary aligned and implemented on 2026-10-11.
The per-bot WhatsApp template alias is P2 work; implementation is tracked in
[the channel phases](../channels/phases.md).

## Context

WhatsApp can identify an incoming participant with a phone-number JID (PN) or a
hidden-user JID (LID). The current adapter preserves a PN-based subject. That is
its normalization policy, not a universal identity or destination rule. An SDK
address, account-device locator, sender, and conversation are distinct values.

## Decision

The WhatsApp adapter emits `IdentityKey` with provider `whatsapp` and PN digits as
its opaque subject (ADR 0051). These subjects are provider-wide and need no local
space prefix. The core does not validate digits, strip JID suffixes, expose a phone
getter, or treat opaque subjects as telephone metadata.

The JID and LID stay inside the WhatsApp adapter:

- Resolve the PN from a phone-number JID, then the message alternative address,
  then the persisted LID mapping store.
- If the existing PN policy cannot normalize a sender, refuse it before graph
  routing and log the unresolved condition without opening a fabricated peer or
  graph run. Supporting another canonical subject requires a separate adapter
  normalization decision, not an undocumented fallback in common code.
- Map the actual conversation address to `ChatTarget` independently of the sender.
  In this adapter a direct PN thread can equal its participant's subject because
  the adapter establishes that mapping. Generic code never derives one from the
  other. A group thread is not its participant's identity.
- Delivery resolves the saved target under the current account's adapter-owned
  state. Cached SDK addresses may help, but restart cannot require a last-sender
  cache in the engine or fleet. Access references and device locators are not
  common target fields.
- Own account identity is normalized separately from the companion-device JID;
  its private SDK lookup remains adapter-owned (ADR 0051).

### No legacy template translation

The retired `contact.*` template root has no alias. Old `contact.id/name/phone`
references are not translated to `peer.*`, no `phone` value is seeded into the
common context, and no adapter-declared reference mapping exists (ADR 0018,
PLAN.md "no backward compatibility"). A reference to the old root fails
validation as an unknown root; it is never silently empty.

Other legacy selectors, outputs, archives, and setup/result formats are removed
by their owning phase the same way; none of them gets a compatibility
translation or a duplicate account/setup implementation.

## Consequences

- Graph runs, holds, and guards use chats, not PN subjects (ADR 0026).
- PN-shaped opaque values preserve current direct-chat behavior without making
  other providers fabricate telephone data.
- The SDK's LID mapping remains necessary and private. Unresolved traffic is not
  silently assigned a different canonical identity.
- Groups remain explicitly filtered/refused until common execution semantics are
  approved; a participant must not stand in for the group conversation.
- The contact-card `phone` label is unrelated payload data. Its value neither
  selects a peer nor determines a conversation or permission to send.
