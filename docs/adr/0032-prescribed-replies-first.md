# ADR 0032 — Prescribed replies first, generation opt-in

Status: Accepted (2026-09)

## Context

Reply realization by a generative model reintroduces what the project avoids:
hallucination, cost, latency, and prompts that must be reviewed and versioned.
It is also often a patch for weak routing: when the model does not know what
the user meant, the reply is improvised instead of selected.

Tolerance of paraphrase is a property of routing, not of writing. With a
calibrated System One (ADR 0030) the graph already knows which intent won, so the
reply for that intent is already the right one. Two problems often attributed
to prescribed text have answers without generation: repetition is handled by
`body` variants chosen at random, and tone is handled by routing to a node
whose prescribed body fits.

## Decision

The recommended and default reply path is prescribed. A node carries `body`
messages, optionally as a list of variants; the graph, guided by the System One model,
selects which node and therefore which prescribed reply goes out.

- A bot with `system_one` and no `llm` is the recommended shape: AI routing,
  prescribed replies.
- Tone and register are chosen by routing, not by generation. A decision
  question (a score or a noul) may drive the choice, but the reply is still a
  prescribed body. Selecting a variant with a decision instead of routing to a
  node is an open decision; the first shape is separate nodes (ADR 0015).
- LLM realization is an explicit, opt-in extension (Phase 8) for the residual
  cases: open-ended repair, multiple languages, and summaries for a human. It
  is never the default and never required for the Phase 8 gate.
- When realization is used, the anchored mode (`body` plus `intent`) is
  preferred: the body is the factual anchor and the fallback. Intent-only
  messages, with no anchor, are discouraged.
- LLM tool calling (ADR 0008) is part of the same opt-in extension. The core
  path calls business APIs through deterministic `request` steps.

## Consequences

- The default path cannot hallucinate, is auditable, cheap, and testable from
  the graph alone.
- Graph authors write the messages; the System One model handles paraphrases and picks the
  branch. This is the project's identity, not a fallback.
- A bot that needs free-form text, multilingual replies, or LLM tools opts in
  and accepts the cost and the review that come with it.
- The three reply modes of ADR 0003 are re-weighted: prescribed `body` is the
  default, anchored is the sanctioned generative mode, and intent-only is
  discouraged.
- Capability expectations are narrower by design: this is a flow bot, not open
  chat. A later phase may revisit it if users ask for free generation.
