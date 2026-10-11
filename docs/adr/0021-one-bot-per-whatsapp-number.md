# ADR 0021 — One bot per normalized account identity

Status: Accepted (2026-09)

## Context

Several transport clients can reach the same external account. Two independent
bots owning that identity could receive the same traffic and send conflicting
replies. Physical resource sharing does not establish shared account ownership
or a graph-routing policy.

## Decision

One normalized own `IdentityKey` may belong to only one `(scope, bot)` in an
instance (ADR 0051). Compare the complete provider/subject key, including the
adapter's durable local-space normalization, not a phone, display name, device
locator, or active backend profile. Equal-looking local IDs in unrelated spaces
are not the same identity. An adapter cannot evade ownership by assigning a new
key to another client of the same external identity.

For WhatsApp the adapter's normalized own subject is PN digits. Its legacy pair
command rejects an already-owned identity and startup rejects duplicate records;
replacement under the same bot preserves the explicit force operation. Conflict
responses must not reveal another scope or bot (ADR 0044). Core code has no phone
getter or phone-specific ownership error contract.

The following removal behavior describes the existing WhatsApp adapter, not a
universal instruction to delete a remote profile or revoke its credentials. P3
owns neutral bind/unbind outcomes, generations, and lifecycle behavior (ADR 0053).

`unpair` and `bot pair --force` run the same removal: the linked-account row,
the pairing status, and the channel's device row with everything that hangs off
it — its keys, sessions, app-state cache, hidden-user map, contact cache, and
message secrets. The bot's queued replies end failed with the reason, so a
re-pair never sends them from another account. Botflota's own contacts, chats,
sessions, messages, and imported contacts are never touched.

The phone keeps listing the removed companion until it is unlinked there, so a
re-pair adds a linked device instead of replacing one. A fresh registration is
also the only way to change what the registration carries, such as the requested
history window (ADR 0035), because WhatsApp reads it once, at linking time.
Remote linked-device removal remains an explicit operation in WhatsApp.

## Consequences

- A fleet has one clear owner for each WhatsApp account.
- Existing duplicate records must be resolved before the fleet starts.
- Intentional multi-bot use of one number is not supported; it needs a future
  shared-account channel design with explicit routing and deduplication.
