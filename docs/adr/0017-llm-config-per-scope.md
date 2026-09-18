# ADR 0017 — LLM configuration lives in scoped llms

Status: Accepted (2026-09). Amended by ADR 0031: `llms` keeps realization and
`deciders` is added; the rest stands.

## Context

The instance had a single `llm` section. With scope as the isolation unit
(ADR 0016), one process serves several clients, and each wants its own
provider, model, and key (BYOK). A single instance-level section cannot
express that.

A graph plus an LLM plus settings is a bot. "Agent" is reserved for a
conventional external agent runtime, a possible future integration.

## Decision

Each scope holds a named `llms` catalog. A bot references one by name with
`llm`, next to `graph`.

```yaml
scopes:
  client-a:
    llms:
      primary:
        provider: "openai"
        model: "gpt-4o-mini"
        api_key: "${CLIENT_A_OPENAI_KEY}"
        routing: "fallback"
        realization: "auto"
        bounds: { max_tools_per_message: 3, timeout: 30000 }
    bots:
      support-bot:
        graph: "faq-support"
        llm: "primary"
```

Rules:

- A bot may omit `llm`; it then runs deterministically: no routing, no
  realization, and no tools.
- `routing` and `realization` default on the llm. A graph may override
  `routing` (ADR 0015); `realization` has no graph override.
- `bounds` holds the tool loop limits (renamed from `llm.agent`).
- `provider` and `model` are required; `api_key` is optional for local
  providers.
- References stay inside the scope (ADR 0005, ADR 0016). There are no shared
  llms across scopes.
- The instance-level `llm` section is removed.

## Consequences

- Per-client models and keys; BYOK is possible.
- Deterministic and LLM-backed bots coexist in one fleet.
- Each scope repeats its llm definitions; that is the isolation cost.
- ADR 0003 and ADR 0008 wording changes to "a bot with no llm runs
  deterministically".
- `config.example.yaml`, the schema, fixtures, and validation are updated.
