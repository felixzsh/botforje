# Phase 7 remaining units: scope isolation and the pairing code

`odd/tasks/phase7-small-closures.md` closed the four small items and merged to
`main` as `0d7723a`. Of Phase 7's 66 deliverables, 56 are checked; the four TBDs
need an owner decision rather than code, the operator-UI cookie is deferred, and
the channel preparation's P6 waits on the operator's WhatsApp checklist. What is
left to implement is the isolation trio whose ADRs are already accepted
(0047–0049) and one setup method decided by ADR 0028.

Three of these ADRs gate ROADMAP Phase 14: a host onboarding authors it does not
fully trust needs the file root, the egress policy, and the secret catalog.
Nothing here is required by Phase 8.

## Specs

- S1 — ADR 0047: an owner-managed `file_roots` map bounds the local files one
  scope's steps may read. A scope without a root keeps today's behavior; scoped
  publication cannot set it; load rejects a root that is not an existing
  directory and rejects overlapping roots.
- S2 — ADR 0048: an owner-managed `egress` instance setting whose `public_only`
  mode refuses a non-public destination on the connection the call actually
  makes and re-checks or refuses each redirect, covering `request` steps and
  media fetched by `url`.
- S3 — ADR 0049: an owner-managed `secret_files` map. A scope with one resolves
  `${NAME}` only from that file, which must sit outside its file root and needs
  no fallback to the process environment.
- S4 — ADR 0028: the eight-digit pairing code is a first-class linking method
  beside the QR, and a pending step is a reported state, never a wait until the
  code expires.
- S5 — AGENTS.md: each unit checks off its own `PLAN.md` deliverable in the
  commit that completes it, updates the document that owns the behavior, and
  adds no dependency.

## Tasks

- T1 — S1: `file_roots` in the instance settings, restart-required and absent
  from `config init`, validated at load, and confinement enforced by the
  component that opens the bytes after templates resolve.
- T2 — S2: `egress.public_only` enforced on the connection that is actually
  made, for `request` steps and for media fetched by `url`.
- T3 — S3: `secret_files` per scope, resolved with no environment fallback, and
  refused when the file sits inside the scope's file root.
- T4 — S4: the pairing code in the core pairing contract, the WhatsApp adapter,
  the setup session, `bot bind`, and the API.

## Log

- L1 — Owner: "si fusiona y continua, trata de continuar orquestando
  subagentes", after the review of Phase 7's state found the trio and the
  pairing code as the only implementation left.
- L2 — Ordering: T1 and T4 are independent and run in parallel worktrees. T2
  and T3 follow T1 because all three share the instance settings in `config`
  and its validation surface, and T3 reads T1's root to decide where a secret
  file may not sit.
- L3 — T1 (`a341a36`, `24cad7a`), T4 (`ddeaf90`) and T2 (`9f99656`, `7b1d6c4`)
  merged into `main` with `--no-ff` as `9fa3649`, `57ce5b5` and `9274eeb`, the
  branches kept intact so every review still binds to an immutable SHA.
  `9274eeb` needed four resolutions: both deliverables checked in `PLAN.md`,
  both instance fields with their own doc comment in `config/config.go`, and
  `access-policy.md`/`integration.md` rewritten to say 0047 and 0048 are
  implemented while 0049 alone remains pending. `config.schema.json` merged
  itself and was proven: regenerated from the merged tree it is byte-identical,
  carrying `egress` and `file_roots`. `make check` green on `9274eeb` (17s).
- L4 — T3 (`627dcb5`, `f4686dc`) merged as `ca3ca53`, no conflicts, and
  `make check` green (21s) with all four units in the tree. The worker's own
  run had failed on `adapters/testchannel` `TestSetupConcurrentMutations…`
  with `sqlite: database is locked (517)`; it does not reproduce here, not even
  at `-count=8`, and T3 touches only `config`, so the load-dependent lock stays
  a repository flake to raise on its own, not a defect of this slice.
- L5 — the T4 reviewer rejected `ddeaf90`: the passkey-pending instruction said
  "finish the step on the phone that is linking" for both methods, which is
  false on the default QR path and contradicts the same commit's guide text.
  Seven further findings (companion name per method, an unreachable
  `SendPasskeyConfirmation` branch, a pending step that permanently displaces
  the code, a terminal reprinting the approval twice a second, ADR 0053 still
  listing two display formats, `binding/methods` hiding that `code` needs a
  phone, and unpinned wiring with a goroutine outliving its test) were all
  cheap against a consumed CLI/API contract: one correction round for all
  eight.
- L6 — the T2 reviewer rejected `7b1d6c4`: the destination check is a class
  deny-list, so RFC 6598 `100.64.0.0/10` is dialled unguarded — the reviewer
  confirmed on an ordinary host that `ip route get 100.100.100.100` resolves to
  `tailscale0` — and webhook delivery never reaches the guard while ADR 0048
  and `integration.md` claim every configurable outbound fetch is bounded. Six
  more (untested guard wiring that no mutation would reveal, a media refusal
  retried as transient, redundant conditions, a panic in an importable package,
  and an unnecessary `config → egress` edge) are corrected; the happy-eyeballs
  regression was discarded as a moderate design change for a low-impact stall.
- L7 — the T3 reviewer rejected `627dcb5`: the plan detects a catalog change
  but the live-apply path ignores it, so rotating the file (or removing a
  declaration) became live on the next unrelated reload and left the running
  `catalogs` map disagreeing with the values its scopes already held — the
  restart requirement of ADR 0049 was stated in the docs and not enforced. Seven
  more (containment decided after the file was read, a silently swallowed
  `EvalSymlinks` failure, an unbounded read, an empty value and a duplicate
  name hiding a mistake, a BOM, five surviving mutations, and the duplicate
  rule living only in a code comment) are corrected in one round. The
  `adapters/testchannel` SQLite lock cannot come from a range that adds no
  goroutine, lock, or database call.
- L8 — the three corrections (`0859a51`, `e04e51c`, `3a6faa5`+`55ddd58`) merged
  as `19d7c42`, `aeb8b05` and `0ba3689`, and `make check` green on `aeb8b05`
  (17s). The T2 and T3 first-round reviews had rejected their units on one
  contract each, so both corrections were audited again.
- L9 — T2's second review approved `0859a51`: the destination rule is now a
  complete registry enumeration with NAT64 and 6to4 decoded, and each of the
  seven items has a test that fails when it is removed. Six residual findings
  are mechanical and confined, so they ship without a third review: a base
  transport carrying its own TLS dialer bypasses the guard for `https`, the
  `runFleet` composition root is still unpinned, a webhook refusal is retried
  as transient while media treats it as permanent, the vocabulary pin is
  sample-based, and two documents still describe egress without webhooks.
  The RFC 8215 `/48` NAT64 decode question stays recorded and unactioned: the
  reviewer could not demonstrate a reachable bypass.
- L10 — T3's second review confirmed seven of eight items fixed and rejected
  `e04e51c` on two: `catalogRestartScopes` returns every declared scope, so a
  rotation in one catalog holds an unrelated scope's live-safe edit back with a
  false reason, and the size bound rejects after an unbounded read instead of
  bounding it, while the ADR advertises otherwise. Both are cheap and confined,
  so they ship mechanically with the three documentation nits and no third
  review.
