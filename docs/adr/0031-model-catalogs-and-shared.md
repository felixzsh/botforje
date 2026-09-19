# ADR 0031 — Model catalogs are scoped, with a shared host catalog

Status: Accepted (2026-09)

## Context

ADR 0017 gives each scope a named `llms` catalog, and a bot references one
`llm`. ADR 0030 separates routing (a System One model) from reply realization (an LLM), so
one profile can no longer carry both. A host also wants to offer a provider
once instead of repeating it in every scope, and a tenant wants to bring its
own key (BYOK) or keep a scope self-contained for export.

A shared scope was considered and rejected: a scope is a tenant boundary, so
sharing one means either it is not a tenant (every tenant operation needs an
exception) or it is (and it cannot be shared). What is shared is a set of
definitions, and definitions belong to the instance, not to a tenant.

## Decision

Each scope holds `system_one` and `llms` catalogs; a bot references one of each by
name with `system_one` and `llm`, next to `graph`. The instance holds a `shared`
catalog with the same two catalogs, in the main config file only.

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

- A name resolves in the scope first, then in `shared`. The scope entry wins
  (shadow by name); references never cross tenant scopes.
- `shared` is the host's, not a tenant: it is not a scope, is not listed, and
  is not addressable by a scoped principal (the authority phase, ROADMAP
  Phase 13, changes nothing here).
- `shared` admits `system_one` and `llms` only. `bots` never; `actions` and `graphs`
  wait for a governance story (who edits, how it is versioned, what a change
  to a referenced entity does to every tenant).
- `shared` lives in the main file only for now; fragments may contribute later.
- `system_one.<name>`: `provider` and `model` required; `routing` and
  `confidence_threshold` default here and a graph may override them (ADR 0015);
  `bounds` holds the call timeout.
- `llms.<name>`: `provider` and `model` required; `realization` and `bounds`;
  no `routing`, which moved to the System One model.
- A bot with neither `system_one` nor `llm` runs deterministically. A `system_one` without
  an `llm` routes with AI and replies from prescribed bodies; this is the
  recommended shape (ADR 0032).
- The shape lands in config v1 in Phase 8 (`config.schema.json`,
  `config/examples/reference.yml`, fixtures, validation).

## Consequences

- A host defines a provider once; a tenant overrides it for BYOK or stays
  self-contained, so a scope still exports on its own.
- `routing` moves from `llms` to `system_one`; `realization` stays on `llms`.
- System One and LLM are configured and operated separately.
- `shared` is one concept, not a second kind of scope: lists, quotas, and
  authority stay tenant-only with no exceptions.
- ADR 0017 is amended: `llms` keeps realization, `system_one` is added, and the
  "no shared llms across scopes" rule becomes "no references between tenant
  scopes".
