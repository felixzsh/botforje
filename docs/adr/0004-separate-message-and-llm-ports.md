# ADR 0004 — Messaging, decision, and generation are separate ports

Status: Accepted (2026-09)

## Context

A bot always needs a messaging transport. It may also use a decision model
(System One) to route, and optionally a generative model to read free text or
write replies. These are independent concerns with different vendors, failure
modes, and lifecycles. Conflating them would tie the engine to WhatsApp and one
model vendor at once.

## Decision

Ports, defined once in the core package so adapters implement them without
importing the engine (ADR 0034):

- `MessageChannel`: connect, disconnect, send, and a stream of inbound
  messages. WhatsApp is reached through whatsmeow today, and can be reached
  through the official Cloud API later.
  A channel is one bot's logical binding view, not an exclusive socket or
  process. Connect/disconnect operate that view; an HTTP source needs no
  persistent connection. Adapter-owned assembly may share physical resources,
  but disconnecting one view must not shut down another binding's view.
- `AccountLifecycle`: readiness, supported setup methods, and local detachment
  of a bot's channel account. Interactive setup is a separate session runner,
  so the transport never pretends to implement every login method (ADR 0053).
- `SystemOneProvider`: typed decisions with calibrated confidence for routing,
  tool selection, and value mapping (ADR 0030). It never generates text.
- `LLMProvider`: reply realization and free-text extraction for the tool
  pipeline (ADR 0033). It never decides a transition.

Ports stay separate from config and persistence. Core code never imports
whatsmeow, a System One or LLM SDK, YAML, or SQL.

An inbound handoff is not proof of durable processing. A source that confirms
updates through a cursor or HTTP response gets an optional completion outcome
for each delivered entry; a source that needs none does not use one. The
outcome is one of four: committed (the turn, its messages, and its queued
replies were durably committed), already-recorded duplicate, deliberate discard
under a current policy, and failed processing. Committed, duplicate, and
deliberate discard let the adapter confirm its own update; a failure never does,
and a cumulative cursor never advances past a failed entry. Handing an entry to
a goroutine, channel, or queue confirms nothing, and cancellation, shutdown, or
a missing outcome is not success. A held inbound confirms after its history is
stored without running the graph, and confirming a turn does not wait for its
replies to leave the outbox. The adapter translates the outcome into its
protocol, for example an HTTP response or a cursor. Message identity and
provider update IDs or cursors stay separate values (ADR 0026, ADR 0040). No
generic durable inbox or provider command port is approved, and confirming
processing promises no exactly-once execution of external side effects: a
replay after a failed commit can repeat a `request` step.

## Consequences

- The engine is testable with fake channel, decision, and generation ports.
- A new channel, decision model, or LLM is a new adapter, not a rewrite.
- A bot with no model runs deterministically; each model port is optional, the
  channel is not.
- Provider API drift stays inside `adapters/`.
