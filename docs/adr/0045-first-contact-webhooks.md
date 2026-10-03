# ADR 0045 — First-contact webhooks

Status: Accepted (2026-09)

## Context

ADR 0029 records a contact's first allowed inbound as the first-contact hook and
left delivery "as a later phase"; ROADMAP Phase 14 owns signed outbound
webhooks. An external system that hosts Botflota needs the fact in near real
time — to open a lead, notify a human, or sync its own records — without
polling the contact read (ADR 0044). Botflota must not know who consumes it, so
the webhook is generic, signed, and configured per instance; the engine cannot
import adapters (ADR 0034), so the hook crosses a port in the core.

## Decision

- **Configuration is instance-level**, in the main config file only, owner
  managed: a `webhooks` list of `url`, `secret`, and `events`. `secret` is a
  secret field, so `${ENV_VAR}` is allowed there (ADR 0014, amended). Changing
  the list is a restart-required global setting, like the other instance
  settings a reload never publishes.
- **One event today: `contact.first_seen`.** It fires exactly once per phone
  per scope, from the `first_seen` claim of the inbound write (ADR 0029): the first
  allowed inbound message, whether the graph handles it or a human hold
  suppresses it. It never fires on history import, contact import, an operator
  reply, or an API-origin send.
- **It is emitted when the profile is durable, not after the turn.** The
  contact row is written before the turn transaction and survives a failed turn
  (ADR 0040), while a later message would not report `created` again, so
  waiting for the commit could lose the event forever. Emission is synchronous
  and only records the event; the HTTP call is the worker's.
- **A core port carries the fact**: `FirstContactNotifier` with a
  `FirstContact` record (`Scope`, `Bot`, `Contact`, `At`). The engine calls it
  and knows nothing else; `webhook` (ADR 0034) implements it as the sink.
- **Delivery is durable, at-least-once, and outside the engine**: the sink
  builds one envelope per event and enqueues one delivery row per configured
  target whose `events` list matches, in SQLite. The worker polls due rows,
  POSTs them, retries network failures, `429`, and `5xx` with exponential
  backoff (base 5 s, doubling, capped at 15 min, at most 8 attempts), records
  `sent` or `failed`, and leaves the row for diagnosis. Rows never block each
  other; only a duplicate with the same `(event_id, url)` is refused.
- **The envelope is frozen at enqueue**:

      { "id": "<event uuid>", "event": "contact.first_seen",
        "occurred_at": "2026-09-28T20:24:20Z", "scope": "bardo",
        "data": { "bot": "bardo",
                  "contact": { "phone": "5218994251984", "display_name": "Ana" },
                  "first_seen": "2026-09-28T20:24:20Z" } }

  with headers `X-Botflota-Event`, `X-Botflota-Delivery` (the delivery row's
  uuid, stable across its retries), and `X-Botflota-Signature: sha256=<hex>`
  — HMAC-SHA256 of the raw body with the target's secret. A retry signs and
  sends the stored bytes unchanged, so the signature verifies.
- **The secret never rests in the database.** The row stores the payload and
  URL; the worker resolves the URL to the running configuration's secret at
  delivery time. A target the configuration no longer holds fails the delivery
  permanently.
- A webhook never blocks the engine: enqueueing is one local insert, and a
  failed enqueue is logged; delivery failures never touch a conversation. A
  consumer deduplicates by envelope `id`.

## Consequences

- A host gets the first contact without polling, and a redelivery or crash
  cannot duplicate an accepted event: the event id is generated once and
  enqueued idempotently.
- Delivery is at-least-once: a crash after the POST but before marking `sent`
  resends the same event id with the same delivery id.
- Removing a target from the config leaves its pending deliveries to fail,
  which is honest; restoring the same URL lets deliveries that have not been
  given up on proceed.
- ROADMAP Phase 14 widens the event catalog (bot lifecycle, counters,
  warnings); this ADR settles only the first-contact event and the mechanism
  every later event reuses.
