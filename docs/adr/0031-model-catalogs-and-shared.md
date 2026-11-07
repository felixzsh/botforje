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
      provider: "systemone"
      base_url: "https://api.example.com/v1/systemone"
      model: "clef"
      api_key: "${DECISION_API_KEY}"
      routing: "fallback"        # off | fallback | always
      confidence_threshold: 0.6
      bounds: { timeout: 2000 }  # milliseconds, measured against a real endpoint
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
    #   default: { api_key: "${SUPPORT_CLEF_KEY}" }  # scope wins
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
  `confidence_threshold` default here, and a graph may override both
  (ADR 0030); `bounds` holds the call timeout and `mapping_stage_size`
  (ADR 0056).
- `llms.<name>`: `provider` and `model` required; `realization` and `bounds`;
  no `routing` — routing belongs to `system_one` and a `routing` key under
  `llms` is rejected, not ignored.
- `provider` is an identifier the binary must be able to build, never a
  free-form vendor string: a config naming one it cannot build fails
  validation before any bot starts. Two ship. `systemone` is the decision
  model, and it requires `base_url` because every endpoint is someone else's
  choice: the standard's own host, a proxy, or a gateway. `openai` and
  `openai_compatible` are one OpenAI-compatible chat adapter, the first with
  the public endpoint by default and the second requiring `base_url`, which is
  how a host-run gateway is configured.
- A decision model is always reached over an API, never run in-process, so
  `api_key` is required for `system_one`. An `llm` may omit it only when its
  endpoint needs none.
- Defaults, when a field is omitted or zero: `routing: fallback` and
  `confidence_threshold: 0.6` on `system_one`, with `bounds.timeout: 2000` ms —
  a measured number, six times the median routing call, not a guess; and
  `realization: auto`, `bounds.max_tools_per_message: 3`, `bounds.timeout:
  30000` ms on `llms`. `auto` realizes only a message that declares `intent`,
  and `off` never does. A negative value is a validation error.
- An `intent`-only message with no `body`, reachable by a bot that resolves no
  `llm`, is a validation error: it has no fallback text to send.
- Model profile keys remain their identifiers. An optional `display_name`
  supplies a UI label; when absent or empty, the interface uses the key, for
  scoped and shared entries alike.
- `provider` and `model` are required; `api_key` is optional for local
  providers.
- `shared` belongs to the instance, so a `${NAME}` in it resolves from the
  process environment only: a scope's secret catalog (ADR 0049) never covers
  it. A scope entry that overrides a shared profile keeps resolving from that
  scope's own catalog.
- A change to `shared` changes every bot that resolves there, so it is an
  instance change: it takes effect on restart, like the other instance
  settings, and never as a live per-bot re-resolution.
- A model call is outbound HTTP, so it obeys the instance egress policy
  (ADR 0048) like a `request` step: under `public_only`, an endpoint that does
  not resolve publicly is refused, and the error names the endpoint and the
  policy. A decision endpoint is always remote, so this bites an `llm` pointed
  at a private host, not a `system_one`.
- A bot with neither `system_one` nor `llm` runs deterministically. A
  `system_one` without an `llm` routes with AI and replies from prescribed
  bodies; this is the recommended shape (ADR 0032).

## Consequences

- A host defines a provider once; a scope overrides it with its own key or
  stays self-contained, so a scope still exports on its own.
- `routing` lives on `system_one`; `realization` stays on `llms`.
- One adapter covers cloud and local chat: the provider identifier chooses the
  endpoint, not a second code path.
- Exchanging one decision model for another is a profile edit: the standard is
  the interface, so a different vendor, a proxy, or a gateway costs no code.
- System One and LLM are configured and operated separately.
- `shared` is one concept, not a second kind of scope: lists, quotas, and
  scoped authority stay scope-only with no exceptions.
- Each scope repeats its own definitions; that is the isolation cost.
