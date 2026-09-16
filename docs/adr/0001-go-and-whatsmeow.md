# ADR 0001 — Go and whatsmeow as the runtime foundation

Status: Accepted (2026-09)

## Context

Driving WhatsApp through a browser session per account costs hundreds of MB
of RSS and several seconds of startup per account, needs a pile of system
libraries, and makes large fleets expensive to host.

Botflota must run many WhatsApp accounts from one process with predictable
resource use.

## Decision

Build the runtime in Go and use `go.mau.fi/whatsmeow`, which implements the
WhatsApp Multi-Device protocol natively over WebSocket, Noise, and Signal
encryption. No browser is involved.

## Consequences

- Single static binary; deployment is copying a file and running it.
- Per-account cost drops to the order of tens of MB and near-instant startup,
  so one process can host a fleet.
- Go concurrency gives a natural per-account actor model.
- The project owns protocol concerns: device store, reconnection, message
  deduplication, and event mapping.
- whatsmeow is an unofficial client. WhatsApp ban and ToS risk remains and is
  handled operationally (pacing, guards, no unsolicited bulk messaging), not
  by the protocol choice.
- Transport sits behind a `MessageChannel` port, so other channel
  implementations (for example the official Cloud API) or messaging
  providers can be added later (see ADR 0004).
