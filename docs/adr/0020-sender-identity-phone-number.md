# ADR 0020 — Sender identity is the phone number

Status: Accepted (2026-09)

## Context

ADR 0013 keyed an unresolved session on the LID and carried the LID (and the
arrival JID) through the domain. That leaked into logs, filters, templates, and
the session table, and made LIDs visible everywhere even though a LID is only
WhatsApp's internal addressing. In practice a message almost always carries the
phone number in the JID server or `SenderAlt`, or the LID store already has the
mapping from history sync.

## Decision

The domain only ever sees the **phone number in plain digits** (for example
`5215500000000`) as a sender identity. That value keys sessions, matches inbox
filters, appears in logs, and is what templates read as `sender.phone`.

The JID and the LID live only inside the whatsapp adapter:

- At parse time it resolves the phone number from the JID server, then
  `SenderAlt`, then the LID store.
- If no phone number can be resolved, it drops the message and logs a warning;
  it never opens a session keyed on a LID.
- For delivery it maps the recipient's phone number back to a transport
  address: the JID the message arrived from when known, otherwise the
  phone-number JID. The transport address is never logged or shown to
  templates.

This supersedes the unresolved-session reconciliation of ADR 0013. The rest of
ADR 0013 stands: resolution happens in the channel adapter, groups are handled
separately, and the domain sees a resolved sender.

## Consequences

- Logs and stored data are phone-based; no LID leaks. The `@s.whatsapp.net`
  suffix is not logged either; it is added only when building a transport
  address.
- A message whose phone number is unknown is dropped (rare, and the LID store
  fills in from history sync). Losing it is preferred over keying anything on a
  LID.
- `sender.lid` disappears from the template context and the session no longer
  stores the LID.
- The LID store remains necessary, but only for resolution inside the adapter.
