# BotForje

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one BotForje instance runs a fleet of WhatsApp
accounts through the native Multi-Device protocol (`go.mau.fi/whatsmeow`).
No browser, no Chromium, no Puppeteer.

BotForje is the definitive implementation of the idea behind
[`botforje-js`](../botforje-js) (Node.js + `whatsapp-web.js`). The rewrite
exists to run large fleets in one process with predictable memory use and no
browser instance per account.

## Status

Pre-code. Architecture, decisions, and the phase plan are being settled;
implementation has not started. See `PLAN.md` for the phases and
`CONTEXT.md` for the current state.

## Documentation

- `CONTEXT.md` — what BotForje is, key decisions, glossary.
- `PLAN.md` — phase-by-phase plan to the MVP.
- `docs/architecture.md` — components, boundaries, message lifecycle.
- `docs/adr/` — architecture decision records.
- `config.example.yaml` — reference configuration.
- `AGENTS.md` — rules for agent-assisted development.

## Scope

MVP: a config-driven fleet runner with multiple WhatsApp bots per instance,
SQLite-backed sessions, hybrid LLM support, a CLI, and a REST API.

Out of scope for now: dashboard, behavior stored in a database, automatic
graph extraction from chat history, SaaS infrastructure, and channels other
than WhatsApp.
