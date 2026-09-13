# BotForje

Declarative WhatsApp bot framework written in Go. Define bots, conversation
graphs, and actions in YAML; one instance runs a fleet of WhatsApp accounts
through the native Multi-Device protocol (`go.mau.fi/whatsmeow`), with no
browser automation.

The graph is the core: the LLM drives and enriches conversations but never
replaces the state machine.

## Status

Pre-code; implementation has not started.

## Documentation

- `CONTEXT.md` — identity, principles, MVP, decisions index, glossary.
- `PLAN.md` — phase-by-phase plan to the MVP.
- `docs/README.md` — documentation map: developer docs and the user guide.
- `config.example.yaml` — reference configuration.
- `CONTRIBUTING.md` — design principles.
- `AGENTS.md` — rules for agent-assisted development.
