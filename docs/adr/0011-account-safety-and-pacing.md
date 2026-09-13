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
  `queue_delay` plus a random `queue_jitter`, so no fixed interval becomes a
  fingerprint.
- Simulate human presence when enabled: mark the inbound message as read
  (`read_receipts`) and show a composing indicator for `typing_delay` before
  the reply.
- Vary reply text: variant lists resolve at random (already in the model).
- Rate limits: per-sender cooldown guards limit repetition, and an optional
  `max_messages_per_minute` caps a bot as a safety valve (`0` = no limit).
- Reconnect with capped exponential backoff plus jitter. On logout or a ban
  signal, stop the bot, keep its session, and surface the state in `status`
  instead of retrying forever.
- Never initiate unsolicited bulk conversations. Bots reply to inbound
  messages, or drive a flow the user started.
- Log every pacing decision (delay, jitter, presence) for audit.

## Consequences

- Bots are intentionally slower and slightly less predictable. That is a
  feature, not a bug.
- Tests must control randomness: the clock and RNG are injected, and
  fixtures run with jitter and presence disabled so conversations stay
  reproducible.
- New bot settings: `queue_jitter`, `simulate_typing`, `typing_delay`,
  `read_receipts`, and `max_messages_per_minute`.
- Account warmup, daily caps, and fleet-level anomaly detection are deferred.
  Phase 9 covers the human side in the operational notes.
- No measure eliminates risk: WhatsApp can still ban an account. The
  `MessageChannel` port keeps room for the official Cloud API (ADR 0004).
