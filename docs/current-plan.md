# Phase 7: API, limits, storage, and account history

Phase 7 plan for reading and publishing existing scopes by API, registering
owner-managed limit sets with generated-key scopes and scoped credentials,
and enforcing aggregate definition, running-bot, outbound-message, and stored
message limits across each set's scopes. It also settles the structural limits
that live in instance config instead of a limit set, the per-bot history
import ceiling, what happens when storage runs out, and the account history
operations (keep, delete, migrate). It covers proposed endpoints, SQLite
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
  `display_name`. Scope provisioning generates the stable scope key without
  setting `display_name`; interfaces initially show the key, and the owner may
  set a visible name later.
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
  actions, nodes, and LLM profiles. Future System One models (SOMs) and shared
  catalog entries use the same rule. A `PUT` that edits a label retains every
  map key; references continue to use keys. GET leaves absent labels absent; presentation may
  fall back to the key without changing the document or its ETag.
- For child-entity creation, the UI's initial `name` is the key. Reject a key
  collision within its map rather than silently changing or suffixing it. Do
  not persist a separate `display_name` until a later rename. A rename checks
  that its new effective label is unique in the relevant namespace, then
  writes `display_name` only; references and stored runtime identity remain
  key-based. Owner scope creation remains the generated-key exception in
  the next section, but does not set a display name.

For example, creating a bot named `Sales assistant` inserts a map entry under
that exact key and leaves `display_name` absent. Renaming it later to
`Regional sales` keeps the `Sales assistant` key and sets
`display_name: Regional sales`. References such as `bot.graph` and graph
`goto` values always contain keys. A key collision or duplicate effective
label rejects the whole publication; never auto-slug, overwrite, or silently
add a numeric suffix. Scope provisioning generates keys without requesting or
storing initial labels.
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
| `POST /api/v1/provisioning` | Create a limit set, its scopes, and a scoped access token. |
| `POST /api/v1/limit-sets` | Create an empty set with all limits; no scopes, no token. |
| `GET /api/v1/limit-sets/{id}` | Read limits, member scopes, computed usage, and period anchor. |
| `PUT /api/v1/limit-sets/{id}/limits` | Replace all limits, with a policy version. |
| `POST /api/v1/limit-sets/{id}/scopes` | Add scopes, optionally grant them to a token. |
| `PUT /api/v1/tokens/{id}/scopes` | Replace grants under owner authority. |

`POST /api/v1/provisioning` takes `Idempotency-Key`, a complete `limits`
object, a `scope_count` of at least one, and a token request (`name`, optional
`expires_at`). It creates exactly that many scopes, each with a generated key
and no `display_name`, and one access token authorized for all of them. The
owner can assign visible names later through ordinary scope publication. The
server anchors the set's first outbound quota period to the commit timestamp of
this provisioning operation; the request does not supply a billing date, and
the anchor is available from the limit-set read API, not the provisioning
response. An empty set is not a provisioning case: it comes from
`POST /api/v1/limit-sets`, and its anchor stays null until its first scope is
added. Botflota generates collision-checked stable scope keys, writes the new
YAML fragments, creates membership and the token, and returns the limit-set ID,
the scope keys, the token ID, and the clear token **once**. Manual YAML
authors still choose their own keys. How many scopes a set holds is the
owner's provisioning decision, so no ceiling on the scope count exists (see
the catalog below).
`exclusive_scopes` applies to every new or changed grant, including this API.
The owner credential stays with the host; it never reaches a scoped user.

Retries are idempotent: a durable record of the request key, the payload
hash, and the committed IDs answers a repeat with the original result instead
of creating scopes twice. If the operation committed but the clear token
never reached the caller, the repeat returns the created keys, the limit-set
ID, and the token ID **without** the clear value — it is stored hashed and
cannot be replayed — and the owner mints a replacement and revokes the first.
The clear credential is never logged and never stored beyond that one
response.

`POST /api/v1/limit-sets/{id}/scopes` accepts an array of requested scope
labels and an optional `grant_to_token_id`. The created keys join the set and
the grant is updated as one operation, or none of it is changed. A separate
owner token-grant update supports existing/manual scopes and credential
replacement; scoped callers cannot use either route. No policy is ever
embedded in a token. Deleting or moving a scope and rotating a token need
explicit later contracts; neither is an implicit side effect of a label edit.
If the set has no period anchor yet, adding its first scope sets the anchor to
that operation's commit timestamp; later scope additions do not move it.

### 3. Aggregate enforcement and protected storage

**Proposed first catalog, pending an ADR:** the `limits` object of a
provisioning request must include every field in this fixed catalog, with no
silent defaults or unknown fields. Each field is a nonnegative integer; zero
forbids that resource. If unlimited is supported, it must be an explicit
`null` for that field, never omission. Every ceiling is across the member
scopes of one limit set:

| Field | What it bounds |
| --- | --- |
| `max_bots` | Bot definitions. |
| `max_running_bots` | Bot actors running concurrently, distinct from definitions. |
| `max_graphs`, `max_actions` | Graph and action definitions. |
| `max_llms`, `max_soms` | LLM and System One model (SOM) definitions. |
| `max_outbound_messages_per_month` | Outbound messages in the set's anchored UTC monthly period. |
| `max_stored_message_bytes` | Logical stored message content, not DB file size. |

Settled for this catalog:

- **No `max_scopes`.** The owner provisions the scopes a set holds and may add
  or remove them later, so the scope count is decided where the scopes are
  created, not enforced a second time. A host expresses its own tiers by
  provisioning that many scopes; Botflota knows none of those tiers.
- **Aggregate versus structural.** `max_bots`, `max_graphs`, and `max_actions`
  are ceilings the set's scopes share: three scopes drawing from one set do
  not each get their own copy. Nodes, edges, steps, and action inputs are not
  aggregate ceilings and are not multiplied by scope or set; see the
  structural limits below.
- **`max_stored_message_bytes` counts logical content, per set.** It sums the
  UTF-8 length of the stored `body` and `content` columns for every message
  row of the set's scopes — inbound, outbound, and imported alike — which is
  what `octet_length()` returns in SQLite. It is deliberately not the size of
  the `.db` file: indexes, free pages, WAL, the whatsmeow device store, chats,
  sessions, and contacts are outside it, so the ceiling is a content promise,
  not a disk promise. A retry that stores no new row costs nothing.
- **`max_outbound_messages_per_month` stays separate.** It bounds newly
  admitted outbound messages in the current monthly period and says nothing
  about storage: inbound and imported messages grow storage without touching
  it. The period is a rolling calendar month from the limit set's anchor, not
  the first through last day of a calendar month.

What counts, and when (settled):

- **Definitions** — `max_bots`, `max_graphs`, `max_actions`, `max_llms`, and
  `max_soms` count the entities in the **saved YAML** of every scope of the
  set, whether or not they run. Running bots have their own ceiling beside
  them.
- **`max_running_bots`** holds a slot from the moment a bot's actor starts
  until it stops. The slot is reserved before the start, so two starts at
  once cannot slip past the ceiling; a paired but stopped bot holds none, and
  "paired" is never mistaken for "running".
- **`max_outbound_messages_per_month`** counts outbound messages admitted to
  the outbox during the set's current period. Query outbox rows joined through
  their messages, chats, and the set's scopes; this excludes history-sync
  imports, which never enter the outbox. Admission and message storage happen
  in the same turn transaction, so a retry adds no row and spends no extra
  slot, while a failed delivery remains counted. Use `outbox.created_at` and
  the UTC period boundaries.
- **`max_stored_message_bytes`** is checked as content is written: a row the
  dedup index rejects stores no bytes and spends none, and deleting messages
  returns their bytes to the ceiling.
- **`settings.history_import_max_bytes`** measures imported content still
  stored under that bot. Unlinking keeps the history, so the usage stays with
  the bot; only deleting history releases it.

Usage is calculated from its source when requested or enforced: definitions
from saved YAML, running bots from the fleet, and message counts and bytes
from SQLite queries over the set's member scopes. There is no generic usage
counter table and no duplicate counter to reconcile. The outbound query uses
the set's anchored period; storage and per-bot history use their respective
stored rows.

Each limit set has one UTC monthly-period anchor. Provisioning sets it to the
successful commit timestamp, not a date supplied by the host; the limit-set
read returns it, and retries with the same idempotency key preserve it. A set
created empty through `POST /api/v1/limit-sets` leaves the anchor unset until
the owner adds its first scope; that operation sets it to its commit
timestamp. Removing all scopes later does not reset an existing anchor.
Periods end at the same UTC time on the corresponding day of the
following month; if that month lacks the anchor day, use its last day, then
return to the original anchor day in later months (January 31 → February's
last day → March 31).

Because outbound use is queried from outbox rows, explicit history deletion
also removes the associated rows from the query and can restore monthly
outbound capacity. This is a consequence of avoiding a durable usage ledger;
the ADR must explicitly accept that behavior or retain a dedicated outbound
ledger.

Lowering a ceiling below current usage is refused, with the usage and the
requested maximum in the error: Botflota never deletes a definition, stops a
bot, or drops history to make a smaller number true. The owner frees the
resource first, then lowers the ceiling. `settings.history_import_max_bytes`
is the exception that shows it is a preference rather than protected policy:
it may be lowered below what is already stored, which stops further import
and leaves the existing history alone.

This is a proposed **complete initial catalog**, not a promise to limit every
possible future resource. Shared instance-level model profiles are not
multiplied by scope. Counting for message variants and how explicit history
deletion affects the outbound count are still for the ADR to settle; the
rest of this section is decided above, and the ADR records it instead of
reopening it.

**Structural limits (instance config, not a limit set).** A graph may hold at
most N nodes, a node at most N edges, an action at most N steps and N inputs.
These bound how large a single definition may grow, so a fixed ceiling per
object is enough: it neither multiplies by scope nor belongs to any set. They
are instance settings in the main config file, in one object that holds only
these — named apart from the provisioning request's `limits`, which is the
owner's policy:

```yaml
definition_limits:
  max_nodes_per_graph: 60
  max_edges_per_node: 20
  max_steps_per_action: 12
  max_action_inputs: 12
```

Each is a positive integer — `0` and negatives are validation errors, because
"no definition at all" is not a structural limit — and each has a default, so
an instance that never sets them still enforces the shipped values. They are
**restart-required**, like other global settings (ADR 0009), and they are
generated into `config.schema.json` from their Go types in the same commit
that adds them. The aggregate ceilings are deliberately absent from this
object: they are the limit set in SQLite.

**History import ceiling (bot settings, not a limit set).**
`settings.history_import_max_bytes` bounds the imported message content one
bot keeps for its linked account, in bytes:

```yaml
scopes:
  support:
    bots:
      assistant:
        settings:
          history_sync: true
          history_import_max_bytes: 20971520  # 20 MiB
```

The ceiling sits on the bot because the import belongs to that bot and its
linked account, not to the scope or the set: unlinking, another bot, or a
peer scope never spend it. A host computes the number outside Botflota from
whatever it sells — Botflota stores no tier and no percentage. A principal
authorized for the scope edits it as ordinary bot configuration; it is not a
protected ceiling, so `max_stored_message_bytes` is the only irreducible
bound. A pairing imports until either ceiling stops it and reports the
import as partial.

**Storage exhaustion (settled for the MVP, pending the ADR's wording).** When
a set has no room under `max_stored_message_bytes`:

- history import stops first and reports how much it stored and that the
  import is partial;
- a bot of that set **pauses its turns**: it keeps its connection, but no new
  turn is processed, so nothing is answered without being stored and the
  redelivery guarantee of ADR 0040 holds. The pause protects consistency,
  not delivery — WhatsApp decides whether it retransmits what arrived while
  the bot was paused, so the reported state must tell an operator that
  messages may have been missed;
- the condition is visible on the bot's status, not only in a log line, and
  freeing space or raising the ceiling resumes it.

Beyond the MVP this stays open: answering while storing nothing, or a
retention flag that drops the oldest messages to make room for the new ones.
Both need the dedup and outbox consequences settled first (ROADMAP Phase 16).

**Proposed SQLite schema**, separate from YAML behavior and token grants.
Nullable ceiling columns represent explicit unlimited values; the API still
requires every corresponding field in a provisioning request:

```sql
CREATE TABLE limit_sets (
  id TEXT PRIMARY KEY,
  policy_version TEXT NOT NULL,
  max_bots INTEGER CHECK (max_bots >= 0),
  max_running_bots INTEGER CHECK (max_running_bots >= 0),
  max_graphs INTEGER CHECK (max_graphs >= 0),
  max_actions INTEGER CHECK (max_actions >= 0),
  max_llms INTEGER CHECK (max_llms >= 0),
  max_soms INTEGER CHECK (max_soms >= 0),
  max_outbound_messages_per_month INTEGER
    CHECK (max_outbound_messages_per_month >= 0),
  outbound_period_start TEXT, -- NULL until the set's first scope is added
  max_stored_message_bytes INTEGER CHECK (max_stored_message_bytes >= 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE limit_set_scopes (
  scope_key TEXT PRIMARY KEY,
  limit_set_id TEXT NOT NULL REFERENCES limit_sets(id)
);
CREATE INDEX limit_set_scopes_set ON limit_set_scopes(limit_set_id);
```

Each scope belongs to at most one limit set; a set may be empty. Existing
manually authored scopes can remain outside any set until the owner attaches
them. `outbound_period_start` is NULL only until the first scope joins an
empty set. Usage is queried from saved YAML, the fleet, and message rows;
there is no `limit_usage` table. Existing token tables gain no limit columns.
Owner policy must be inaccessible to scoped publication, `GET` projections,
or token-grant changes. An owner may set a ceiling; even an owner publication
must honor the current ceiling until the policy changes explicitly.
Provisioning retries also need a durable record of request-key and payload
hashes plus committed IDs; finalize its schema together with the one-time
clear-token response rule. A memory-only idempotency map is insufficient
after a crash.

The limit-set catalog itself stays out of YAML: it is protected SQLite
policy, and `config.schema.json` stays generated from Go behavior types and
must not be edited by hand. It already covers optional `display_name` and the
existing global `exclusive_scopes`. The structural limits and
`settings.history_import_max_bytes` are the exception — they are config, so
they arrive with their Go types, a regenerated schema, and a
`config/examples/reference.yml` entry in the same commit. The proposed owner
JSON request validates its complete limits object separately; the ADR still
decides whether owner policy stays in SQLite or moves to YAML.

Enforce projected totals while holding publication/provisioning locks; a
scoped caller must not bypass limits via two scopes, parallel requests, or a
direct API call. The storage refusal policy is settled above: import stops,
then the affected bot pauses its turns; it never answers a message it could
not store, so the pause stays inside the atomic turn of ADR 0040.

**Manual edits and startup.** A hand-edited file that exceeds a ceiling is
never repaired by deleting or rewriting it. While the instance runs, the
active configuration stays and the violation is reported with the scope, the
object, the usage, and the ceiling; at a cold start the affected scope is
refused with the same diagnostic instead of being trimmed to fit — Botflota
never picks which bots to drop. Scopes outside every limit set keep their
current meaning: valid configuration with no set ceilings.

**All-or-nothing across YAML and SQLite.** Membership, counters, and
generated YAML span files and the database, and a SQLite transaction cannot
cover a file. So an operation first prepares and validates everything, then
records itself durably in SQLite with its request key and payload hash, then
writes the affected files recoverably, and finally commits its SQLite rows
and clears that record. Startup finishes or reverts a pending record before
normal config load, so a crash leaves either the whole operation or none of
it — never a scope without its membership, and never a token without the
scopes it was minted for.

ADR 0002 makes YAML canonical for **behavior** and currently describes SQLite
as runtime/credential storage. Update it and record a canonical policy ADR
before implementing owner policy in SQLite. That ADR records the rules
settled above — counting, lowering a ceiling, startup, manual edits, and the
all-or-nothing cross-store commit — and still settles treatment of unmatched
scopes, changing membership, counting for variants and queued replies, and
the owner API's failure model.

### 4. Account history: keep, delete, migrate

Unlinking is not deletion, and deletion is never implied by an account
change:

- **Unlink keeps everything.** `DELETE .../bots/{bot}/account` and `bot unpair`
  remove the credentials, the account record, and the pairing status, and fail
  the replies the old link still owes (ADR 0040). Chats, sessions, messages,
  sender profiles, and contacts stay exactly as they are, so a temporary
  unlink or a later replacement never costs history.
- **Deleting history is its own explicit operation**, confirmed by the caller
  and addressed to one `(scope, bot)`: it drops that bot's chats, and with
  them the cascaded sessions, messages, and queued replies, plus its contacts.
  It never touches the scope's `senders` — those are shared by the scope's
  bots (ADR 0029) — and it never touches another bot. The API reports what
  was removed, and the storage it freed is counted back into the limit set.
  Nothing else calls it: no pairing, replacement, or config edit deletes
  history as a side effect.
- **Migration transfers history, not identity.** A migration moves a bot's
  chats, messages, and contacts to another bot **of the same scope**, so a
  recreated bot or a peer bot starts from the same conversations and Botflota
  treats the result as an ordinary bot. Scope never changes: references and
  sessions never cross scopes (ADR 0016), so there is no cross-scope move.
  Senders are already shared by the scope and need no copy. A bot that only
  relinks its account needs no migration at all — its history is keyed by
  bot, not by account.

Migration is a **move**, not a copy: two bots holding the same history would
cost storage and prove nothing. The operation therefore runs with both bots
stopped, settles the source's pending outbox first — the old link must never
send what it no longer owns, so a non-empty queue is failed explicitly rather
than carried over — and ends the source's active sessions instead of
pretending the destination's graph sits at the same node. Chats, messages,
and contacts transfer in one recoverable operation that lands all of them or
none, and that a repeat does not duplicate. Senders need no transfer: they
already belong to the scope.

Both operations are owner-and-scoped like any other write to their scope,
and neither bypasses a ceiling: content counts against the destination's set
once, not twice.

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
  "max_bots":4,"max_running_bots":2,
  "max_graphs":10,"max_actions":20,
  "max_llms":2,"max_soms":2,
  "max_outbound_messages_per_month":10000,
  "max_stored_message_bytes":104857600
},"scope_count":2,
"access_token":{"name":"Primary access"}}
```

```json
{"limit_set_id":"ls_82a1","scopes":["scope_a7f3","scope_b921"],
"access_token_id":42,"token":"<clear-value-shown-once>"}
```

All 8 proposed limits are supplied, including `max_running_bots` separate
from `max_bots`. The host retains any mapping to its own policy names.
Botflota stores no such names on tokens or scopes. The requested scope count
is not a limit but the tier itself: Botflota creates exactly that many scopes
and knows nothing about a ceiling above them. A repeat with the same
`Idempotency-Key` returns the original result instead of provisioning again;
a retry whose token-bearing response was lost returns the created IDs but
never the clear token, so the owner mints a replacement and revokes the
first. Never solve retries by logging or indefinitely storing that clear
credential.

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

- Provisioning rejects missing limit fields, a `scope_count` below one, or a
  missing token name. One owner request creates policy, the requested number
  of scopes, and the scoped token together
  with no half-finished results after error or restart. Manual authors keep
  their chosen scope keys. A scoped token cannot create scopes or change
  policy; new grants follow `exclusive_scopes` and never happen implicitly.
- Repeating a provisioning request with the same `Idempotency-Key` returns
  the original IDs instead of provisioning a second time, and a response whose
  clear token was lost returns those IDs without it; the owner mints a
  replacement, and no path ever logs or stores the clear credential.
- Provisioning anchors the outbound period to its successful commit time; the
  limit-set read returns it, and retry preserves it. A set created empty
  through `POST /api/v1/limit-sets` has no anchor, and adding its first scope
  sets the anchor once. Periods roll by calendar month from that UTC instant,
  including the Jan-31 → Feb-last-day → Mar-31 case.
- Subsequent owner provisioning respects the ceilings already stored, and the
  scope count only ever follows the owner's provisioning requests; lowering
  limits below use is rejected under the settled ADR. A credential
  replacement does not reset usage. Concurrent writes across two scopes
  cannot exceed one shared limit, including running bots and outbound use.
- Restart and manual edits cannot activate excess bots; a bot that is merely
  paired does not consume a running slot. Exercise accepted outbound replies,
  retries, and storage accounting under the settled ADR.
- Duplicate effective labels are rejected by the server within each stated
  namespace; equal labels across categories or scopes remain valid. Test
  create, rename, fallback-to-key, and collisions, including direct API calls.
- Structural limits: a graph, node, or action over its instance maximum fails
  `config validate` with the object named; `0` and negative values are
  rejected as instance settings; changing one keeps the runtime on the old
  value until restart, like every other global setting.
- Lowering a ceiling below current use is refused with the usage and the
  requested maximum in the error, and nothing is deleted to make it fit. A
  hand-edited file that exceeds a ceiling leaves the active configuration
  untouched and the file unchanged; a cold start refuses the scope instead of
  choosing which resources to drop.

Storage and history import:

- Reaching `max_stored_message_bytes` stops import first (reported as
  partial, with what it stored), then pauses the affected bots' turns with
  the state visible on the bot's status; no message is answered without being
  stored. Freeing space or raising the ceiling resumes turns without a
  restart.
- `history_import_max_bytes` bounds one bot only: its import stops at the
  ceiling while peer bots and the rest of the scope keep importing, and a
  value above the set's remaining storage still cannot push the set past
  `max_stored_message_bytes`. The ceiling is ordinary bot config, so a
  scoped principal may raise it inside its own scope; only the limit set
  stays out of its reach.
- Storage accounting matches the documented definition: the sum of `body` and
  `content` bytes over the set's scopes, so imported, inbound, and outbound
  rows all count and duplicate rows never do.
- Outbound use is computed from the set's outbox rows and their `created_at`
  values inside the anchored period; imported history never counts, failed
  deliveries do, retries do not, and history deletion's effect on that count
  matches the policy the ADR settles. No generic usage table or duplicate
  persisted counter exists.

Account history:

- Unlink, replace, pair, and unpair leave chats, sessions, messages, and
  contacts untouched, and the next pairing resumes them.
- Explicit history deletion removes exactly one bot's history and contacts,
  never the scope's senders or another bot, reports what it removed, and
  returns the storage to its limit set.
- Migration moves a bot's chats, messages, and contacts to another bot of the
  same scope, never across scopes, with both bots stopped, the source's
  queued replies failed before anything transfers, and its active sessions
  ended; it lands all of them or none after an error or restart, and a repeat
  does not duplicate them.

When implementation begins, request build permission before checks. Use the
Make targets, add concurrency tests, and mark deliverables complete only in
the commit that proves them. Do not implement limit policy before its ADR
records the settled rules above — authority, counting, lowering, recovery,
and failure behavior.
