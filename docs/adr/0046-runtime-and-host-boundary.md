# ADR 0046 — Runtime and host applications have separate responsibilities

Status: Accepted (2026-10)

## Context

The runtime, its control plane, and a host application need to integrate without
sharing a product identity. An embedded dashboard is useful for an operator, but
making it the required interface of every host couples their experience to this
project. A generic name for a host-specific rule does not make that rule a
runtime responsibility.

## Decision

- Botflota owns declarative execution: graphs, actions, channels, fleet,
  persistence, delivery, validation, and the operational control plane.
- YAML remains canonical for behavior. API publication stays in Botflota and
  uses the same validation and activation path as manual edits (ADR 0002).
- Scopes isolate names, references, and data access. Scoped credentials express
  authority, not the host's account model or resource policy. Neither concept
  requires a host application.
- A host owns its application, identity model, access decisions, mapping to
  scopes and instances, and guided authoring experience. It consumes Botflota's
  API rather than writing the running instance's database or config files.
- A host can keep integration credentials server-side and expose its own
  application API. Using Botflota does not require browser handoff, a trusted
  issuer, or a Botflota dashboard.
- A future Botflota dashboard is an optional operator interface. It is not the
  host application's required interface and is not a prerequisite for an
  integration. Its implementation remains deferred.
- Events and reads describe runtime facts. The host interprets them; asynchronous
  delivery does not guarantee an exact admission ceiling.
- Technical protections may belong to Botflota when justified independently by
  execution safety. Aggregate host policy, periods, and provisioning combined
  with policy are TBD in `docs/access-policy.md`, not settled runtime contracts.
- Logical scope isolation is not a sandbox for untrusted definitions. Local file
  access is bounded by an optional owner-set file root per scope (ADR 0047);
  network destinations, environment references, and shared resource use stay
  process-level and are not bounded by Botflota. A host that onboards untrusted
  authors restricts the definitions it publishes at its own backend, sets a file
  root per scope, and separates deployments when it needs stronger guarantees
  (`docs/integration.md`).
- The simple local path must not require issuer setup, policy groups, or host
  provisioning. Optional implicit-scope syntax remains TBD; ADR 0016 still
  defines the implemented explicit-scopes format.

## Consequences

- The API remains reusable by the CLI, operator tools, and independent hosts.
  Hosts do not duplicate the graph engine, channel lifecycle, or validator.
- A visual authoring application and assisted onboarding are not commitments of
  Botflota's roadmap. Runtime primitives such as simulation can be considered
  separately when an actual caller needs them.
- Delegated browser sign-in is optional future work, not the integration seam
  every host must implement.
- No runtime implementation or configuration format changes with this ADR.
  Existing tokens, publication, labels, and reload semantics remain intact.
- Open choices are documented as TBD before new code makes them permanent.
