# ADR 0030 — AI routing is decisions, not generation

Status: Accepted (2026-09)

## Context

A generative model asked to pick a transition returns free text that has to be
parsed, validated against the edge set, and tolerated when it drifts. It also
gives no calibrated uncertainty: a wrong route and a certain one look the same.

A System One model is a different tool. It answers typed questions — a choice
from a closed set, a score on a rubric, or a probability — with calibrated
confidence and no generated text. TypeSafe calls this class System One.

The graph stays the core: a model may choose among declared edges, never
replace the state machine.

## Decision

AI routing is decision-only, and System One is the project's main and
recommended AI path. A System One model is the only model that takes part in
routing; a generative LLM never decides a transition.

- Two ports, as ADR 0004 defines: `SystemOneProvider.Decide` returns a typed
  decision with confidence; `LLMProvider` keeps reply realization and the tool
  pipeline only.
- A decision request carries one question per candidate edge intent, as a choice
  from the closed set of intents. The winner, the tie-break, and the default
  edge stay in code.
- A confidence threshold gates acting: at or above it the transition is taken;
  below it the graph takes the default edge or escalates to a human.
- The port is vendor-neutral. Jev (TypeSafe) is the intended first adapter, but
  access and pricing are still to confirm; no code depends on it yet.

Routing modes. The default is set on the `system_one` profile (ADR 0031) and a
graph may override it:

- `off`: no AI routing; transitions come from triggers and the default edge.
- `fallback`: deterministic triggers first; System One resolves input that
  matches no trigger.
- `always`: System One decides before triggers, even when a trigger matches.

Edges remain the only transitions in every mode.

## Consequences

- A decision cannot invent a transition or a format: the options are the edge
  intents, declared in config.
- Confidence becomes a routing signal, so a low-confidence match can defer
  instead of guessing.
- The deterministic path stays complete: a provider that is down, slow, or
  rate-limited degrades to triggers and the default edge.
- The LLM keeps what it is good at: realizing reply text and reading free text
  for tools, and only as an opt-in extension (ADR 0032, ADR 0033).
- The threshold and the phrasing of each intent need tests against real
  conversations.
