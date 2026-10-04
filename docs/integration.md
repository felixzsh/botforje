# Host integration boundary

ADR 0046 owns the responsibility decision. This document describes how to apply
it without requiring a particular host, dashboard, or deployment topology.
`api.md` owns current API behavior; `access-policy.md` owns open policy decisions.

## Responsibility map

| Botflota | Host application |
| --- | --- |
| Execute graphs and actions | Provide its own user experience |
| Validate and publish YAML | Author drafts and decide which changes to request |
| Pair accounts and operate bot channels | Guide setup and display operational results |
| Persist conversations and deliver messages | Decide workflows built on those capabilities |
| Enforce scope authorization | Map external identities to scopes and instances |
| Expose runtime facts and events | Interpret those facts for its own policies |
| Apply justified technical protections | Define aggregate policy and its periods |

A scope is a namespace and logical isolation boundary, not an external account,
an interface group, or a policy group. Several scopes may be associated with
one external identity; the mapping belongs entirely to the host.

## Recommended application flow

```text
Browser -> host application API -> Botflota REST API -> runtime and stores
                  |
          external identity, scope mapping,
          application policy and draft authoring
```

The host authenticates its own callers and checks their application permissions.
It chooses the target instance and scope from its own mapping, not from an
unchecked browser parameter. Integration credentials remain server-side; use
scoped credentials for scope operations where possible and keep owner authority
separate. Never pass the owner token to a browser.

Botflota still authorizes every API operation. A host-side check does not replace
that boundary, and a scoped token currently has both read and write authority
inside its granted scopes. Narrower application permissions are the host's to
enforce without exposing that broader credential.

An independent operator dashboard may use Botflota directly. Delegated browser
sign-in is optional future work, not a required step in the flow above.

## Configuration and runtime state

- Draft authoring belongs to the caller. A host may persist drafts in its own
  storage; that does not make drafts the active runtime configuration.
- Publish complete, version-checked definitions through Botflota's API. YAML
  remains the canonical saved behavior on the instance (ADR 0002).
- Distinguish saved from active configuration: an accepted edit can remain
  pending until activation is safe (ADR 0009).
- Use API operations for pairing, conversation control, messages, and history.
  Do not operate directly on the running instance's files or database.
- Deduplicate webhook deliveries by event ID. Only `contact.first_seen` exists
  today; other lifecycle and usage events are planned, not available contracts.
- The host owns backups and deployment of the instances it operates, using the
  documented runtime procedures rather than independent writes to live stores.

## Simple local operation

A local operator can define bots in YAML and use the CLI without a host
application, external identity provider, or resource-policy catalog. Today the
configuration uses explicit scopes; `default` is an ordinary chosen scope name.
Optional implicit-scope syntax is TBD, not an accepted configuration format.

## Shared-process capabilities

A scope bounds authorization, not what a definition can do. The two lists are
different:

| Bounded by Botflota | Left to the host and the deployment |
| --- | --- |
| Local file reads under a scope's file root (ADR 0047) | Network destinations, redirects, and DNS |
| A per-action media size cap and request timeout | Environment secret references |
| Scope authorization on every API route | CPU, memory, and fairness between scopes |

Without a file root a scope reads any file the process can; a definition that
references an environment variable sends that variable wherever its request
points. Publish-time validation cannot close these by itself: a templated path
or URL is resolved later, and a redirect can change the destination.

Recommended host rules when callers are not fully trusted:

- Provision one directory per scope, assign it as the scope's file root through
  owner configuration, and let callers upload into it. Never let the client, or
  the definition it publishes, choose a server path.
- Author a media step against an uploaded file, by a name the host manages,
  never a free filesystem path.
- Do not let a caller author `${ENV_VAR}` references. Hold integration
  credentials in the host backend and inject them from the instance's own
  configuration.
- Allow outbound HTTP to external services only. Botflota does not filter
  destinations today; enforce egress at the network or proxy layer when a scope
  must not reach private addresses.
- Bound sizes and timeouts with the existing settings, and treat fairness
  between scopes as a deployment concern.

The host's backend validates every document before publishing it, because the
API accepts a complete scope and a scoped token can publish directly. That
validation is the host's, and it is more than a UI restriction.

## Scope assets and media steps

The recommended shape for a host that lets clients attach files:

```text
client upload -> host backend stores it under the scope's private directory
              -> host publishes a media step that names the file
              -> runtime reads it inside the scope's file root (ADR 0047)
```

The host owns creation, naming, cleanup, and quotas of that directory. The
client never sees or writes a filesystem path; it chooses among files it already
uploaded, and the definition the host publishes uses a path relative to the
scope's root.

## Integration validation

A first integration should prove one scoped publish, pairing, a conversation
read, and a signed webhook, without waiting for a dashboard or delegated login.
Use a fake channel for deterministic authorization and publication tests; real
account onboarding is a separate operational check. Onboarding authors who are
not fully trusted waits for a scope file root and the host rules above; until
then, share an instance only with authors you would let read the server's
files.
