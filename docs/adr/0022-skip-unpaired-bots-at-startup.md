# ADR 0022 — Skip unpaired bots at fleet startup

Status: Accepted (2026-09)

## Context

A configuration may define a bot before its WhatsApp account has been paired.
Trying to initialize that bot produces a connection error and can make startup
fail even when other configured bots are ready.

## Decision

BotForje checks each configured bot's local pairing before building the fleet:

- `not paired` and `unpaired` are recoverable states. BotForje logs a warning,
  does not build that bot's channel, engine, or outbox, and continues with the
  paired bots.
- Invalid configuration, duplicate ownership, database failures, and malformed
  stored pairings remain fatal startup errors.
- If no bot is paired, the fleet stays idle until it receives a shutdown signal.

## Consequences

- A bot can remain in the configuration while its pairing is completed later.
- The warning points to `bot pair`; its invocation details come from the usage.
- Pairing a skipped bot requires restarting the fleet; runtime reload is
  deferred to the reload phase.
