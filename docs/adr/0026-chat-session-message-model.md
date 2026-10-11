# ADR 0026 — Chat, graph run, and message model

Status: Accepted (2026-09); P1 design aligned and implemented on 2026-10-11.
The account-scoped durable dedup key stays a P5 decision; implementation is
tracked in [the channel phases](../channels/phases.md).

## Context

Durable dialogue history and bounded graph execution have different lifetimes.
The old implementation used phone-based contact relationships and called bounded
execution a session. Neither a sender's identity nor an SDK session defines the
conversation that owns the history.

## Decision

Three concepts, with the identity and destination values of ADR 0051:

- **Chat** — the durable conversation for one bot, unique by `(scope, bot,
  provider, kind, thread, topic)`. It never ends and owns history, holds, and
  guards. Its `Target` is independent of the sender.
- **GraphRun** — one bounded execution of a graph for a chat, starting at the
  root and ending on the graph timeout or `default_timeout`. A chat has many
  `GraphRuns`. Bind attempts and SDK sessions are different concepts.
- **Message** — one retained inbound or outbound message. It belongs to a history
  chat, may reference the graph run that handled it, and may carry a known sender
  independently of the chat's participant.

### Relationships and persistence

Physical table names belong to the SQLite adapter, not to the domain vocabulary.
The logical relationships are:

- Peers are unique by scoped provider/subject (ADR 0029).
- Chats store the complete target and an optional peer reference. A direct target
  without a known participant is valid; an unknown participant is SQL NULL and
  zero in the Go internal-ID convention. Do not create a peer from a thread.
- Graph runs reference chats and store node, visited nodes, graph/node context,
  creation/update times, and expiry. The newest run is the current execution;
  expiry starts a new run instead of overwriting history.
- Messages reference their history chat and optionally a graph run and known
  sender. Deleting a chat cascades to its graph runs and messages; deleting a
  graph run clears its message references, not the messages themselves.
- All references resolve under their scope and bot as applicable. A valid foreign
  key alone does not permit a peer or chat from another scope.
- Topic absence is stored canonically as an empty value, so SQL uniqueness matches
  `ChatTarget` equality (ADR 0051).

An allowed live inbound resolves its sender identity and full target separately.
Execution, holds, and guards resolve by chat, never by the sender's last chat.
A peer-only lookup cannot return the unique conversation: one peer may have many
chats with the same bot. Shared conversations remain filtered/refused until their
runtime policy is approved (ADR 0051).

### History association versus actual destination

An inbound belongs to the conversation it arrived on. A normal reply targets that
conversation. An explicit alternate destination keeps the triggering chat's
history association, but its outbound record and queued delivery also carry the
exact destination. Never substitute the history chat's target for that destination.

`provider_message_id` is opaque. It is interpreted in its actual destination and
provider message space, not globally and not in the triggering history chat when
those differ. Source update IDs and receive cursors are different adapter values.
P1 tests overlapping source/destination IDs and restart with the persisted target.
Full account-local/replacement provenance and the final durable dedup key remain
P5 decisions; this ADR does not invent a namespace column or equate that space
with binding generation (ADR 0051).

### Recording semantics

`ingest_kind` records how data entered the runtime: live, sync, API, or unknown.
History import is inert: it creates no live activity or first-seen claim.
Unknown history senders remain unknown, not invented peers.

Origin records known provenance, not inferred human authorship. Imported outbound
bodies remain unknown; a body actually produced by the model is LLM-origin, a kept
anchor is preset, and an API send is API-origin. Stronger external-send attribution
and pause policy are reviewed in P5 with ADR 0043.

Routing metadata records the engine's actual choice. The `TurnStore` transaction
commits the inbound, graph-run update, outbound records, and queued deliveries
(ADR 0040); it is not another table-shaped port.

## Consequences

- `GraphRunStore`, `GraphRunID`, `TurnRecord.GraphRun`, and `HistoryCounts.GraphRuns`
  name graph execution explicitly; bare `Run`/`Runs` are not replacements.
- One sender in two supported targets has independent execution and guard state.
- The engine records messages through the same scoped stores; an internal rename
  does not authorize changing JSON keys, routes, event topics, or archive names.
- The pre-release schema may be rebuilt explicitly under ADR 0006. P1 must prove
  fresh-schema constraints and a real database close/reopen without sender caches;
  the design is not evidence that those gates already pass.
