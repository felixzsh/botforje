# ADR 0014 — Secrets enter through environment interpolation

Status: Accepted (2026-09)

## Context

Config files get copied, committed, and shared, so secrets must never live
in YAML. Tokens are needed in a few places: the LLM key, outbound request
headers, and basic auth.

The template engine resolves runtime conversation values (`graph`, `node`,
`sender`, `message`, `bot`, `input`, request results). Adding secrets to it
would let a secret flow into a generated message or a log.

## Decision

Any string in the config may interpolate `${ENV_VAR}` from the process
environment. Interpolation happens once, when the config is loaded.

- Expansion runs after YAML decoding, on string values, so secret characters
  cannot break the YAML.
- A missing variable is a validation error; the engine never sends a literal
  `${ENV_VAR}` to a provider or a user.
- `validate` reports missing variables by name, never by value.
- Resolved values are wrapped in a `Secret` type whose `String()` masks them,
  so logs, errors, and the API never print them.
- There is one mechanism: no `api_key_env`. The LLM key is
  `api_key: "${OPENAI_API_KEY}"`.
- Secrets come from the process environment (systemd `EnvironmentFile`, a
  container secret, or a secret manager injecting env), never from the
  database: reading secrets must not depend on the state they configure.

## Consequences

- Config is safe to commit and share.
- Startup fails fast when a secret is missing; no half-configured bot runs.
- Operators wire secrets through systemd or the container runtime.
- Redaction is a type-level guarantee, not a convention.
- Per-tenant secrets in the database are deferred to the SaaS phase
  (ADR 0005).
