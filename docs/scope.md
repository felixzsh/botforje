# Scope

What the MVP includes. Everything else is deferred (`../PLAN.md`).

A config-driven fleet runner with these capabilities, without dashboard or
SaaS infrastructure:

- One instance runs multiple WhatsApp bots.
- Bots, graphs, and actions defined in YAML; YAML is the behavior source of
  truth.
- Graph conversations with sessions persisted in SQLite.
- Actions with `message`, `request`, and `location` steps; cooldown guards.
- Filters (groups, allow/ignore senders) and a per-bot outbox with
  configurable delay and jitter, plus human-like presence (ADR 0011).
- CLI: `config` and `bot` groups, plus `fleet`.
- REST API with key-based auth.
- Hybrid LLM support designed in from the start: deterministic routing works
  with no LLM configured; intents, providers, and tools are
  first-class when enabled.
