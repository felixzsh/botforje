# ADR 0010 — Node memory and routing across visited nodes

Status: Accepted (2026-09)

## Context

Conversations often need to return to earlier nodes ("menu", "back") without
declaring the same edge in every node. Matching input against the edges of
previously visited nodes solves this and has proven useful in practice, so
it is adopted here with explicit rules.

## Decision

Each session keeps an ordered list of visited nodes, most recent last,
including the current node at the end. Visits are reordered, never reset by
navigation: returning to the root does not clear the list. The list is
persisted with the session.

Deterministic matching considers candidate nodes in this order:

1. Visited nodes, oldest to most recent.
2. The current node, last.

Every edge with `triggers` in those nodes is fuzzy matched. The best score
wins, regardless of which node it came from; ties go to the later candidate
(the current node, then the most recent). The default edge is only taken from
the current node. If nothing matches and LLM routing is enabled, the agent
chooses among the same candidates by `intent`; otherwise the graph `fallback`
applies.

On session creation, the incoming message is evaluated before the root's
entry actions: if it transitions, the root message is skipped; if not, the
root's `on_enter` runs.

The visited list is also passed to the agent as conversation context,
bounded to a configured maximum.

## Consequences

- "Back" navigation works without duplicating edges in every node.
- Routing depends on the visit path: tests must cover the same input with
  different histories.
- The memory is small (node names) but must be persisted and capped for
  prompt use.
- No `common_edges` mechanism is needed; this supersedes it.
