# ADR 0022 — Skip unpaired bots at fleet startup

Status: Accepted (2026-09)

## Context

A configuration may define a bot before its WhatsApp account has been paired.
Trying to initialize that bot produces a connection error and can make startup
fail even when other configured bots are ready.

## Decision

Botflota checks each configured bot's local pairing before building the fleet:

- `not paired` and `unpaired` are recoverable account states, as are the
  not-ready readiness states ADR 0053 describes. Botflota logs a warning, does
  not build that bot's channel, engine, or outbox, and continues with the
  ready bots.
- Invalid configuration, duplicate ownership, database failures, and malformed
  stored pairings remain fatal startup errors.
- If no bot is ready, the fleet stays idle until it receives a shutdown signal.

## Consequences

- A bot can remain in the configuration while its pairing is completed later.
- The warning points to `bot pair`; its invocation details come from the usage.
- A bot paired while the fleet runs joins it immediately: the pairing session
  activates it through the fleet, so no restart is needed (ADR 0039). A bot
  paired while the fleet is stopped is built at the next start, as before.
