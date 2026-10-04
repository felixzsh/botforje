# ADR 0047 — A scope's local files live under an owner-set root

Status: Accepted (2026-10)

## Context

A `media` step reads its bytes from a templated `url` or a local `path`, and the
channel opens that path with the process's own privileges
(`adapters/whatsapp/channel/content.go`). That is right for an operator who
writes their own YAML and deploys their own instance, but a scoped author who
does not own the server can point the path at any file the process can read.
Scope authorization limits which resources a credential administers; it does not
limit which files a definition can ask the runtime to open.

A host that runs definitions from several independent authors needs a way to
bound what local files one scope can reach without giving up the capability.

## Decision

Botflota gains an optional, owner-managed **file root** per scope: a directory
that bounds the local files that scope's definitions may read.

- `file_roots` is a map of scope key to absolute directory, declared in the main
  config file only, next to `shared` and `webhooks`. It is owner-managed and
  restart-required; a scoped publication can never set, change, or remove it,
  because it is not part of a scope's document.
- When a scope has a root:
  - a local `path` in one of its steps is relative to that root, and an absolute
    path is rejected;
  - a path that resolves outside the root — including through `..` segments or a
    symbolic link — is refused, with the confinement applied when the file is
    opened, after templates are resolved, never by trusting a prefix;
  - a refused or unreadable path fails the step; it never falls back to an
    unconfined read.
- When a scope has no root, local access keeps today's behavior: the process's
  own file permissions. An instance that trusts its authors needs no
  configuration.
- Load rejects a root that is not an existing directory and rejects roots that
  overlap, so one scope's root can never contain another's.
- The root bounds local file reads only. It is not a sandbox: it says nothing
  about network destinations, secret names, or resource use, each of which has
  its own owner control (ADRs 0048 and 0049) or none.
- The operator provisions the directory and its permissions. Botflota does not
  create it, and the guidance is to keep credentials and unrelated data outside
  every root.

## Consequences

- A host that onboards independent authors can give each scope a private
  directory and know that a definition cannot read past it, without a second
  runtime or a container per scope.
- A local operator who never sets a root is not affected; the field is optional
  and absent from `config init`.
- The confinement belongs to the configuration surface, not to the channel
  alone: validation reports an absolute path under a root early, and the
  component that reads the bytes enforces it last, so an adapter cannot
  accidentally bypass it.
- Roots do not bound HTTP, secrets, or CPU and memory. A host that needs those
  guarantees must restrict the definitions it publishes or separate the
  deployments; ADR 0046 records that boundary and `docs/integration.md` the
  recommended host rules.
- A future step that reads another local source must use the same root; the
  decision is written per capability, not per adapter.
