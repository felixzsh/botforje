# ADR 0020 — Contact identity is the phone number

Status: Accepted (2026-09)

## Context

WhatsApp is migrating from phone-number JIDs (`user@s.whatsapp.net`, PN) to
hidden user JIDs (`user@lid`, LID). An inbound message may be addressed by
either form, and the alternative address is not always present. Sessions and
business flows need a stable identity, and the phone number keys sessions,
filters, logs, and templates.

An earlier shape keyed unresolved sessions on the LID and carried the LID (and
the arrival JID) through the domain. That leaked WhatsApp addressing into logs,
filters, templates, and the session table.

## Decision

The domain keys a contact by the provider-scoped subject of ADR 0051; for
WhatsApp the subject is the phone number in plain digits (for example
`5215500000000`). It keys sessions, matches the bot's filter, appears in
logs, and is what templates read as `contact.phone`.

The JID and the LID live only inside the whatsapp adapter:

- At parse time it resolves the phone number in this order: the JID itself
  when its server is `s.whatsapp.net`, then the message alternative address
  (`SenderAlt`), then the LID store (`Store.LIDs.GetPNForLID`), which
  whatsmeow persists from history sync and mapping events.
- If no phone number can be resolved, it drops the message and logs a warning;
  it never opens a session keyed on a LID.
- For delivery it maps the recipient's phone number back to a transport
  address: the JID the message arrived from when known, otherwise the
  phone-number JID. The transport address is never logged or shown to
  templates.

Groups are handled separately: the contact is the participant, and the chat is
the group.

## Consequences

- Logs and stored data carry the subject; for WhatsApp that is the phone
  number, so no LID leaks. The `@s.whatsapp.net` suffix is not logged either;
  it is added only when building a transport address.
- A message whose phone number is unknown is dropped (rare, and the LID store
  fills the mapping in from history sync). Losing it is preferred over keying
  anything on a LID.
- `contact.lid` is not part of the template context and the session does not
  store a LID.
- The LID store remains necessary, but only for resolution inside the adapter.
