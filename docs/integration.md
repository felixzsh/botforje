# Host integration boundary

ADR 0046 owns the responsibility decision. This document describes how to apply
it without requiring a particular host, dashboard, or deployment topology.
`api.md` owns current API behavior; `access-policy.md` owns open policy decisions.

## Responsibility map

| Botflota | Host application |
| --- | --- |
| Execute graphs and actions | Provide its own user experience |
| Validate and publish YAML | Author drafts and decide which changes to request |
| Bind accounts and operate bot channels | Guide setup and display operational results |
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
- Use API operations for binding, conversation control, messages, and history.
  Do not operate directly on the running instance's files or database.
- Deduplicate webhook deliveries by event ID. Only `peer.first_seen` exists
  today; other lifecycle and usage events are planned, not available contracts.
- Set each scope's file root yourself in the main config file. It is owner
  policy, restart-required, and no publication can set, change, or remove it
  (ADR 0047).
- The host owns backups and deployment of the instances it operates, using the
  documented runtime procedures rather than independent writes to live stores.

## Simple local operation

A local operator can define bots in YAML and use the CLI without a host
application, external identity provider, or resource-policy catalog. Today the
configuration uses explicit scopes; `default` is an ordinary chosen scope name.
Optional implicit-scope syntax is TBD, not an accepted configuration format.

## Shared-process capabilities

A scope bounds authorization, not what a definition can do. Three controls are
the owner's to set; the rest stays with the deployment:

| Bounded by Botflota, when the owner sets it | Left to the host and the deployment |
| --- | --- |
| Local file reads, under a file root (ADR 0047) | CPU, memory, and fairness |
| Outbound HTTP, under an egress policy (ADR 0048) | Fetch bounds beyond the media cap |
| `${NAME}` resolution, under a secret catalog (ADR 0049) | Network hardening beyond the policy |

With no file root, no `egress` line, and no secret catalog, a definition reads
any file the process can, reaches any host the server can, and resolves any
process variable. Publish-time validation cannot close those by itself: a
templated path or URL resolves later, and a redirect can change the destination.
The runtime checks are the bound; the document is not.

Recommended host rules when callers are not fully trusted:

- Give each scope a private directory as its file root and let callers upload
  into it. Never let the client, or the definition it publishes, choose a server
  path.
- Run the instance with `egress: public_only`, and reach an internal service
  through a public address. Add no allowlist until a real caller needs one and
  its decision is recorded.
- Give each scope a secret catalog outside its file root, and let the host
  backend choose what goes in it. A client never authors `${NAME}`, and the
  catalog holds only what that client may send to an endpoint it controls.
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

Keep each scope's secret catalog outside that directory (ADR 0049): a file
inside the root could be sent as a document, which would leak the whole catalog.

## Secrets

A scope with a secret catalog resolves `${NAME}` only from it, with no fallback
to the process environment (ADR 0049). The host writes the file, declares it as
`secret_files` in the main config file, and keeps it outside every file root.

- The client never authors a `${NAME}`; the host backend decides which name a
  published header uses, or publishes a fixed credential.
- The catalog holds only credentials that scope may use with the reach the host
  allows. A credential whose destination must stay fixed belongs to the host
  backend, not to a scope.
- Names are per scope: the same name in two catalogs is two different values.

With no catalog, a scope falls back to the process environment, which on a
shared instance is the operator's and every other scope's names as well.

## Integration validation

A first integration should prove one scoped publish, binding, a conversation
read, and a signed webhook, without waiting for a dashboard or delegated login.
Use a fake channel for deterministic authorization and publication tests; real
account onboarding is a separate operational check. Onboarding authors who are
not fully trusted uses the owner controls above: a file root, `egress:
public_only`, and a secret catalog per scope (ADRs 0047–0049). All three are
available. `file_roots` in the main config file bounds one scope's local reads,
and a scope without one still reads any file the process can. The egress policy
is the instance setting and the dial the runtime makes. `secret_files` in the
main config file bounds the names one scope resolves, and a scope without an
entry still resolves the server's process environment.
