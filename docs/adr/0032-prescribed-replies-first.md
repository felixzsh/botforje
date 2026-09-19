# ADR 0032 — Prescribed replies first, generation opt-in

Status: Accepted (2026-09)

## Context

Reply realization by a generative model reintroduces what the project avoids:
hallucination, cost, latency, and prompts that must be reviewed and versioned.
It is also often a patch for weak routing: when the model does not know what
the user meant, the reply is improvised instead of selected.

Tolerance of paraphrase is a property of routing, not of writing. With a
calibrated System One (ADR 0030) the graph already knows which intent won, so
the reply for that intent is the right one. Repetition is handled by `body`
variants chosen at random, and tone by routing to a node whose prescribed body
fits.

## Decision

Nodes describe a goal, not literal output. A message realizes a reply in one of
three modes, in order of preference:

- Prescribed: `body`, optionally a list of variants chosen at random. The
  default, and the only mode that needs no provider.
- Decision-selected: the System One model routes to a node whose prescribed
  body fits the intent or tone. The reply is still prescribed.
- Realized: the opt-in generative path; `body` plus `intent` anchors the model
  and doubles as the fallback. Intent-only, with no anchor, is discouraged.

- A bot with `system_one` and no `llm` is the recommended shape: AI routing,
  prescribed replies.
- Tone and register are chosen by routing, not generation. Selecting a variant
  with a decision instead of routing to a node is an open decision; the first
  shape is separate nodes.
- LLM realization is an explicit, opt-in extension (Phase 8) for open-ended
  repair, multiple languages, and summaries for a human. It is never the
  default and never required for the Phase 8 gate.
- Node policy is ambient; message policy refines it. A policy check runs before
  anything is queued.
- Tool calling follows ADR 0033: the LLM reads free text and extracts candidate
  parameters, which is not user-facing generation, and `request` steps remain
  for deterministic calls.

## Consequences

- The default path cannot hallucinate, is auditable, cheap, and testable from
  the graph alone.
- Graph authors write the messages; the System One model handles paraphrases and
  picks the branch. This is the project's identity, not a fallback.
- A bot that needs free-form text, multilingual replies, or LLM tools opts in
  and accepts the cost and the review that come with it.
- Every generated message is traceable: node, intent, provider, and policy are
  logged.
- Capability expectations are narrower by design: this is a flow bot, not open
  chat.
