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
updates through a cursor or HTTP response needs a narrow processing-completion
boundary before the preparation's P5 gate closes. Distinguish committed work,
already-recorded duplicates, deliberate discards, and failed processing from
provider transport acknowledgements and message identity. Exact signatures and
discard/failure semantics remain TBD in `docs/channel-plan.md`, section 7;
the four transport operations are not an assertion that no completion contract
will be needed. No generic durable inbox or provider command port is approved.

## Consequences

- The engine is testable with fake channel, decision, and generation ports.
- A new channel, decision model, or LLM is a new adapter, not a rewrite.
- A bot with no model runs deterministically; each model port is optional, the
  channel is not.
- Provider API drift stays inside `adapters/`.
