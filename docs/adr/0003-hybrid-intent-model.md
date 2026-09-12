# ADR 0003 — Hybrid intent model

Status: Accepted (2026-09)

## Context

Fixed response strings are predictable, brittle to paraphrases, and make many
bots sound identical. Fully free LLM responses are hard to control, test, and
audit. Both extremes are bad for a framework that wants controlled behavior.

## Decision

A node describes a goal, not literal output:

- `goal`: what the agent must achieve in this state, named from its side
  (for example, "collect the shipping zip").
- `policy`: ambient constraints for the state: agent decisions, tool
  selection, and generated text.
- `tools`: actions the agent may invoke while in this node (ADR 0008).

Reply realization lives on messages and has three modes:

- `body` only: deterministic text. A list means random variants.
- `intent` only: the LLM realizes the reply from intent and policy. Without a
  provider the step fails; there is no fallback text.
- `body` plus `intent`: the LLM generates using `body` as the factual anchor
  within policy; if no provider is available, the call fails, or the policy
  check rejects the output, the resolved `body` is sent as the fallback.

Policies compose: `node.policy` always applies; `message.policy` is optional
and refines it for that reply, winning on direct conflict. Without
`message.policy`, the node policy is inherited.

`policy` without `intent` is invalid, and a variant list combined with
`intent` is invalid. Critical flows (prices, data, compliance) stay
deterministic or constrained to values from context.

## Consequences

- Repetition drops, paraphrase handling improves, and identical-message
  fingerprinting is reduced.
- LLM usage is optional per node and per instance; bots still run with no
  provider configured.
- Every generated message must be traceable: node, intent, tools called,
  provider, and policy version are logged.
- Testing needs two extra layers: classification cases and reply evaluations
  (golden conversations) on top of deterministic unit tests.
- Policies and prompts are versioned with the config and change under review.
