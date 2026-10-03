# ADR 0011 — Account safety and human-like pacing

Status: Accepted (2026-09)

## Context

whatsmeow is an unofficial client. WhatsApp bans accounts that look automated:
high volume, instantaneous replies, identical text, unsolicited outreach, and
freshly paired accounts that send immediately. The protocol choice does not
mitigate this; behavior does.

Account safety is a pacing policy on the outbound path. It must not throttle
the engine: a bot has to keep processing inbound while earlier replies wait out
their delays, especially with many bots and many messages per bot.

## Decision

Treat account safety as a first-class, testable policy:

- Within one bot, inbound handling and outbound sending run concurrently, each
  serialized on its own: the engine processes one message at a time and the
  outbox sends one message at a time. They do not block each other, so the
  engine keeps draining inbound while the outbox paces earlier replies.
- Pace outbound through the per-bot outbox FIFO. Between messages wait
  `queue_delay`, so no fixed interval becomes a fingerprint.
- Add one shared `jitter` to every pacing delay, drawn again per use as a
  random value in [0, jitter]; the configured delay stays the floor.
- Simulate human presence when enabled: wait `read_receipt_delay` and mark the
  inbound message as read (`read_receipts`), then wait `reply_delay`, showing a
  composing indicator when `simulate_typing` is on, before the reply.
- Vary reply text: variant lists resolve at random.
- Rate limits: per-contact guards limit repetition (`cooldown` for a minimum
  interval, `rate_limit` for a maximum per window); an optional
  `max_messages_per_minute` caps a bot as a safety valve (`0` = no limit).
  Guard state is kept per chat, not per session (ADR 0027).
- Reconnect with capped exponential backoff plus jitter. On logout or a ban
  signal, stop the bot, keep its session, and surface the state in `bot list`
  instead of retrying forever; 428 and 463 stop it too (ADR 0028).
- Never initiate unsolicited bulk conversations. Bots reply to inbound
  messages, or drive a flow the user started.
- Log every pacing decision (delay, jitter, presence) for audit.

Pacing is a configuration concern, not a scheduling one: the configured delays
are preserved exactly, but they live on the outbox worker so the engine stays
free.

## Consequences

- Bots are intentionally slower and slightly less predictable. That is a
  feature, not a bug.
- Throughput comes from concurrency between bots and between the engine and
  the outbox, not from shortening delays.
- Tests must control randomness: the clock, RNG, and sleep are injected, and
  fixtures run with jitter and presence disabled so conversations stay
  reproducible.
- New bot settings: `read_receipts`, `read_receipt_delay`, `reply_delay`,
  `simulate_typing`, `queue_delay`, `jitter`, and `max_messages_per_minute`.
- Account warmup, daily caps, and fleet-level anomaly detection are deferred.
  Phase 9 covers the human side in the operational notes.
- No measure eliminates risk: WhatsApp can still ban an account.
