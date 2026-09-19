# ADR 0010 — Visited nodes and routing

Status: Accepted (2026-09)

## Context

Conversations often need to return to earlier nodes ("menu", "back") without
declaring the same edge in every node. Matching input against the edges of
previously visited nodes solves this and has proven useful in practice, so
it is adopted here with explicit rules.

## Decision

Routing may consult previously visited nodes, not only the current one. Each
session keeps an ordered list of visited nodes with the current one last; it
is reordered, never reset by navigation, and used both for matching and as
LLM context. On session creation, the incoming message is evaluated before
the root's entry actions.

Candidate order, tie-breaking, and default-edge rules live in
`docs/architecture.md`.

## Consequences

- "Back" navigation works without duplicating edges in every node.
- Routing depends on the visit path: tests must cover the same input with
  different histories.
- The visited-nodes list holds node names with no duplicates, so it cannot
  exceed the number of nodes in the graph; it needs no separate storage cap.
  Bounding it when it is handed to a model as context is a prompt decision.
- No `common_edges` mechanism is needed; this supersedes it.
