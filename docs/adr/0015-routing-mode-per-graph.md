# ADR 0015 — Routing mode can be overridden per graph

Status: Accepted (2026-09). The actor is the System One model (ADR 0030) and
the setting lives on `system_one` (ADR 0031); the modes and the override
stand.

## Context

The instance picks how the LLM takes part in routing: `off` (deterministic
only), `fallback` (the LLM resolves input that matches no trigger), or
`always` (the LLM decides even when a trigger matches). A single global
mode is too coarse: a compliance graph may want deterministic routing while
a sales graph wants the LLM to roam.

## Decision

`llms.<name>.routing` sets the default for bots using that llm. Each graph
may override it with `routing: off | fallback | always`.

- `off`: no LLM routing; transitions come from triggers and the default edge.
- `fallback`: deterministic matching first; the LLM resolves input that
  matches no trigger.
- `always`: the LLM decides the transition even when a trigger matches.

Edges remain the only transitions in every mode (ADR 0008). Reply realization
is a separate switch (`realization`), not part of this decision.

## Consequences

- Deterministic flows stay deterministic without disabling the LLM globally.
- Behavior differs per graph; tests must cover each mode per graph.
- The mechanics live in `docs/architecture.md`; this ADR fixes the override.
