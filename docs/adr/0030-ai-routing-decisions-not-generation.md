# ADR 0030 — AI routing is decisions, not generation

Status: Accepted (2026-09)

## Context

The LLM path takes part in routing only when deterministic matching finds
nothing in `fallback` mode, or always in `always` mode (ADR 0015, ADR 0019).
It asks a generative model to pick a transition and returns free text that has
to be parsed, validated against the edge set, and tolerated when it drifts.
It also gives no calibrated uncertainty: a wrong route and a certain one look
the same.

A System One model is a different tool. It answers typed questions —
a choice from a closed set, a score on a rubric, or a probability — with
calibrated confidence and no generated text. TypeSafe calls this class System
One and ships Jev as its first model, behind one endpoint
(`POST /v1/systemone`).

The graph stays the core: a model may choose among declared edges, never
replace the state machine.

## Decision

AI routing is decision-only, and System One is the project's main and
recommended AI path. A System One model is the only model that takes part in
routing; a generative LLM never decides a transition.

- Triggers and the default edge (ADR 0019, ADR 0010) remain the first line and
  work with no provider configured.
- Two ports replace the single `LLMProvider` of ADR 0004:
  `SystemOneProvider.Decide` returns a typed decision with confidence, and
  `LLMProvider` keeps reply realization and the tool loop only.
- A decision request carries one question per candidate edge intent, as a
  choice from the closed set of intents. The winner, the tie-break, and the
  default edge stay in code.
- A confidence threshold gates acting: at or above it the transition is taken;
  below it the graph takes the default edge or escalates to a human.
- The first adapter is Jev; the port stays open to any provider that returns
  typed decisions.

## Consequences

- A decision cannot invent a transition or a format: the options are the edge
  intents, declared in config.
- Confidence becomes a routing signal, so a low-confidence match can defer
  instead of guessing.
- The deterministic path stays complete: a provider that is down, slow, or
  rate-limited degrades to triggers and the default edge.
- The LLM keeps what it is good at: realizing reply text and calling tools,
  and only as an opt-in extension (ADR 0032). It is no longer a classifier.
- The provider is English-first and new; the threshold and the phrasing of
  each intent need tests against real conversations.
- Amends ADR 0004 (port split) and ADR 0015 (the routing actor). Where the
  configuration lives is ADR 0031; how replies are produced is ADR 0032, and
  how tools run is ADR 0033.
