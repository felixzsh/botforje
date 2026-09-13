# ADR 0015 — Routing mode can be overridden per graph

Status: Accepted (2026-09)

## Context

The instance picks how the LLM takes part in routing: `off` (deterministic
only), `fallback` (the agent resolves input that matches no trigger), or
`always` (the agent decides even when a trigger matches). A single global
mode is too coarse: a compliance graph may want deterministic routing while
a sales graph wants the agent to roam.

## Decision

`llm.routing` sets the instance default. Each graph may override it with
`routing: off | fallback | always`.

- `off`: no LLM routing; transitions come from triggers and the default edge.
- `fallback`: deterministic matching first; the agent resolves input that
  matches no trigger.
- `always`: the agent decides the transition even when a trigger matches.

Edges remain the only transitions in every mode (ADR 0008). Reply realization
is a separate switch (`realization`), not part of this decision.

## Consequences

- Deterministic flows stay deterministic without disabling the LLM globally.
- Behavior differs per graph; tests must cover each mode per graph.
- The mechanics live in `docs/architecture.md`; this ADR fixes the override.
