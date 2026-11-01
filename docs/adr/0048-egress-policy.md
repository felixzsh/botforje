# ADR 0048 — Optional egress policy for outbound HTTP

Status: Accepted (2026-10)

## Context

A `request` step and a `media` step fetched by URL make an outbound call from
the process that runs the instance. For an operator who writes their own YAML
that is the point. For a scoped author it is a way to reach services the scope
does not own: `http://127.0.0.1:9000/admin/reset` names the instance's own host,
not the author's, and a service that trusts local connections answers it. A
public-looking name can also resolve to a private address, and a redirect can
move a request after the first check.

Scope authorization limits which resources a credential administers; it does not
limit which hosts a definition can reach. A host that runs definitions from
independent authors needs a way to bound that reach without giving up the
capability.

## Decision

Botflota gains an optional, owner-managed **egress policy** for outbound HTTP.

- An instance setting in the main config file, next to `shared`, `webhooks`,
  and the file roots: `egress` is `open` (the default) or `public_only`. It is
  owner-managed and restart-required; a scoped publication can never change it.
- Under `public_only`, an outbound HTTP or HTTPS call may reach only a public
  destination. Loopback, private, link-local, unique-local, unspecified,
  multicast, and link-local metadata addresses are blocked, for IPv4 and IPv6,
  whether written literally or reached through a name.
- The check applies to the effective connection, not to the URL text: the name
  is resolved and every address the call would dial is checked, and each
  redirect is re-checked or refused. A dialer that can be trusted to check the
  address it actually connects to is the mechanism.
- It covers every configurable outbound fetch the runtime makes, including a
  `request` step, media fetched by `url`, and the model endpoint of a scope's
  `system_one` or `llms` profile, whose `base_url` is a destination like any
  other; it never relies on `request` alone.
- A blocked destination fails the action with an error that names neither
  internal topology nor another scope.
- It is a policy, not a sandbox. It does not bound secrets (ADR 0049), response
  size, CPU, or fairness between scopes.
- There is no allowlist in this decision: an owner that must reach a service on
  a private address runs with `open`, or publishes that service on a public
  address. A narrower exception needs its own decision, so the first cut cannot
  be widened by accident.

## Consequences

- A host can run definitions from independent authors while the runtime refuses
  to reach the host's own internal services.
- An instance with no `egress` line keeps today's behavior: no filtering, which
  is what a trusted single-operator install wants.
- A host whose tools must call its own application API puts that API behind a
  public address, or keeps the policy open and accepts the risk deliberately.
- The policy is per instance, not per scope; a host with scopes that need
  different reach separates them into instances.
- A local model server is a loopback or private destination, so under
  `public_only` a `base_url` such as `http://localhost:11434` is refused like
  any other internal service. A scope that needs a private model endpoint runs
  on an instance with `open`, or reaches the model through a public gateway,
  which is an ordinary `llms` profile with a `base_url` (ADR 0046).
- Blocked calls and redirects are reported honestly, so an author can tell a
  refused destination from a network failure, without learning the internal
  network.
