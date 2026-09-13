# ADR 0013 — Sender identity: PN and LID

Status: Accepted (2026-09)

## Context

WhatsApp is migrating from phone-number JIDs (`user@s.whatsapp.net`, PN) to
hidden user JIDs (`user@lid`, LID). An inbound message may be addressed by
either form, and the alternative address is not always present.

Sessions and domain logic need a stable sender identity, and business flows
need the phone number (for example, to match an order). Keying a session on
whatever JID arrived would split the same person into two.

The Phase 0 spike observed a direct message arriving as
`212150010273830@lid` while the account itself was known by its PN.

## Decision

The channel adapter resolves the sender to a phone number when possible, in
this order:

1. The JID itself when its server is `s.whatsapp.net`.
2. The message alternative address (`SenderAlt`).
3. The LID store (`Store.LIDs.GetPNForLID`), which whatsmeow persists from
   history sync and mapping events.

The session key is the resolved PN. When no PN is known yet, the session is
keyed by the LID and marked unresolved; a mapping learned later reconciles
it to the PN.

Each session stores both identifiers when known. Replies go to the JID the
message arrived from (LID or PN), never to a fabricated address.

Groups are not covered here: the sender is the participant, and the chat is
the group.

## Consequences

- One session per person despite the LID/PN split.
- Domain logic can rely on a phone number.
- A first message from an unknown contact may open an unresolved session
  until the LID store catches up.
- The identity rule lives in the channel adapter; the domain sees a resolved
  `sender` (PN, plus LID when known).
