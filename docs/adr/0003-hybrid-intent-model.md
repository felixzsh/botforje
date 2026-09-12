# ADR 0003 — Hybrid intent model

Status: Accepted (2026-09)

## Context

Fixed response strings are predictable, brittle to paraphrases, and make many
bots sound identical. Fully free LLM responses are hard to control, test, and
audit. Both extremes are bad for a framework that wants controlled behavior.

## Decision

A node describes intent, not literal output:

- `intent`: what the node is for, for example `ask_shipping_zip`.
- `policy`: tone, constraints, and prohibitions for any generated reply.
- `examples`: phrases that lead to this node; also usable as fast-path match.
- `can_use`: actions the engine may run while in this node.

Routing and reply realization each choose a mode per node: deterministic
(text or variants), LLM-assisted, or both with deterministic fallback. A
policy check runs on generated replies before they are queued; a failed check
falls back to deterministic output. Critical flows (prices, data, compliance)
stay deterministic or constrained to values from context.

## Consequences

- Repetition drops, paraphrase handling improves, and identical-message
  fingerprinting is reduced.
- LLM usage is optional per node and per instance; bots still run with no
  provider configured.
- Every generated message must be traceable: node, intent, provider, and
  policy version are logged.
- Testing needs two extra layers: classification cases and reply evaluations
  (golden conversations) on top of deterministic unit tests.
- Policies and prompts are versioned with the config and change under review.
