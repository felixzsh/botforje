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
