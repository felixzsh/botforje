# ADR 0031 — Model catalogs are scoped, with a shared host catalog

Status: Accepted (2026-09)

## Context

One process serves several scopes, and each wants its own provider, model, and
key. A single instance-level model section cannot express that, so model
profiles live in the scope. A host also wants to offer a provider once instead
of repeating it in every scope.

A shared scope was considered and rejected: a scope is an isolation boundary,
so sharing one means it is not isolated (every operation needs an exception).
What is shared is a set of definitions, and definitions belong to the
instance, not to a scope.

Since ADR 0030, routing is a System One concern and realization is a separate
LLM concern, so one profile can no longer carry both.

## Decision

Each scope holds `system_one` and `llms` catalogs; a bot references one of each
by name with `system_one` and `llm`, next to `graph`. The instance holds a
`shared` catalog with the same two catalogs, in the main config file only.

```yaml
shared:
  system_one:
    default:
      provider: "typesafe"
      model: "jev-latest"
      api_key: "${TYPESAFE_API_KEY}"
      routing: "fallback"        # off | fallback | always
      confidence_threshold: 0.6
      bounds: { timeout: 2000 }  # milliseconds
  llms:
    standard:
      provider: "openai"
      model: "gpt-4o-mini"
      api_key: "${OPENAI_API_KEY}"
      realization: "auto"        # off | auto
      bounds: { max_tools_per_message: 3, timeout: 30000 }

scopes:
  support:
    # system_one:
    #   default: { api_key: "${SUPPORT_TYPESAFE_KEY}" }  # scope wins
    bots:
      support-bot:
        graph: "faq-support"
        system_one: "default"
        llm: "standard"          # optional
```

Resolution and rules:

- A name resolves in the scope first, then in `shared`; the scope entry wins.
  References never cross scopes.
- `shared` belongs to the instance, not to a scope: it is not a scope, is not
  listed, and is not addressable by a scoped principal. It admits `system_one`
  and `llms` only; `bots` never, and `actions`/`graphs` wait for a governance
  story.
- `shared` lives in the main file only for now.
- `system_one.<name>`: `provider` and `model` required; `routing` and
  `confidence_threshold` default here and a graph may override `routing`
  (ADR 0030); `bounds` holds the call timeout.
- `llms.<name>`: `provider` and `model` required; `realization` and `bounds`;
  no `routing`.
- `provider` and `model` are required; `api_key` is optional for local
  providers.
- A bot with neither `system_one` nor `llm` runs deterministically. A
  `system_one` without an `llm` routes with AI and replies from prescribed
  bodies; this is the recommended shape (ADR 0032).

## Consequences

- A host defines a provider once; a scope overrides it with its own key or
  stays self-contained, so a scope still exports on its own.
- `routing` lives on `system_one`; `realization` stays on `llms`.
- System One and LLM are configured and operated separately.
- `shared` is one concept, not a second kind of scope: lists, quotas, and
  scoped authority stay scope-only with no exceptions.
- Each scope repeats its own definitions; that is the isolation cost.
