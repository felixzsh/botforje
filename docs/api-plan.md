# API plan

Phase 7 plan for reading and publishing existing scopes by API, registering
owner-managed limit sets with generated-key scopes and scoped credentials,
and enforcing aggregate definition, running-bot, outbound-message, and stored
message limits across each set's scopes. It covers proposed endpoints, SQLite
tables, concurrency, recovery, and acceptance tests. Nothing below is
implemented unless marked current. An ADR must settle the new owner policy,
quota semantics, and cross-store commit before coding them. Phase 14 uses
these APIs in the scoped surface; Phase 18 does not reimplement the limits.
Do not start Phase 8 or build the dashboard here.

## Baseline

- YAML remains the source of truth for behavior (ADR 0002). The loader merges
  nested includes and records each entity's source file; config validation
  checks the whole candidate. SQLite holds runtime state and credentials
  (ADR 0006).
- Today `GET /api/v1/scopes/{scope}/config` reads the active snapshot. There is
  no publication API; saved valid YAML and active runtime may differ (ADR 0009).
- The owner can address any scope. A scoped token can authorize one or more
  scopes and can both read and write them; no read-only scoped token is
  planned. It cannot create scopes, change its own grants, or edit owner
  policy. Out-of-scope targets look unknown (404; `docs/api.md`). The merged
  `exclusive_scopes` setting makes grants exclusive when enabled, including
  checking existing active tokens at startup; by default sharing is allowed.
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
- No config revision history or general rollback. An opaque logical version
  per scope prevents stale API writes; a multi-scope publication carries one
  expected version per scope. These are not schema versions or archives.

## Delivery order

### 1. Read and publish existing scopes

Keep Phase 7's `PLAN.md` publication deliverable open until its tests pass.
ADR 0002 permits edits to authorized scopes even when they live in the main
file. Preserve global settings and every unaddressed scope. Existing
definitions stay in their source files; new ones use managed fragments and
the main include list may be updated to reach them. Includes are recommended
organization, not an API requirement; formatting is not a config contract.

- `GET /api/v1/scopes/{scope}/config` should return the **saved** logical scope,
  its opaque version, and the active version/status. An editor must never
  base a write on an older active snapshot while valid YAML is pending.
- A version-checked `PUT /api/v1/scopes/{scope}/config` replaces one existing
  logical scope. `POST /api/v1/config/publications` publishes a nonempty set
  of existing scopes as **one** saved change. Both routes use the same writer
  and checks. For a scoped token, authorize every requested key before
  loading or returning scope details; an unauthorized or unknown key makes
  the whole request fail without disclosing which other scopes exist. Owner
  may target any existing scope. Neither route creates, removes, or rekeys a
  scope, edits instance settings, or changes owner policy.
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
  key-based. Owner scope creation remains the generated-key exception in
  the next section.

For example, creating a bot named `Sales assistant` inserts a map entry under
that exact key and leaves `display_name` absent. Renaming it later to
`Regional sales` keeps the `Sales assistant` key and sets
`display_name: Regional sales`. References such as `bot.graph` and graph
`goto` values always contain keys. A key collision or duplicate effective
label rejects the whole publication; never auto-slug, overwrite, or silently
add a numeric suffix. Scope creation instead generates a key and stores the
requested initial label as `display_name`.
- Every requested scope has its own expected version. Missing or stale
  versions, validation errors, or quota failures reject **the entire batch**
  without writing any scope. Resolve source files and serialize overlapping
  commits. Rebase writes to untouched scopes in shared physical files; never
  overwrite a newer edit to a requested scope. Identical batches are no-ops.
  Bound requests waiting for locks; avoid unbounded batch size and file work.
- Stage all changed files and recover incomplete commits after errors or
  crashes before normal startup loading. Coordinate with the watcher so it
  never observes a half-published document. Share the normal load, validate,
  compare, and activate pipeline (ADR 0009). The save is atomic; subsequent
  activation may be pending for some scopes. Report each scope's saved and
  active versions/status; never claim a deferred edit is live. The process
  currently loads config before taking its instance lock: fix that ordering.
- Protect secrets on read and write. An unchanged masked literal (`***`) must
  retain the original value, never be written as a secret. Define omission,
  replacement with `${ENV_VAR}`, and explicit removal before accepting writes.
  A scoped token gets useful errors only for its own scope; global errors and
  other scopes' details remain opaque.
- Direct manual edits are outside API write locks/ETags. Do not overwrite a
  manual editor silently or make the watcher rewrite its files. An invalid
  manual document blocks API publication until corrected by the owner.

Keep draft-only validation out of this slice unless needed. Do not conflate a
label edit with a key migration. A scoped dashboard can batch edits to its
already authorized scopes; it cannot call owner provisioning.

### 2. Owner provisioning and limit sets (advance into Phase 7)

The host assigns numeric ceilings; Botflota never knows why it chose them.
A **limit set** is an internal, owner-managed policy joining several scopes
under shared ceilings. It is not a scope, a UI group, or a token. The current
`access_tokens` and `access_token_scopes` tables continue to express only
authority. A token can cover multiple scopes; renewing or revoking it does
not reset usage. Only owner operations create scopes, policies, or grants.

Proposed owner endpoints (all absent today):

| Method and path | Purpose |
| --- | --- |
| `POST /api/v1/provisioning` | Create a limit set, initial scopes, and token together. |
| `POST /api/v1/limit-sets` | Create an empty set with all limits; no token. |
| `GET /api/v1/limit-sets/{id}` | Read limits, member scopes, and usage. |
| `PUT /api/v1/limit-sets/{id}/limits` | Replace all limits, with a policy version. |
| `POST /api/v1/limit-sets/{id}/scopes` | Add scopes, optionally grant them to a token. |
| `PUT /api/v1/tokens/{id}/scopes` | Replace grants under owner authority. |

`POST /api/v1/provisioning` takes `Idempotency-Key`, a complete `limits`
object, a requested array of scopes (possibly empty), and an optional token
request (`name`, optional `expires_at`). The initial token, when requested,
must have at least one scope. Botflota generates collision-checked stable
scope keys, writes the new YAML fragments, creates membership and the token,
and returns the keys, limit-set ID, token ID and clear token **once**. A scope's
initial requested visible name becomes `display_name`; manual YAML authors
still choose their own keys. Setting `max_scopes` alone creates no scopes.
`exclusive_scopes` applies to every new or changed grant, including this API.
The owner credential stays with the host; it never reaches a scoped user.

`POST /api/v1/limit-sets/{id}/scopes` accepts an array of requested scope
labels and an optional `grant_to_token_id`. The created keys join the set and
the grant is updated as one operation, or none of it is changed. A separate
owner token-grant update supports existing/manual scopes and credential
replacement; scoped callers cannot use either route. No policy is ever
embedded in a token. Deleting or moving a scope and rotating a token need
explicit later contracts; neither is an implicit side effect of a label edit.

### 3. Aggregate enforcement and protected storage

**Proposed first catalog, pending an ADR:** the `limits` object must include
every field in this fixed catalog, with no silent defaults or unknown fields.
Each field is a nonnegative integer; zero forbids that resource. If unlimited
is supported, it must be an explicit `null` for that field, never omission.
Every ceiling is across the member scopes of one limit set:

| Field | What it bounds |
| --- | --- |
| `max_scopes` | Number of member scopes. |
| `max_bots` | Bot definitions. |
| `max_running_bots` | Bot actors running concurrently, distinct from definitions. |
| `max_graphs`, `max_nodes`, `max_edges` | Graph definitions and their parts. |
| `max_actions`, `max_steps`, `max_action_inputs` | Action definitions and parts. |
| `max_llms`, `max_system_one_profiles` | Scoped model profiles. |
| `max_outbound_messages_per_month` | Outbound messages admitted in a UTC month. |
| `max_stored_message_bytes` | Logical stored message content, not DB file size. |

This is a proposed **complete initial catalog**, not a promise to limit every
possible future resource. Shared instance-level model profiles are not
multiplied by scope. Define counting for variants, imported messages, queued
replies, failed delivery, deletion, and storage before implementation. A
retry of an already admitted outbound message must not consume another slot.
`max_running_bots` must be enforced on actor starts, restarts, and startup,
not merely checked when YAML is published. Decide precisely when a bot starts
and stops counting; do not mistake "paired" for "running". Reject lowering
a limit below current usage unless the ADR defines another safe outcome.

**Proposed SQLite schema**, separate from YAML behavior and token grants.
Nullable ceiling columns represent explicit unlimited values; the API still
requires every corresponding field in a provisioning request:

```sql
CREATE TABLE limit_sets (
  id TEXT PRIMARY KEY,
  policy_version TEXT NOT NULL,
  max_scopes INTEGER CHECK (max_scopes >= 0),
  max_bots INTEGER CHECK (max_bots >= 0),
  max_running_bots INTEGER CHECK (max_running_bots >= 0),
  max_graphs INTEGER CHECK (max_graphs >= 0),
  max_nodes INTEGER CHECK (max_nodes >= 0),
  max_edges INTEGER CHECK (max_edges >= 0),
  max_actions INTEGER CHECK (max_actions >= 0),
  max_steps INTEGER CHECK (max_steps >= 0),
  max_action_inputs INTEGER CHECK (max_action_inputs >= 0),
  max_llms INTEGER CHECK (max_llms >= 0),
  max_system_one_profiles INTEGER CHECK (max_system_one_profiles >= 0),
  max_outbound_messages_per_month INTEGER
    CHECK (max_outbound_messages_per_month >= 0),
  max_stored_message_bytes INTEGER CHECK (max_stored_message_bytes >= 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE limit_set_scopes (
  scope_key TEXT PRIMARY KEY,
  limit_set_id TEXT NOT NULL REFERENCES limit_sets(id)
);
CREATE INDEX limit_set_scopes_set ON limit_set_scopes(limit_set_id);
CREATE TABLE limit_usage (
  limit_set_id TEXT NOT NULL REFERENCES limit_sets(id),
  resource TEXT NOT NULL,
  period_start TEXT NOT NULL,
  amount INTEGER NOT NULL CHECK (amount >= 0),
  PRIMARY KEY (limit_set_id, resource, period_start)
);
```

Each scope belongs to at most one limit set; a set may be empty. Existing
manually authored scopes can remain outside any set until the owner attaches
them. Usage rows are for time-windowed or otherwise non-derivable counters;
derive definition counts from saved YAML and count running bots from the
fleet, not redundant tables. Existing token tables gain no limit columns.
Owner policy must be inaccessible to scoped publication, `GET` projections,
or token-grant changes. An owner may set a ceiling; even an owner publication
must honor the current ceiling until the policy changes explicitly.
Provisioning retries also need a durable record of request-key and payload
hashes plus committed IDs; finalize its schema together with the one-time
clear-token response rule. A memory-only idempotency map is insufficient
after a crash.

No new YAML fields are proposed for limits or group membership:
`config.schema.json` stays generated from Go behavior types and must not be
edited by hand. It already covers optional `display_name` and the existing
global `exclusive_scopes`. The proposed owner JSON request validates its
complete limits object separately; this work does not change YAML schema
unless the eventual ADR chooses YAML instead of protected SQLite policy.

Enforce projected totals while holding publication/provisioning locks; a
scoped caller must not bypass limits via two scopes, parallel requests, or a
direct API call. Manual YAML edits and startup must not activate over-limit
resources or silently delete existing data. For storage/inbound flows, define
a safe backpressure or refusal policy before enforcement. Group membership,
quota counters, and generated YAML span SQLite and files: design crash
recovery as one logical commit, not an assumed cross-store transaction.

ADR 0002 makes YAML canonical for **behavior** and currently describes SQLite
as runtime/credential storage. Update it and record a canonical policy ADR
before implementing owner policy in SQLite. The ADR must also settle exact
counter semantics, treatment of unmatched scopes, changing membership,
lowering ceilings, startup behavior, and the owner API's failure model.

## Wire examples (proposed, not implemented)

`GET /api/v1/scopes/scope_a7f3/config` returns only that saved scope, its
opaque version/ETag, and its active version/status. A missing `display_name`
stays absent in `config`; the UI may show `scope_a7f3` as fallback. To save
two existing scopes, a scoped token authorized for **both** may send:

```http
POST /api/v1/config/publications
Authorization: Bearer <scoped-or-owner-token>
Content-Type: application/json

{"scopes":{
  "scope_a7f3":{"version":"v-a","config":{"display_name":"Sales"}},
  "scope_b921":{"version":"v-b","config":{"display_name":"Support"}}
}}
```

Assume each `config` above is the **entire** saved scope; omitted entities
would be deleted. One stale or unauthorized scope rejects the whole request;
no partial saved scopes. Return a result for each scope with saved version,
active version, and active/pending status. `PUT` to one scope is a convenience
through the same publication pipeline. Never synthesize fallback labels into
the submitted config or change a scope key as part of publication.

An owner may perform a separate all-or-nothing initial registration:

```http
POST /api/v1/provisioning
Authorization: Bearer <owner-token>
Idempotency-Key: <opaque-request-key>
Content-Type: application/json

{"limits":{
  "max_scopes":3,"max_bots":4,"max_running_bots":2,
  "max_graphs":10,"max_nodes":50,"max_edges":100,
  "max_actions":20,"max_steps":80,"max_action_inputs":40,
  "max_llms":2,"max_system_one_profiles":2,
  "max_outbound_messages_per_month":10000,
  "max_stored_message_bytes":104857600
},"scopes":[{"display_name":"Sales"},{"display_name":"Support"}],
"access_token":{"name":"Primary access"}}
```

```json
{"limit_set_id":"ls_82a1","scopes":[
  {"key":"scope_a7f3","display_name":"Sales"},
  {"key":"scope_b921","display_name":"Support"}
],"access_token":{"id":42,"scopes":["scope_a7f3","scope_b921"],
"token":"<clear-value-shown-once>"}}
```

All 13 proposed limits are supplied, including `max_running_bots` separate
from `max_bots`. The host retains any mapping to its own policy names.
Botflota stores no such names on tokens or scopes. Decide safe retry behavior
for an owner request whose token-bearing response was lost: the stored token
is hashed and cannot simply be replayed in clear. Never solve retries by
logging or indefinitely storing that clear credential.

Publication outcomes: 200 for saved and active/no-op, 202 if any requested
scope is saved but pending, 428 for missing expected versions, 412 if any
version is stale, 409 for a quota or grant conflict, 422 for invalid config,
404 for an unknown/out-of-scope target, and 401 for invalid credentials.
The owner gets detailed diagnostics; a scoped caller must not learn paths,
other scopes, owner policy, or whether the failure came from an unauthorized
scope. This status sketch needs finalization with the canonical API contract.

## Acceptance gates

Phase 7 publication:

- Owner, authorized multi-scope token, and out-of-scope token: reads and
  writes obey authority, with 404 and no existence or policy disclosure.
- Scope split across the main file and nested includes: edit, add, and remove
  definitions, including scopes defined directly in the main file; preserve
  unrelated scopes, global settings, and includes.
- Two concurrent scopes in one file both survive; two stale writes to the
  same scope conflict. An identical write leaves files and runtime unchanged.
- One batch with two authorized scopes saves both or neither; a stale,
  unknown, or unauthorized member leaves all files unchanged and reveals
  nothing about a scope outside the caller's authority. Activation may be
  pending for one scope while another becomes active.
- Missing/empty and explicit `display_name`: round-trip without changing keys,
  references, guards, sessions, linked accounts, or token access. Editing a
  label in one scope cannot alter another; missing fallback is presentation
  only. A manual key edit remains a different identity.
- Invalid candidate or masked secret: no partial file write and no secret
  leakage. An injected failure after the first file replacement recovers
  entirely old or entirely new content, including after restart.
- Incompatible graph edit: YAML saved, runtime pending, unrelated scopes
  still active. Manual edits and newly matched includes remain watchable.

Advanced owner provisioning and limit policy in Phase 7:

- Provisioning rejects missing limit fields. A set may start empty, or an
  owner request may create policy, initial scopes, and a scoped token together
  with no half-finished results after error or restart. Manual authors keep
  their chosen scope keys. A scoped token cannot create scopes or change
  policy; new grants follow `exclusive_scopes` and never happen implicitly.
- Subsequent owner provisioning respects `max_scopes` and the other ceilings;
  lowering limits below use is rejected under the settled ADR. A credential
  replacement does not reset usage. Concurrent writes across two scopes
  cannot exceed one shared limit, including running bots and outbound use.
- Restart and manual edits cannot activate excess bots; a bot that is merely
  paired does not consume a running slot. Exercise accepted outbound replies,
  retries, and storage accounting under the settled ADR.
- Duplicate effective labels are rejected by the server within each stated
  namespace; equal labels across categories or scopes remain valid. Test
  create, rename, fallback-to-key, and collisions, including direct API calls.

When implementation begins, request build permission before checks. Use the
Make targets, add concurrency tests, and mark deliverables complete only in
the commit that proves them. Do not implement limit policy before its ADR
settles authority, counting, persistence, and recovery.
