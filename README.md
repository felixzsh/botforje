# BotForje

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one instance runs a fleet of WhatsApp accounts
through the native Multi-Device protocol (`go.mau.fi/whatsmeow`), with no
browser automation.

## Principles

- Declarative first: behavior lives in YAML; changing a bot never means
  changing the runtime.
- One runtime: a single binary, no mandatory auxiliary processes.
- The graph is the core: the LLM drives and enriches conversations but never
  replaces the state machine.
- Config can be authored by hand, by a UI, or AI-assisted; the runtime does
  not change for any of them.

## Status

Pre-code; implementation has not started.

## Documentation

- `PLAN.md` — phases to the MVP, progress checkboxes, and open decisions.
- `ROADMAP.md` — phases after the MVP and the order they arrive in.
- `docs/README.md` — documentation map: references, developer docs, guide.
- `config/example.yml` — reference configuration.
- `CONTRIBUTING.md` — design principles.
- `AGENTS.md` — rules for agent-assisted development.
