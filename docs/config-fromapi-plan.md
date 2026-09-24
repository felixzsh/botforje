# API configuration publication and scope provisioning plan

Planning context, not an implemented API contract. Work through the open
decisions below before treating proposed routes or policy storage as settled.
The current phase is Phase 7 (`PLAN.md`); later scope provisioning and limits
belong to `ROADMAP.md`. Do not start Phase 8 or build the dashboard as part of
this work.

## Baseline

- YAML remains the source of truth for behavior (ADR 0002). The loader merges
  nested includes and records each entity's source file; config validation
  checks the whole candidate. SQLite holds runtime state and credentials
  (ADR 0006).
- Today `GET /api/v1/scopes/{scope}/config` reads the active snapshot. There is
  no publication API; saved valid YAML and active runtime may differ (ADR 0009).
- The owner can address any scope. A scoped token can authorize one or more
  scopes, but cannot create scopes, change its own authority, or edit owner
  policy. Out-of-scope targets must look unknown (404; `docs/api.md`).
- YAML map keys remain identities, not display labels (ADR 0016). Optional
  `display_name` is editable; when absent or empty, an interface shows the
  key. A label edit must preserve references, sessions, guards, linked
  accounts, and token authorization. The API must not materialize the fallback
  in saved YAML. Manual key changes are deletion plus creation, not inferred
  renames. The watcher never writes IDs into manual files.
- Creation and rename have different semantics. When the editor creates a
  bot, graph, action, node, or model profile, its submitted initial name is
  used verbatim as the YAML map key; no random key or `display_name` is
  generated. A later rename keeps that key and writes the new visible label to
  `display_name`. Scope provisioning is the deliberate exception: the owner
  API generates the stable scope key, and the requested initial visible name
  becomes `display_name`.
- Visible labels must be unique in their UI namespace: scopes across the
  instance; each entity category within a scope; nodes within their graph;
  and each catalog within its owner (`scope` or `shared`). Same labels in
  different entity categories or different scopes are allowed. Compare the
  effective label (`display_name` when non-empty, otherwise the key), so a
  missing label cannot collide with an explicitly named entity unnoticed.
  Enforce this in config validation and on every API create/rename; UI checks
  are only early feedback. Case and whitespace normalization for comparisons
  remains an implementation detail to settle before the API contract freezes.
- No config revision history or general rollback. An opaque logical scope
  version/ETag prevents stale API writes; it is not a schema version or an
  archive of previous documents.

## Delivery order

### 1. Publish existing scopes (Phase 7)

Keep Phase 7's `PLAN.md` publication deliverable open until its tests pass.
ADR 0002 now permits the API to edit the main file. Use source origins to edit
existing definitions wherever they live, including an authorized scope in the
main file. The API may change only the target scope and required include
registrations; it must preserve global settings and unrelated scopes. New
definitions still go to a deterministic managed fragment, and the main
include list may be updated to reach it. Separate include files remain a
recommended user organization, not a prerequisite for API editing. Formatting
and comments are not part of the config contract.

- `GET /api/v1/scopes/{scope}/config` should return the **saved** logical scope,
  its opaque version, and the active version/status. An editor must never
  base a write on an older active snapshot while valid YAML is pending.
- A version-checked `PUT /api/v1/scopes/{scope}/config` replaces that scope's
  logical document. The scope key comes from the URL. Existing entities stay
  in their source files, including the main file when that is their origin;
  new ones go to a deterministic managed fragment. The server handles include
  registration without exposing file paths to scoped callers.
- The typed config includes optional `display_name` on scopes, bots, graphs,
  actions, nodes, and LLM profiles. Future System One/shared profiles use the
  same rule. A `PUT` that edits a label retains every map key; references
  continue to use keys. GET leaves absent labels absent; presentation may
  fall back to the key without changing the document or its ETag.
- For child-entity creation, the UI's initial `name` is the key. Reject a key
  collision within its map rather than silently changing or suffixing it. Do
  not persist a separate `display_name` until a later rename. A rename checks
  that its new effective label is unique in the relevant namespace, then
  writes `display_name` only; references and stored runtime identity remain
  key-based. Scope creation keeps the generated-key exception described in
  Phase 14.

For example, creating a bot named `Sales assistant` inserts a map entry under
that exact key and leaves `display_name` absent. Renaming it later to
`Regional sales` keeps the `Sales assistant` key and sets
`display_name: Regional sales`. References such as `bot.graph` and graph
`goto` values always contain keys. A key collision or duplicate effective
label rejects the whole publication; never auto-slug, overwrite, or silently
add a numeric suffix. Scope creation instead generates a key and stores the
requested initial label as `display_name`.
- The loader validates the complete candidate before disk writes. Resolve
  source files and serialize overlapping commits; rebase a write to scope A
  over a concurrent write to scope B in the same physical file. Reject a
  stale write to A, even if its label change seems harmless. Identical
  publications are no-ops. Bound requests waiting for publication locks.
- Stage multi-file updates and recover incomplete commits after errors or
  crashes before normal startup loading. Coordinate with the watcher so it
  never observes a half-published document. Share the normal load, validate,
  compare, and activate pipeline (ADR 0009). Report saved versus active or
  pending; do not claim that a deferred edit is live. The process currently
  loads config before taking its instance lock: fix the recovery ordering.
- Protect secrets on read and write. An unchanged masked literal (`***`) must
  retain the original value, never be written as a secret. Define omission,
  replacement with `${ENV_VAR}`, and explicit removal before accepting PUT.
  A scoped token gets useful errors only for its own scope; global errors and
  other scopes' details remain opaque.
- Direct manual edits are outside API write locks/ETags. Do not overwrite a
  manual editor silently or make the watcher rewrite its files. An invalid
  manual document blocks API publication until corrected by the owner.

Keep `POST /config/validate` out of this slice unless draft validation without
publication is needed. Do not conflate a label edit with a key migration.

### 2. Provision scopes and grant access (Phase 14)

Provisioning is an **owner** operation, separate from scoped publication.
The API generates and checks a unique, stable scope key at creation; manual
authors choose their own keys. It may initialize `display_name`, which an
authorized scoped principal can later edit with the normal publication API.
Creating a scope must not automatically add it to any credential.

A scoped token may authorize multiple scopes and must be granted a new scope
by a separate owner credential operation. A host-facing flow can request
creation from its own backend; the owner credential never reaches a scoped
user or the dashboard. Once group limits are designed in Phase 18, a host may
create an empty group with limits and add scopes on demand. If it wants
ready-to-use scopes, it may request owner provisioning of an initial number
within the group's capacity, with generated keys and optional initial labels.
Neither creating a credential nor setting `max_scopes` implicitly creates
that many scopes.

Settle the provisioning route, collision handling, duplicate-request behavior,
and failure boundaries before implementation. Reuse the publication validation
and activation path rather than inventing a second config writer. Joining a
group needs the Phase 18 policy decision and its own failure boundaries;
neither operation may expose or interrupt peer scopes.

### 3. Enforce aggregate limits across scopes (decision before implementation)

The host knows why limits differ; Botflota knows only generic owner-managed
groups and resource ceilings. For example, a group may start empty with
`max_scopes: 3` and `max_bots: 4`: as scopes join, their bots share one
four-bot budget. These numbers are examples, not a fixed policy schema.

- Group membership and limits are owner-controlled. They are not fields in
  scoped-editable YAML or access tokens. A token answers **which scopes** it
  may use, never **how much** their group may consume. Reissuing a token must
  not reset counters or widen a group's budget.
- Check projected aggregate usage under the same concurrency control as
  creation, membership changes, and publication. A scoped caller cannot evade
  a ceiling by splitting bots across authorized scopes, racing two writes,
  using a direct API call instead of the UI, or moving its own label.
- Decide which resources count, whether every scope must belong to a group,
  how a scope leaves or changes groups, and what to do when lowering a limit
  below current usage. Define how policy applies to manual YAML edits and
  startup without silently deleting or orphaning existing data.
- Decide protected persistence and recovery for group policy and membership
  (including consistency with YAML commits). ADR 0002 makes YAML canonical
  for **behavior**, not a settled home for owner policy; do not choose SQLite
  or a protected file by accident. Define the owner API and its authorization
  matrix in a canonical ADR before implementing limits or group provisioning.

Phase 18 now targets aggregate limits; settle their full contract in an ADR.
Per-scope runtime controls need not be removed. No commercial vocabulary
enters Botflota's contracts, code, comments, or docs.

## Publication wire sketch (not a finalized endpoint reference)

`GET /api/v1/scopes/support/config` returns saved config and an opaque ETag,
alongside active status. A missing `display_name` stays missing in `config`;
the UI displays `support`. A label edit can publish:

```http
PUT /api/v1/scopes/support/config
If-Match: "scope-version-opaque"
Content-Type: application/json

{"config":{"display_name":"Support desk","graphs":{"welcome":{
"root":"start","nodes":{"start":{}}}},"bots":{"assistant":{
"graph":"welcome"}}}}
```

This is a **whole-scope** replacement: omitted entities are removed. Assume
the example includes the scope's entire current content. For a real label
edit, the caller must include the rest of the saved scope unchanged; a
dedicated field update could be considered only if it reuses the same
versioned publication path. Never synthesize fallback labels into the PUT.

Expected outcomes: 200 for active/no-op, 202 for valid but pending, 428 for
missing `If-Match`, 412 for stale scope version, 422 for invalid proposed
config, 404 for unknown or unauthorized scope, and 401 for invalid credential.
If unrelated invalid YAML blocks a scoped caller, return a generic failure
while exposing diagnostics to the owner. Do not expose paths or other scopes.

## Acceptance gates

Phase 7 publication:

- Owner, authorized multi-scope token, and out-of-scope token: reads and
  writes obey authority, with 404 and no existence or policy disclosure.
- Scope split across the main file and nested includes: edit, add, and remove
  definitions, including scopes defined directly in the main file; preserve
  unrelated scopes, global settings, and includes.
- Two concurrent scopes in one file both survive; two stale writes to the
  same scope conflict. An identical write leaves files and runtime unchanged.
- Missing/empty and explicit `display_name`: round-trip without changing keys,
  references, guards, sessions, linked accounts, or token access. Editing a
  label in one scope cannot alter another; missing fallback is presentation
  only. A manual key edit remains a different identity.
- Invalid candidate or masked secret: no partial file write and no secret
  leakage. An injected failure after the first file replacement recovers
  entirely old or entirely new content, including after restart.
- Incompatible graph edit: YAML saved, runtime pending, unrelated scopes
  still active. Manual edits and newly matched includes remain watchable.

Phase 14 provisioning and Phase 18 policy, respectively:

- Owner provisioning creates a unique scope key without implicitly granting
  token access; a scoped token cannot create a scope. Manual authors can keep
  their own keys. For owner-provisioned scopes, the initial requested label is
  stored as `display_name`; child entities use their submitted initial names
  as keys.
- Owner group starts empty, optional initial provisioning stays within its
  budget, and later provisioning updates membership without granting token
  access automatically. Concurrent writes across two scopes cannot exceed
  one aggregate ceiling; scoped and direct API attempts to alter owner
  policy fail. Lowering limits and manual edits follow the settled ADR.
- Duplicate effective labels are rejected by the server within each stated
  namespace; equal labels across categories or scopes remain valid. Test
  create, rename, fallback-to-key, and collisions, including direct API calls.

When implementation begins, request build permission before checks. Use the
Make targets, add concurrency tests where appropriate, and mark phase
deliverables complete only in the commit that proves them. Do not implement
group policy while its authority, persistence, and recovery decision is open.
