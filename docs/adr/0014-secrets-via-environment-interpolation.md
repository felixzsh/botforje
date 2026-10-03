# ADR 0014 — Secrets enter through environment references only

Status: Accepted (2026-09)

## Context

Config files get copied, committed, and shared, so secrets must never live in
YAML. Tokens are needed in a few places: model API keys, outbound request
headers, and the signing secret of an outbound webhook (ADR 0045).

The template engine resolves runtime conversation values (`graph`, `node`,
`contact`, `message`, `bot`, `input`, request results). Adding secrets to it
would let a secret flow into a generated message or a log. Treating every
config string as interpolable has the same risk: any string could carry a
secret and lose its disguise.

## Decision

`${ENV_VAR}` is a secret reference, not general interpolation. It is allowed
only in fields explicitly enabled for secrets:

- the `api_key` of a model profile;
- the values of request `headers`;
- the `secret` of a webhook target.

Nothing else. A `${...}` in a message body, a context value, an input default,
an include path, or any other ordinary string is a validation error.

- The whole containing string is sensitive, not only the resolved value: a
  string that carries a secret reference is secret end to end.
- Serialization preserves the `${ENV_VAR}` placeholder, so a masked form never
  leaks the value; the real value is revealed only to the consumer that sends
  it (the adapter that performs the call).
- A missing variable is a validation error that names the variable, never its
  value. The engine never sends a literal `${ENV_VAR}`.
- The engine rejects any `${...}` that reaches an outgoing message as a last
  guard, so a validation gap cannot leak a secret.
- `{{...}}` stays runtime-only. It never reads the environment, and a string
  never combines `${...}` and `{{...}}`.
- There is one mechanism: no `api_key_env`. The key is
  `api_key: "${OPENAI_API_KEY}"`.
- Secrets come from the process environment (systemd `EnvironmentFile`, a
  container secret, or a secret manager injecting env), never from the
  database. They are separate from runtime credentials the engine generates
  and stores (the whatsmeow device store, ADR 0006).

## Consequences

- Config is safe to commit and share, and interpolation cannot smuggle a
  secret into a message or a log.
- Startup fails fast when a referenced variable is missing.
- Operators wire secrets through systemd or the container runtime.
- Redaction is a property of the field and the string, not a convention:
  `request` headers and model keys need a sensitive string type that keeps the
  placeholder and reveals on demand, and ordinary strings reject `${...}`.
- Per-scope config secrets in the database are deferred to a later phase.
