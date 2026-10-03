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

## Shared-process trust boundary — TBD

Scope authorization does not contain the capabilities of a definition. Current
actions can make HTTP requests, and WhatsApp multimedia can read local paths.
Review the complete execution path before accepting definitions from callers
who should not have those permissions.

TBD: trusted-author deployment only, restricted definition capabilities, or
separate process/instance isolation. The decision must cover filesystem access,
network destinations and redirects, secrets, and bounded resource use. Do not
claim that tokens and scope-addressed routes alone provide a sandbox.

## Integration validation

A first integration should prove one scoped publish, pairing, a conversation
read, and a signed webhook, without waiting for a dashboard or delegated login.
Use a fake channel for deterministic authorization and publication tests; real
account onboarding is a separate operational check. Shared-process onboarding
of untrusted authors waits for the trust decision above.
