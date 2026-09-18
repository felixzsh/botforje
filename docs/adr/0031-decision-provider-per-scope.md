# ADR 0031 — Decision providers live in scoped deciders

Status: Accepted (2026-09)

## Context

ADR 0017 gives each scope a named `llms` catalog, and a bot references one
`llm` that did both routing and realization. ADR 0030 separates the routing
model (a decision provider) from the reply model (an LLM), so one profile can
no longer carry both. The two have different vendors, failure modes, and
costs, and a bot may use one, the other, both, or neither.

## Decision

Each scope holds a named `deciders` catalog next to `llms`. A bot may
reference one by name with `decider`, next to `graph` and `llm`; both are
optional.

```yaml
scopes:
  support:
    deciders:
      router:
        provider: "typesafe"
        model: "jev-latest"
        api_key: "${TYPESAFE_API_KEY}"
        routing: "fallback"        # off | fallback | always
        confidence_threshold: 0.6
        bounds: { timeout: 2000 }  # milliseconds
    llms:
      primary:
        provider: "openai"
        model: "gpt-4o-mini"
        api_key: "${OPENAI_API_KEY}"
        realization: "auto"        # off | auto
        bounds: { max_tools_per_message: 3, timeout: 30000 }
    bots:
      support-bot:
        graph: "faq-support"
        decider: "router"
        llm: "primary"
```

Rules:

- `deciders.<name>.routing` (`off` | `fallback` | `always`) is the default ADR
  0015 describes. A graph may override it, as it does today.
- `deciders.<name>.confidence_threshold` gates acting on a decision (ADR 0030);
  a graph may override it with `confidence_threshold`.
- `llms.<name>` drops `routing`; it keeps `realization` and `bounds`, and its
  only role is reply realization and the tool loop.
- A bot with neither `decider` nor `llm` runs deterministically. A bot with a
  `decider` routes with AI and replies deterministically. A bot with an `llm`
  replies with AI and routes deterministically.
- `provider` and `model` are required; `api_key` is optional for local or
  self-hosted endpoints. References stay inside the scope.
- The shape lands in config v1 in Phase 8: `config.schema.json`,
  `config/example.yml`, fixtures, and validation.

## Consequences

- `routing` moves from `llms` to `deciders`; `realization` stays on `llms`.
- Decision and generative providers are configured and operated separately.
- Deterministic, decision-only, LLM-only, and hybrid bots coexist in a fleet.
- Each scope repeats its decider definitions; that is the isolation cost.
- ADR 0017 is amended: `llms` keeps realization only and `deciders` is added.
