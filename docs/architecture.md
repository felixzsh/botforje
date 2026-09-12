# Architecture

Component boundaries, ports, and the lifecycle of a message. This document
describes intent; update it as the code exists.

## Ports and adapters

Two independent ports sit beside the config engine and persistence:

- `MessageChannel` — transport port. The first implementation is WhatsApp via
  whatsmeow. Other channels (Telegram, SMS) become future packages.
- `LLMProvider` — provider port for classification and reply realization.
  Cloud and local providers sit behind the same interface.

The config engine loads YAML into the domain model; the engine never sees
YAML. Persistence stores runtime state: device store, sessions,
conversations, and messages.

A messaging provider and an LLM provider are different things. A bot always
needs a channel; an LLM is optional and only used when configured.

## Domain model

- Bot — identity (account) plus settings and the reference to one graph.
- Graph — state machine with root, fallback, timeout, and a map of nodes.
- Node — intent, policy, examples, entry/exit actions, allowed actions, and
  optional node-local context.
- Edge — transition matched by intent, text, or condition. LLM routing
  resolves unmatched input when a provider is configured.
- Action — ordered steps (`message`, `request`, `location`) with guards.
- Scope — namespace applied to actions, graphs, and bots. References resolve
  only within the same scope.

## Message lifecycle

Inbound:

1. Channel receives an event and maps it to an `IncomingMessage`.
2. Inbox filters it (groups, allow/ignore lists, dedup) and routes it to the
   right bot and session.
3. Graph engine loads or creates the session and evaluates the current node's
   edges: deterministic match first, LLM intent routing as fallback.
4. Engine runs the transition: exit actions, target node, entry actions, and
   any allowed actions selected by the LLM.
5. Reply text is deterministic, LLM-realized within node policy, or both; a
   policy check runs before anything is queued.
6. Results are queued in the bot's outbox and the session is persisted.

Outbound:

1. Outbox paces messages with the bot's configured delay.
2. Channel sends them; failures are surfaced, never silently dropped.

## Concurrency

- One actor per bot: inbound events and sessions for a bot are serialized;
  different bots run concurrently.
- Bounded queues between inbox, engine, and outbox provide backpressure.
- No shared mutable state between bots; shared services are read-only or
  synchronized.
- A fleet supervisor starts, stops, and reports bot actors.

## Configuration pipeline

```
YAML -> structural validation -> typed model -> semantic validation -> engine
```

Structural validation uses `config.schema.json`. Semantic validation
(reference resolution, scope isolation, uniqueness, reachability) happens in
Go because a JSON Schema cannot express it.

## Explicitly avoided for now

- Graph databases. SQLite tables and YAML are enough.
- Code generation from the schema in either direction.
- Behavior stored in the database before the dashboard exists.
- Shared mutable state between bots.
