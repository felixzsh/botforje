# ADR 0003 — Hybrid intent model

Status: Accepted (2026-09)

## Context

Fixed response strings are predictable, brittle to paraphrases, and make many
bots sound identical. Fully free LLM responses are hard to control, test, and
audit. Both extremes are bad for a framework that wants controlled behavior.

## Decision

Nodes describe a goal, not literal output, and messages realize replies in
three modes: deterministic (`body`), LLM-realized (`intent`), or anchored
(`body` plus `intent`, where the body is the factual anchor and the
fallback). Node policy is ambient; message policy refines it.

Field shapes, valid combinations, and the runtime flow live in
`config.schema.json` and `docs/architecture.md`.

## Consequences

- Repetition drops, paraphrase handling improves, and identical-message
  fingerprinting is reduced.
- LLM usage is optional per node and per bot; a bot with no `llm` runs
  deterministically (ADR 0017).
- Every generated message must be traceable: node, intent, tools called,
  provider, and policy version are logged.
- Testing needs two extra layers: classification cases and reply evaluations
  (golden conversations) on top of deterministic unit tests.
- Policies and prompts are versioned with the config and change under review.
