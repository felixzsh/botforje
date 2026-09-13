# ADR 0008 — Nodes with parameterized tools

Status: Accepted (2026-09)

## Context

Giving the LLM only two roles (routing to a node and realizing reply text)
falls short of real conversations. Inside a state, the LLM may need to look
something up, ask a follow-up, or call a business API with concrete arguments
(for example, order two units of product X). Fixed entry actions and
text-only messages cannot express that.

## Decision

A node exposes tools the LLM may invoke while the conversation is in that
state. Each tool is an action with declared, validated `inputs`; an input may
bind to a node or graph variable to read and write it. Edges remain the only
allowed transitions, and the LLM never invents one.

The per-message tool loop, argument handling, error behavior, and bounds are
described in `docs/architecture.md`.

## Consequences

- Bots gain real tool-calling capability while state transitions stay
  explicit.
- Actions become tool contracts: `inputs` are part of their public API and
  need names and descriptions that read well to a model.
- New failure modes (loops, wrong arguments, side effects) must be bounded
  and logged; cost per message grows only where tools are enabled.
- `can_use` is replaced by `tools`, and `node.intent` becomes `node.goal`.
