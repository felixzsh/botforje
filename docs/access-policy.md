# Resource policy and provisioning — TBD

ADR 0046 separates runtime protections from the policies of a host application.
Nothing in this document is implemented or an approved enforcement contract.
Publication and account history remain independent capabilities
(`current-plan.md`); they do not require limit sets.

## Decisions requiring creator approval

- **TBD — aggregate policy ownership and mechanism.** Whether Botflota needs
  aggregate ceilings at all, and which independently useful enforcement
  primitives it should expose. No mandatory limit-set catalog is approved.
- **TBD — exact versus eventual enforcement.** The host can reject a proposed
  configuration before publication, but automatic replies execute inside the
  runtime. Webhooks report facts after execution and cannot enforce an exact
  admission ceiling. Decide required guarantees before designing the seam.
- **TBD — periods and durable usage.** The host owns interpretation and period
  selection. No provision-time monthly anchor is a runtime decision. Decide
  whether any runtime ledger is needed, how retries count, and whether deleting
  history may affect usage. Stored history is not automatically a durable ledger.
- **TBD — scope provisioning and grants.** Decide minimal owner operations,
  naming, idempotency, token replacement, and file/database recovery without
  coupling scope creation to a resource-policy group. Current publication only
  changes existing scopes; current token grants change by mint and revoke.
- **TBD — exhausted storage and lowered ceilings.** Choose explicit behavior,
  including inbound handling, loss risk, retention, and recovery. There is no
  implemented policy that pauses a scope at an aggregate content ceiling.
- **TBD — technical protection defaults.** Structural limits, bounded imports,
  timeouts, and backpressure must be justified by execution safety, not by an
  external offering. Proposed new configuration fields are not available yet.

The shared-process threat model and optional implicit-scope syntax are separate
TBD decisions in `integration.md` and `PLAN.md`. No new implementation starts
until the relevant decision is accepted in an ADR.

## Candidate design for evaluation only

The remaining sections describe one possible design, not a required catalog,
settled semantics, route contract, or implementation gate. Every detail below
is TBD under the decisions above; omit or replace it when it does not earn a
runtime responsibility. In particular, combined provisioning, mandatory
ceilings, monthly anchors, and protected policy storage are not commitments.

## Ownership and boundary

- The host assigns numeric ceilings; Botflota never knows why it chose them.
  A **limit set** is an internal, owner-managed policy joining several scopes
  under shared ceilings. It is not a scope, a UI group, or a token.
- The current `access_tokens` and `access_token_scopes` tables continue to
  express only authority. Limits never live in a token, and a token never
  carries policy. Renewing or revoking a credential does not reset usage.
- Only owner operations create scopes, policies, or grants. A scoped token
  cannot create scopes, change its grants, or edit owner policy.
- Owner policy must be inaccessible to scoped publication, `GET` projections,
  or token-grant changes. An owner may set a ceiling; even an owner
  publication must honor the current ceiling until the policy changes
  explicitly.
- External groupings belong to the host. Scope count does not define a
  runtime policy group, and provisioning need not create policy.

Proposed owner endpoints (all absent today):

| Method and path | Purpose |
| --- | --- |
| `POST /api/v1/provisioning` | Create a limit set, its scopes, and a scoped access token. |
| `POST /api/v1/limit-sets` | Create an empty set with all limits; no scopes, no token. |
| `GET /api/v1/limit-sets/{id}` | Read limits, member scopes, computed usage, and period anchor. |
| `PUT /api/v1/limit-sets/{id}/limits` | Replace all limits, with a policy version. |
| `POST /api/v1/limit-sets/{id}/scopes` | Add scopes, optionally grant them to a token. |
| `PUT /api/v1/tokens/{id}/scopes` | Replace grants under owner authority. |

## Provisioning

`POST /api/v1/provisioning` takes `Idempotency-Key`, a complete `limits`
object, a `scope_count` of at least one, and a token request (`name`, optional
`expires_at`). It creates exactly that many scopes, each with a generated key
and no `display_name`, and one access token authorized for all of them. Naming
those scopes is a later configuration edit, not part of provisioning. The
server anchors the set's first outbound quota period to the commit timestamp of
this operation; the request does not supply a period boundary, and the anchor is
available from the limit-set read API, not the provisioning response. An empty
set is not a provisioning case: it comes from `POST /api/v1/limit-sets`, and
its anchor stays null until its first scope is added. Botflota generates
collision-checked keys, writes the YAML fragments, creates membership and the
token, and returns the limit-set ID, the scope keys, the token ID, and the
clear token **once**. How many scopes a set holds is the host's provisioning
decision, so no ceiling on the scope count exists. `exclusive_scopes` applies
to every new or changed grant, including this API. The owner credential stays
with the host; it never reaches a scoped user.

Retries are idempotent: a durable record of the request key, the payload hash,
and the committed IDs answers a repeat with the original result instead of
creating scopes twice. If the operation committed but the clear token never
reached the caller, the repeat returns the created keys, the limit-set ID, and
the token ID **without** the clear value — it is stored hashed and cannot be
replayed — and the owner mints a replacement and revokes the first. The clear
credential is never logged and never stored beyond that one response.

`POST /api/v1/limit-sets/{id}/scopes` accepts a requested number of scopes and
an optional `grant_to_token_id`. The created keys join the set and the grant is
updated as one operation, or none of it is changed. A separate owner
token-grant update supports existing scopes and credential replacement;
scoped callers cannot use either route. Deleting or moving a scope and rotating
a token need explicit later contracts. If the set has no period anchor yet,
adding its first scope sets the anchor to that operation's commit timestamp;
later scope additions do not move it.

## Limit catalog

**Proposed first catalog, pending an ADR:** the `limits` object must include
every field in this fixed catalog, with no silent defaults or unknown fields.
Each field is a nonnegative integer; zero forbids that resource. If unlimited
is supported, it must be an explicit `null`, never omission. Every ceiling is
across the member scopes of one limit set:

| Field | What it bounds |
| --- | --- |
| `max_bots` | Bot definitions. |
| `max_running_bots` | Bot actors running concurrently, distinct from definitions. |
| `max_graphs`, `max_actions` | Graph and action definitions. |
| `max_llms`, `max_soms` | LLM and System One model (SOM) definitions. |
| `max_outbound_messages_per_month` | Outbound messages in the set's anchored UTC monthly period. |
| `max_stored_message_bytes` | Logical stored message content, not DB file size. |

Candidate semantics — TBD:

- **No `max_scopes`.** The host provisions the scopes a set holds and may add
  or remove them later, so the count is decided where the scopes are created,
  not enforced a second time.
- **Aggregate versus structural.** `max_bots`, `max_graphs`, and `max_actions`
  are ceilings the set's scopes share. Nodes, edges, steps, and action inputs
  are not aggregate ceilings; see the structural limits below.
- **`max_stored_message_bytes` counts logical content, per set.** It sums the
  UTF-8 length of the stored `body` and `content` columns for every message row
  of the set's scopes — inbound, outbound, and imported alike — which is what
  `octet_length()` returns in SQLite. It is deliberately not the size of the
  `.db` file: indexes, free pages, WAL, the whatsmeow device store, chats,
  sessions, and contacts are outside it. A retry that stores no new row costs
  nothing.
- **`max_outbound_messages_per_month` stays separate.** It bounds newly
  admitted outbound messages in the current monthly period and says nothing
  about storage: inbound and imported messages grow storage without touching
  it. The period is a rolling calendar month from the limit set's anchor, not
  the first through last day of a calendar month.

Candidate counting semantics — TBD:

- **Definitions** — `max_bots`, `max_graphs`, `max_actions`, `max_llms`, and
  `max_soms` count the entities in the **saved YAML** of every scope of the
  set, whether or not they run. Running bots have their own ceiling beside
  them.
- **`max_running_bots`** holds a slot from the moment a bot's actor starts
  until it stops. The slot is reserved before the start, so two starts at once
  cannot slip past the ceiling; a paired but stopped bot holds none, and
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
the owner adds its first scope. Removing all scopes later does not reset an
existing anchor. Periods end at the same UTC time on the corresponding day of
the following month; if that month lacks the anchor day, use its last day, then
return to the original anchor day in later months (January 31 → February's
last day → March 31).

Because outbound use is queried from outbox rows, explicit history deletion
also removes the associated rows from the query and can restore monthly
outbound capacity. This is a consequence of avoiding a durable usage ledger;
the ADR must explicitly accept that behavior or retain a dedicated outbound
ledger.

Lowering a ceiling below current usage is refused, with the usage and the
requested maximum in the error: Botflota never deletes a definition, stops a
bot, or drops history to make a smaller number true. The host frees the
resource first, then lowers the ceiling.

This is a proposed **complete initial catalog**, not a promise to limit every
possible future resource. Shared instance-level model profiles are not
multiplied by scope. Counting for message variants and how explicit history
deletion affects the outbound count are still for the ADR to settle; the rest
of this section is also TBD. An accepted ADR must resolve it before implementation.

## Ceilings that live in config

**Structural limits (instance config, not a limit set).** A graph may hold at
most N nodes, a node at most N edges, an action at most N steps and N inputs.
These bound how large a single definition may grow, so a fixed ceiling per
object is enough: it neither multiplies by scope nor belongs to any set. They
are instance settings in the main config file, restart-required like other
global settings (ADR 0009), generated into `config.schema.json` from their Go
types in the commit that adds them. Each is a positive integer with a default;
`0` and negatives are validation errors.

**History import ceiling (bot settings, not a limit set).**
`settings.history_import_max_bytes` bounds the imported message content one
bot keeps for its linked account, in bytes. The ceiling sits on the bot because
the import belongs to that bot and its linked account: unlinking, another bot,
or a peer scope never spends it. It is ordinary bot configuration, editable by
a principal authorized for the scope, so it is a preference rather than
protected policy. A pairing imports until either ceiling stops it and reports
the import as partial.

Both can be adopted without the rest of this document; they are listed here
because they bound the same resources.

## Storage exhaustion

When a set has no room under `max_stored_message_bytes`:

- history import stops first and reports how much it stored and that the
  import is partial;
- a bot of that set **pauses its turns**: it keeps its connection, but no new
  turn is processed, so nothing is answered without being stored and the
  redelivery guarantee of ADR 0040 holds. The pause protects consistency, not
  delivery — WhatsApp decides whether it retransmits what arrived while the bot
  was paused, so the reported state must tell an operator that messages may
  have been missed;
- the condition is visible on the bot's status, not only in a log line, and
  freeing space or raising the ceiling resumes it.

Beyond the MVP this stays open: answering while storing nothing, or a
retention flag that drops the oldest messages to make room for the new ones
(ROADMAP Phase 16).

## Proposed storage

Separate from YAML behavior and token grants. Nullable ceiling columns
represent explicit unlimited values; the API still requires every
corresponding field in a provisioning request:

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
Provisioning retries also need a durable record of request-key and payload
hashes plus committed IDs; finalize its schema together with the one-time
clear-token response rule. A memory-only idempotency map is insufficient after
a crash.

The limit-set catalog itself stays out of YAML: it is protected SQLite policy,
and `config.schema.json` stays generated from Go behavior types and must not be
edited by hand. The proposed owner JSON request validates its complete limits
object separately; the ADR still decides whether owner policy stays in SQLite
or moves to YAML.

Enforce projected totals while holding publication/provisioning locks; a
scoped caller must not bypass limits via two scopes, parallel requests, or a
direct API call. The candidate storage refusal policy is TBD: import stops,
then the affected bot pauses its turns; it never answers a message it could
not store, so the pause stays inside the atomic turn of ADR 0040.

**Manual edits and startup.** A hand-edited file that exceeds a ceiling is
never repaired by deleting or rewriting it. While the instance runs, the
active configuration stays and the violation is reported with the scope, the
object, the usage, and the ceiling; at a cold start the affected scope is
refused with the same diagnostic instead of being trimmed to fit — Botflota
never picks which bots to drop. Scopes outside every limit set keep their
current meaning: valid configuration with no set ceilings.

**All-or-nothing across YAML and SQLite.** Membership and generated YAML span
files and the database, and a SQLite transaction cannot cover a file. So an
operation first prepares and validates everything, then records itself durably
in SQLite with its request key and payload hash, then writes the affected files
recoverably, and finally commits its SQLite rows and clears that record.
Startup finishes or reverts a pending record before normal config load, so a
crash leaves either the whole operation or none of it — never a scope without
its membership, and never a token without the scopes it was minted for.

ADR 0002 makes YAML canonical for **behavior** and currently describes SQLite
as runtime/credential storage. Update it and record a canonical policy ADR
before implementing owner policy in SQLite. That ADR must decide the candidate
rules above — counting, lowering a ceiling, startup, manual edits, and the
all-or-nothing cross-store commit — and still settles treatment of unmatched
scopes, changing membership, counting for variants and queued replies, and the
owner API's failure model.

## Wire example (proposed, not implemented)

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

All 8 proposed limits are supplied, including `max_running_bots` separate from
`max_bots`. The host retains any mapping to its own policy names. A repeat with
the same `Idempotency-Key` returns the original result instead of provisioning
again; a retry whose token-bearing response was lost returns the created IDs but
never the clear token. Never solve retries by logging or indefinitely storing
that clear credential.

## Acceptance gates

- Provisioning rejects missing limit fields, a `scope_count` below one, or a
  missing token name. One owner request creates policy, the requested number
  of scopes, and the scoped token together with no half-finished results after
  error or restart. A scoped token cannot create scopes or change policy; new
  grants follow `exclusive_scopes` and never happen implicitly.
- Repeating a request with the same `Idempotency-Key` returns the original IDs
  instead of provisioning a second time, and a response whose clear token was
  lost returns those IDs without it; the owner mints a replacement, and no path
  ever logs or stores the clear credential.
- Provisioning anchors the outbound period to its successful commit time; the
  limit-set read returns it, and retry preserves it. A set created empty
  through `POST /api/v1/limit-sets` has no anchor, and adding its first scope
  sets the anchor once. Periods roll by calendar month from that UTC instant,
  including the Jan-31 → Feb-last-day → Mar-31 case.
- Lowering a ceiling below current use is refused with the usage and the
  requested maximum in the error, and nothing is deleted to make it fit. A
  hand-edited file that exceeds a ceiling leaves the active configuration
  untouched and the file unchanged; a cold start refuses the scope instead of
  choosing which resources to drop.
- Restart and manual edits cannot activate excess bots; a bot that is merely
  paired does not consume a running slot. Concurrent writes across two scopes
  cannot exceed one shared limit, including running bots and outbound use.
- Storage accounting matches the documented definition: the sum of `body` and
  `content` bytes over the set's scopes, so imported, inbound, and outbound
  rows all count and duplicate rows never do. Reaching the ceiling stops import
  first, then pauses the affected bots' turns with the state visible on the
  bot's status; freeing space or raising the ceiling resumes turns without a
  restart.
- Outbound use is computed from the set's outbox rows and their `created_at`
  values inside the anchored period; imported history never counts, failed
  deliveries do, retries do not, and history deletion's effect on that count
  matches the policy the ADR settles. No generic usage table or duplicate
  persisted counter exists.

When implementation begins, request build permission before checks. Do not
implement this candidate before an ADR resolves the TBD decisions above.
