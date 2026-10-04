# ADR 0049 — A scope's secrets come from an owner-set catalog

Status: Accepted (2026-10)

## Context

ADR 0014 resolves `${ENV_VAR}` from the process environment, and only in fields
enabled for secrets (a model `api_key`, request header values, a webhook
`secret`). For a single operator that is the whole story. On an instance that
also runs definitions from independent authors, it hands every scope the
operator's environment: a definition can reference a name it guesses or knows,
such as an internal key, and send it to an endpoint it controls.

The egress policy of ADR 0048 does not close this, because the endpoint that
receives the secret can be a perfectly public one. The missing bound is *which
names a scope may resolve*, not where the call goes.

## Decision

Botflota gains an optional, owner-managed **secret catalog** per scope.

- `secret_files` is a map of scope key to a file path, declared in the main
  config file only, next to `file_roots`. It is owner-managed and
  restart-required; a scoped publication can never set or change it, because it
  is not part of a scope's document.
- When a scope has a catalog, a `${NAME}` in one of its definitions resolves
  **only** from that catalog. There is no fallback to the process environment;
  a name the catalog does not hold is an error that names the name, never a
  value.
- The file is read as data: `NAME=value` lines, blank lines and `#` comments.
  It is never executed, has no includes, and performs no command substitution.
- It must live outside every scope's file root (ADR 0047), so it can never be
  sent as media; load rejects a catalog that sits inside a file root.
- Values keep ADR 0014's behavior otherwise: the containing string is secret end
  to end, serialization keeps the placeholder, and only the consumer that sends
  the value reveals it.
- Instance-level secrets — a webhook target and a `shared` model profile — stay
  owner-owned and resolve from the process environment, because no scope
  document can reach them.
- An instance with no `secret_files` entry for a scope keeps today's behavior:
  the process environment.

## Consequences

- A host assigns one file per scope, keeps it outside the assets directory, and
  knows that a scope cannot reach another scope's or the operator's environment.
- Rotation is editing the file and restarting; a secrets manager that writes
  that file still fits, and Botflota adds no secret store of its own.
- It does not stop an author from sending a credential in its own catalog to an
  endpoint it can reach. The catalog holds only what that author may use with
  that freedom; an internal credential whose destination must stay fixed
  belongs to the host backend, not to a scope catalog.
- The catalog changes where a scope's names come from, not the secret field
  rules of ADR 0014, which stay in force.
