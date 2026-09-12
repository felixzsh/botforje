# BotForje — Context

## What it is

A declarative WhatsApp bot framework written in Go. Behavior lives in YAML:
bots, conversation graphs, actions, and guards. The engine runs many WhatsApp
accounts (a fleet) in a single process through the native Multi-Device
protocol provided by `go.mau.fi/whatsmeow`.

This repository is the definitive BotForje, a from-scratch implementation
that supersedes `botforje-js` (Node.js + `whatsapp-web.js`). It is not a
port: the goal is to keep the good ideas (config-driven behavior, cooldowns,
queues, guards) and make better decisions from the start.

## Why Go and whatsmeow

- whatsmeow speaks the WhatsApp Multi-Device protocol natively over
  WebSocket. No Chromium, no Puppeteer, no xvfb, no browser process per
  account.
- Go fits the workload: many independent account actors in one process, each
  with bounded queues and controlled resource use.
- A single static binary keeps deployment and operations simple.

Details: `docs/adr/0001-go-and-whatsmeow.md`.

## MVP definition

A config-driven fleet runner roughly equivalent to `botforje-js` in
capability, without dashboard or SaaS infrastructure, and built on the new
architecture:

- One instance runs multiple WhatsApp bots.
- Bots, graphs, and actions defined in YAML; YAML is the behavior source of
  truth.
- Graph conversations with sessions persisted in SQLite.
- Actions with `message`, `request`, and `location` steps; cooldown guards.
- Inbound filters (groups, allow/ignore senders) and a per-bot outbox with
  configurable delay.
- CLI: `validate`, `auth`, `daemon`, `status`, `reload`.
- REST API with key-based auth.
- Hybrid LLM support designed in from the start: deterministic routing works
  with no LLM configured; intents and providers are first-class when enabled.

Not in the MVP: dashboard, behavior stored in a database, automatic graph
extraction from chat history, SaaS/login infrastructure, and channels other
than WhatsApp.

## Key decisions (short)

- Go + whatsmeow over Node.js + whatsapp-web.js. See ADR 0001.
- YAML is the behavior source of truth; SQLite stores runtime state. A later
  dashboard phase may migrate behavior to the database. See ADR 0002.
- Hybrid intent model: nodes describe intent, policy, examples, and allowed
  actions; replies are deterministic, LLM-realized, or both. See ADR 0003.
- Messaging channel and LLM provider are two separate ports. See ADR 0004.
- Scope is an explicit field per entity, not derived from file paths.
  See ADR 0005.
- SQLite is the only persistence technology in the MVP. See ADR 0006.
- Go structs are the source for the config schema; runtime validation is in
  Go and the JSON Schema is generated for editors. See ADR 0007.
- `botforje-js` is reference only: no code port, no fixture port.

## Current state

Pre-code. The config v1 shape is drafted in `config.example.yaml` and
`config.schema.json` (intents included); no implementation started.

## Glossary

- Bot — one WhatsApp account plus the graph that gives it behavior.
- Fleet — the set of bots a single BotForje instance runs.
- Scope — isolation namespace for entities; references never cross scopes.
- Graph — conversation state machine owned by one bot.
- Node — a state: intent, policy, entry actions, optional node context.
- Edge — transition between nodes, matched by intent, text, or condition.
- Action — reusable pipeline of steps (`message`, `request`, `location`).
- Step — single operation inside an action.
- Guard — rule that limits execution, for example a cooldown per sender.
- Session — per-sender conversation state stored in SQLite.
- Channel — messaging transport implementation (WhatsApp today).
- Provider — LLM implementation (cloud or local) used for routing and replies.
- Inbox — routes inbound events to the right bot and session.
- Outbox — per-bot FIFO queue that paces outbound messages.
