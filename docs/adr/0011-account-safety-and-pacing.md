# ADR 0011 — Account safety and human-like pacing

Status: Accepted (2026-09)

## Context

whatsmeow is an unofficial client. WhatsApp bans accounts that look
automated: high volume, instantaneous replies, identical text, unsolicited
outreach, and freshly paired accounts that send immediately. The protocol
choice does not mitigate this; behavior does.

Several measures already exist across the docs but are scattered: one actor
per bot serializes work (`docs/architecture.md`), the outbox paces sends
with `queue_delay`, actions support cooldowns, and messages support random
variants. Others are missing: presence and typing, read receipts, randomized
pacing, a reconnect policy, and a global send ceiling.

## Decision

Treat account safety as a first-class, testable policy, not an operational
afterthought:

- Serialize per bot: one actor handles inbound and outbound; a bot never
  processes or answers two messages at once.
- Pace outbound through the per-bot outbox FIFO. Between messages wait
  `queue_delay`, so no fixed interval becomes a fingerprint.
- Add one shared `jitter` to every pacing delay, drawn again per use as a
  random value in [0, jitter]; the configured delay stays the floor.
- Simulate human presence when enabled: wait `read_receipt_delay` and mark the
  inbound message as read (`read_receipts`), then wait `reply_delay`, showing a
  composing indicator when `simulate_typing` is on, before the reply.
- Vary reply text: variant lists resolve at random (already in the model).
- Rate limits: per-sender guards limit repetition (`cooldown` for a minimum
  interval, `rate_limit` for a maximum per window); an optional
  `max_messages_per_minute` caps a bot as a safety valve (`0` = no limit).
  Guard state is kept per chat, not per session, so it outlives session expiry
  (ADR 0027).
- Reconnect with capped exponential backoff plus jitter. On logout or a ban
  signal, stop the bot, keep its session, and surface the state in `bot list`
  instead of retrying forever.
- Never initiate unsolicited bulk conversations. Bots reply to inbound
  messages, or drive a flow the user started.
- Log every pacing decision (delay, jitter, presence) for audit.

## Consequences

- Bots are intentionally slower and slightly less predictable. That is a
  feature, not a bug.
- Tests must control randomness: the clock, RNG, and sleep are injected, and
  fixtures run with jitter and presence disabled so conversations stay
  reproducible.
- New bot settings: `read_receipts`, `read_receipt_delay`, `reply_delay`,
  `simulate_typing`, `queue_delay`, `jitter`, and `max_messages_per_minute`.
- Account warmup, daily caps, and fleet-level anomaly detection are deferred.
  Phase 9 covers the human side in the operational notes.
- No measure eliminates risk: WhatsApp can still ban an account. The
  `MessageChannel` port keeps room for the official Cloud API (ADR 0004).
