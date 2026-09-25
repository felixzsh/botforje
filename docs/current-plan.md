# Phase 7: configuration publication and account history

Phase 7 plan for the capabilities Botflota needs on its own, whatever hosts it:
reading and publishing **existing** scopes by API, the visible-name rules that
make those edits safe, and the account history operations (keep, delete,
migrate). It covers the proposed endpoints, concurrency, recovery, and
acceptance tests. Nothing below is implemented unless marked current.

What is deliberately **not** here: creating scopes, changing token grants, and
every ceiling or quota. Those belong to the host's access policy and may be
redesigned with it (`docs/access-policy.md`). This document never creates a
scope or a credential, and it never needs a limit set to be useful. Phase 14
uses these APIs in the scoped surface. Do not start Phase 8 or build the
dashboard here.

## Baseline

- YAML remains the source of truth for behavior (ADR 0002). The loader merges
  nested includes and records each entity's source file; config validation
  checks the whole candidate. SQLite holds runtime state and credentials
  (ADR 0006).
- Today `GET /api/v1/scopes/{scope}/config` reads the active snapshot. There is
  no publication API; saved valid YAML and active runtime may differ (ADR
  0009).
- The owner can address any scope. A scoped token can authorize one or more
  scopes and can both read and write them; no read-only scoped token is
  planned. It cannot create scopes, change its own grants, or edit owner
  policy. Out-of-scope targets look unknown (404; `docs/api.md`). The merged
  `exclusive_scopes` setting makes grants exclusive when enabled; by default
  sharing is allowed. This plan reuses those credentials unchanged.
- YAML map keys remain identities, not display labels (ADR 0016). Optional
  `display_name` is editable; when absent or empty, an interface shows the key.
  A label edit must preserve references, sessions, guards, linked accounts, and
  token authorization. The API must not materialize the fallback in saved YAML.
  Manual key changes are deletion plus creation, not inferred renames. The
  watcher never writes IDs into manual files.
- No config revision history or general rollback. An opaque logical version per
  scope prevents stale API writes; a multi-scope publication carries one
  expected version per scope. These are not schema versions or archives.
- Quotas do not exist yet, so a publication is validated against the config
  itself. When ceilings arrive (`docs/access-policy.md`), the same writer takes
  them into account without changing this contract.

## Delivery order

### 1. Read and publish existing scopes

Keep Phase 7's `PLAN.md` publication deliverable open until its tests pass.
ADR 0002 permits edits to authorized scopes even when they live in the main
file. Preserve global settings and every unaddressed scope. Existing
definitions stay in their source files; new ones use managed fragments and the
main include list may be updated to reach them. Includes are recommended
organization, not an API requirement; formatting is not a config contract.

- `GET /api/v1/scopes/{scope}/config` should return the **saved** logical scope,
  its opaque version, and the active version/status. An editor must never base
  a write on an older active snapshot while valid YAML is pending.
- A version-checked `PUT /api/v1/scopes/{scope}/config` replaces one existing
  logical scope. `POST /api/v1/config/publications` publishes a nonempty set of
  existing scopes as **one** saved change. Both routes use the same writer and
  checks. For a scoped token, authorize every requested key before loading or
  returning scope details; an unauthorized or unknown key makes the whole
  request fail without disclosing which other scopes exist. The owner may
  target any existing scope. Neither route creates, removes, or rekeys a scope,
  edits instance settings, or changes owner policy.
- The typed config already includes optional `display_name` on scopes, bots,
  graphs, actions, nodes, and LLM profiles. A `PUT` that edits a label retains
  every map key; references continue to use keys. `GET` leaves absent labels
  absent; presentation may fall back to the key without changing the document
  or its ETag.
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
already authorized scopes.

### 2. Visible names and label uniqueness

Creation and rename have different semantics. When an editor creates a bot,
graph, action, node, or model profile, its submitted initial name is used
verbatim as the YAML map key; no random key or `display_name` is generated. A
later rename keeps that key and writes the new visible label to `display_name`.
For example, creating a bot named `Sales assistant` inserts a map entry under
that exact key and leaves `display_name` absent; renaming it to
`Regional sales` keeps the key and sets `display_name: Regional sales`.
References such as `bot.graph` and graph `goto` values always contain keys.

A key collision within a map is rejected rather than silently changed or
suffixed: never auto-slug, overwrite, or add a numeric suffix.

Visible labels must be unique in their UI namespace: scopes across the
instance; each entity category within a scope; nodes within their graph; and
each catalog within its owner (`scope` or `shared`). Same labels in different
entity categories or different scopes are allowed. Compare the effective label
(`display_name` when non-empty, otherwise the key), so a missing label cannot
collide with an explicitly named entity unnoticed. Enforce this in config
validation and on every API create/rename; UI checks are only early feedback.
Comparison drops surrounding space, collapses inner runs, and ignores case, so
a label differing only in those ways is refused; the saved label keeps its own
text. A shared catalog joins the rule when its types arrive (ADR 0031), which
is why the check is keyed by namespace rather than by today's scope shape.

### 3. Account history: keep, delete, migrate

Unlinking is not deletion, and deletion is never implied by an account change.
This is behavior Botflota owes an operator regardless of any host.

- **Unlink keeps everything** (current). `DELETE .../bots/{bot}/account` and
  `bot unpair` remove the credentials, the account record, and the pairing
  status, and fail the replies the old link still owes (ADR 0040). Chats,
  sessions, messages, sender profiles, and contacts stay exactly as they are,
  so a temporary unlink or a later replacement never costs history.
- **Deleting history is its own explicit operation**, confirmed by the caller
  and addressed to one `(scope, bot)`: it drops that bot's chats, and with them
  the cascaded sessions, messages, and queued replies, plus its contacts. It
  never touches the scope's `senders` — those are shared by the scope's bots
  (ADR 0029) — and it never touches another bot. The API reports what was
  removed. Nothing else calls it: no pairing, replacement, or config edit
  deletes history as a side effect.
- **Migration transfers history, not identity.** A migration moves a bot's
  chats, messages, and contacts to another bot **of the same scope**, so a
  recreated bot or a peer bot starts from the same conversations and Botflota
  treats the result as an ordinary bot. Scope never changes: references and
  sessions never cross scopes (ADR 0016), so there is no cross-scope move.
  Senders are already shared by the scope and need no copy. A bot that only
  relinks its account needs no migration at all — its history is keyed by bot,
  not by account.

Migration is a **move**, not a copy: two bots holding the same history would
cost storage and prove nothing. The operation therefore runs with both bots
stopped, settles the source's pending outbox first — the old link must never
send what it no longer owns, so a non-empty queue is failed explicitly rather
than carried over — and ends the source's active sessions instead of pretending
the destination's graph sits at the same node. Chats, messages, and contacts
transfer in one recoverable operation that lands all of them or none, and that
a repeat does not duplicate. Both operations are owner-and-scoped like any
other write to their scope.

Their semantics are settled in **ADR 0042**: the routes and their confirmation,
what deletion reports, how a destination that already holds a chat absorbs one,
how contacts merge, why live session state does not cross, the single port that
owns the transaction, and why a retry needs no request key.

### 4. Watcher coverage

Publication reuses the existing activation pipeline, so the two watcher gaps
already open in `PLAN.md` are part of this work: a multi-scope candidate, and
the expiry that lifts an active-session deferral once those sessions end.

## Wire examples (proposed, not implemented)

`GET /api/v1/scopes/scope_a7f3/config` returns only that saved scope, its opaque
version/ETag, and its active version/status. A missing `display_name` stays
absent in `config`; the UI may show `scope_a7f3` as fallback. To save two
existing scopes, a scoped token authorized for **both** may send:

```http
POST /api/v1/config/publications
Authorization: Bearer <scoped-or-owner-token>
Content-Type: application/json

{"scopes":{
  "scope_a7f3":{"version":"v-a","config":{"display_name":"Sales"}},
  "scope_b921":{"version":"v-b","config":{"display_name":"Support"}}
}}
```

Assume each `config` above is the **entire** saved scope; omitted entities would
be deleted. One stale or unauthorized scope rejects the whole request; no
partial saved scopes. Return a result for each scope with saved version, active
version, and active/pending status. `PUT` to one scope is a convenience through
the same publication pipeline. Never synthesize fallback labels into the
submitted config or change a scope key as part of publication.

Publication outcomes: 200 for saved and active/no-op, 202 if any requested scope
is saved but pending, 428 for missing expected versions, 412 if any version is
stale, 409 for a quota or grant conflict, 422 for invalid config, 404 for an
unknown/out-of-scope target, and 401 for invalid credentials. The owner gets
detailed diagnostics; a scoped caller must not learn paths, other scopes,
owner policy, or whether the failure came from an unauthorized scope. This
status sketch needs finalization with the canonical API contract.

## Acceptance gates

Publication:

- Owner, authorized multi-scope token, and out-of-scope token: reads and
  writes obey authority, with 404 and no existence or policy disclosure.
- Scope split across the main file and nested includes: edit, add, and remove
  definitions, including scopes defined directly in the main file; preserve
  unrelated scopes, global settings, and includes.
- Two concurrent scopes in one file both survive; two stale writes to the same
  scope conflict. An identical write leaves files and runtime unchanged.
- One batch with two authorized scopes saves both or neither; a stale, unknown,
  or unauthorized member leaves all files unchanged and reveals nothing about a
  scope outside the caller's authority. Activation may be pending for one scope
  while another becomes active.
- Missing/empty and explicit `display_name`: round-trip without changing keys,
  references, guards, sessions, linked accounts, or token access. Editing a
  label in one scope cannot alter another; missing fallback is presentation
  only. A manual key edit remains a different identity.
- Invalid candidate or masked secret: no partial file write and no secret
  leakage. An injected failure after the first file replacement recovers
  entirely old or entirely new content, including after restart.
- Incompatible graph edit: YAML saved, runtime pending, unrelated scopes still
  active. Manual edits and newly matched includes remain watchable.
- Duplicate effective labels are rejected by the server within each stated
  namespace; equal labels across categories or scopes remain valid. Test
  create, rename, fallback-to-key, and collisions, including direct API calls.

Account history:

- Unlink, replace, pair, and unpair leave chats, sessions, messages, and
  contacts untouched, and the next pairing resumes them.
- Explicit history deletion removes exactly one bot's history and contacts,
  never the scope's senders or another bot, and reports what it removed. A
  confirmation that does not repeat the target is refused.
- Migration moves a bot's chats, messages, and contacts to another bot of the
  same scope, never across scopes, with both bots stopped, the source's queued
  replies failed before anything transfers, and its active sessions expired; it
  lands all of them or none after an error or restart, and a repeat does not
  duplicate them. A destination that already holds a chat for a sender absorbs
  the moved messages and sessions, keeps its own guards and identity, and skips
  a provider id it already stores; contacts merge by phone, keeping the row
  with the later update and any text only the other row has (ADR 0042).

When implementation begins, request build permission before checks. Use the Make
targets, add concurrency tests, and mark deliverables complete only in the
commit that proves them.
