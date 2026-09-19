# ADR 0034 — Package layout: capabilities, adapters, and internal

Status: Accepted (2026-09)

## Context

Every package lived under `internal/`: domain, config, template, engine,
fleet, outbox, sqlite, whatsapp, and logging. `internal` was the default
rather than a decision, and a reader could not tell a product capability from
an implementation detail. The standard library and projects like Caddy point
at a better criterion: the core is a package of its own, capabilities are
packages, concrete integrations are separate, and `internal/` keeps only
subordinate mechanisms.

## Decision

- A product capability gets its own package; a subordinate detail lives
  inside its owner or, as a last resort, in `internal/`.
- The root package `botflota` holds the core contracts: messages, persisted
  records, ports, errors, and the structured logger. It depends on the
  standard library only.
- Top-level capability packages: `config`, `template`, `engine`, and `fleet`
  (its filter and outbox are unexported parts of the package).
- Concrete implementations of the core ports live under `adapters/`, grouped
  by technology: `adapters/sqlite`, `adapters/whatsapp`.
- `cmd/botflota` parses arguments and assembles; it holds no domain logic.
- `internal/` is a last resort, never a default: only a shared implementation
  detail that is not a capability and has no natural owner.
- Visibility is not a support contract. Botflota ships a binary; packages
  outside `internal/` are importable because Botflota's own code composes
  them, not because they are an SDK, and they carry no compatibility promise
  beyond the module's own releases.
- Whether a module is an application or a library does not decide visibility;
  capability versus subordinate detail does.

## Consequences

- Dependencies point one way: `cmd`, the capability packages, and the
  adapters depend on the core; the core never imports them.
- Importing an adapter pulls its dependencies (whatsmeow, SQLite, an LLM
  SDK); the core stays free of them.
- Path references in ADR 0026 and the docs were updated; no decision changed.
- Future capabilities arrive with their own package (`api`, `control`,
  `dashboard`, `auth`, `webhook`, `behavior`), recorded in
  `docs/architecture.md`; `internal/` reopens only under the rule above.
