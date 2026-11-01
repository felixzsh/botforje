# Phase 7 small closures and capacity evidence

Align the canonical documents with the work already landed (channel-neutral
preparation, load testing, SQLite policy) and close the four small Phase 7
items, keeping every behaviour deterministic under the existing gates.

## Specs

- S1 — `PLAN.md`, Phase 7: "The `chat sync --all` summary: report answered,
  timed-out, and skipped counts, and skip a chat with no anchor instead of
  failing the batch."
- S2 — `PLAN.md`, Phase 7: "Optional: `completion` for bash, zsh, and fish."
- S3 — `PLAN.md`, Phase 7: "Reject a `${...}` that reaches any other string
  (ADR 0014): entities (llm, action, graph, bot) are scanned, but
  instance-level settings (`address`, `data_dir`), `include` paths, and a
  scope's `display_name` are not."
- S4 — `PLAN.md`, Phase 7: "Account states beyond binding: a connection
  rejected with 428 (passkey required) or 463 (reachout timelock) stops the bot
  and is visible in `bot list` (ADR 0028)."
- S5 — Owner: "haz los ajustes en los adr/docs que corresponda respecto a lo ya
  hecho" and "si agregas el item que cerraria el hilo de load test": the
  capacity envelope measured on 2026-10-30 must be reachable from `PLAN.md`,
  `ROADMAP.md`, and `docs/access-policy.md`, and the documents that still
  describe the load harness as unimplemented are corrected.
- S6 — Owner: one focus per slice; no unrelated refactoring, and every changed
  command or message updates its help text and the document that owns it.

## Tasks

- T1 — docs: fix the stale load-testing statements, cite the measured envelope
  in `PLAN.md`, `ROADMAP.md`, and `docs/access-policy.md`, and extend ADR 0048
  and ADR 0049 to the model endpoints Phase 8 introduces. S5, S6.
- T2 — `chat sync --all` counts answered, timed out, skipped, and failed, and a
  chat without an anchor is skipped rather than failed. S1.
- T3 — `botflota completion <shell>` renders bash, zsh, and fish from the CLI
  tree. S2.
- T4 — a `${...}` anywhere outside a secret field is rejected, including
  instance settings, `include` paths, and a scope's `display_name`. S3.
- T5 — WhatsApp connect failures 428 and 463 stop the bot and surface as their
  own state in `bot list`. S4.

## Log

- L1 — Owner: "si, ahora ayudame a cerrar esos 4 items chicos de fase 7 pero
  antes haz los ajstes en los adr/docs que corresponda respecto a lo ya hecho y
  si agregaa el item que cerraria el hilo de load test".
- L2 — Preceding context: the load-testing branch was merged into `main` by
  fast-forward after `make check` passed, and `origin/main` is 405 commits
  behind because nothing was ever pushed.
