# ADR 0030 — AI routing is decisions, not generation

Status: Accepted (2026-09)

## Context

A generative model asked to pick a transition returns free text that has to be
parsed, validated against the edge set, and tolerated when it drifts. It also
gives no calibrated uncertainty: a wrong route and a certain one look the same.

A System One model is a different tool. It answers typed questions — a choice
from a closed set, a score on a rubric, or a probability — with calibrated
confidence and no generated text. Clef and Jev answer exactly that shape: one
state and a schema of typed questions in, one probability per allowed option
out, and nothing generated in between. They are interchangeable because the
wire is a published standard rather than a vendor API.

The graph stays the core: a model may choose among declared edges, never
replace the state machine.

## Decision

AI routing is decision-only, and System One is the project's main and
recommended AI path. A System One model is the only model that takes part in
routing; a generative LLM never decides a transition.

- Two ports, as ADR 0004 defines: `SystemOneProvider.Decide` returns typed
  decisions with confidence; `LLMProvider` keeps reply realization and the tool
  pipeline only.
- A decision request is one state plus one or more typed questions, answered in
  one call. A question is a `choice` over named options, a `score` over ordered
  options, or a `noul` — the probability that a statement holds. Every answer
  carries a probability for every allowed option. Routing asks one `choice`
  over the candidate edge intents, tool selection asks one `noul` per tool, and
  value mapping asks one `choice` per value. The winner, the tie-break, and the
  default edge stay in code. A call carries at most 64 questions and a `choice`
  at most 255 options: the intersection of the endpoints this project supports,
  measured rather than guessed (`odd/tasks/systemone-wire-spike.md`).
- The wire is a published standard, and it is what the port speaks: `POST
  /v1/systemone` with a `model`, a `state`, and a map of typed questions, and
  answers keyed by the same names. Clef states that its API is fully compatible
  with Jev and SystemOne, and any server speaking it is exchanged through
  configuration alone — `base_url`, `model`, and the key are profile fields, so
  nothing in the graph, the engine, or the config names a vendor. Decision
  models are always reached over an API, never run in-process. Jev is the first
  endpoint configured, and exchanging it for Clef is the test that the contract
  is real rather than a claim.
- The project uses the intersection of that standard: `noul`, `choice`, and
  `score` questions over a text `state`, with `instructions` as a string and
  `criteria` as option descriptions. Media inputs, asset placeholders, and
  `box` questions are outside it, so no endpoint configured here is required to
  support them.
- A confidence threshold gates acting, in this order: at or above it the
  transition is taken; below it the graph takes its default edge; with no
  default edge, its `fallback` node; with neither, it escalates. The gate reads
  the answer's own `confidence`, never the largest probability in
  `probabilities`: the two agree on clear input and diverge when the
  distribution is flat, which is exactly when deferring matters. The
  probabilities stay available for a margin or a tie-break.

Escalation. A graph may declare `escalate: <node>`. Escalating enters that node
and hands the chat to a person: the node's prescribed reply is queued and the
fleet holds the chat in `human` control (ADR 0043) once the turn commits, so no
automatic reply overtakes the person. The engine reports the escalation and
holds no control state of its own.

Routing modes. The default is set on the `system_one` profile (ADR 0031), and a
graph may override the mode and the confidence threshold:

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
- One round trip answers the whole question batch, so routing, tool selection,
  and value mapping share one call budget and one failure mode.
