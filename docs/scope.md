# Scope

What Botflota includes.

Botflota owns execution and a generic control plane, not a host application.
`integration.md` describes the separation (ADR 0046). Scopes and tokens work
without an external account system; YAML publication is a runtime capability.
An optional future dashboard serves operators, not a required host experience.
Visual authoring and assisted onboarding belong to independent applications.
Resource policy and untrusted-definition isolation remain TBD, not guarantees
of the current scope model.

A config-driven fleet runner with these capabilities, without a dashboard or a
hosting platform:

- One instance runs multiple WhatsApp bots.
- Bots, graphs, and actions defined in YAML; YAML is the behavior source of
  truth.
- Graph conversations with sessions persisted in SQLite.
- Actions with `message`, `request`, `location`, `media`, `contact`, and `poll`
  steps; cooldown guards.
- Filters (groups, allow/ignore contacts) and a per-bot outbox with
  configurable delay and jitter, plus human-like presence.
- CLI: `config` and `bot` groups, plus `fleet`.
- REST API with token-based auth.
- Hybrid AI support designed in from the start: deterministic routing works
  with no model configured; System One (Jev) routes and decides tools,
  prescribed replies are the default, and an LLM reads free text and realizes
  replies only when enabled.
