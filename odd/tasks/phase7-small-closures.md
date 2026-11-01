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
- L3 — Slice boundary: `origin/main` is 405 commits behind `main` because nothing
  was ever pushed, so the accumulated-diff count of the branch says nothing. The
  reviewable slice is this branch's own commits, one per unit, each on top of the
  commit that completed the unit before it.
- L4 — T1 committed as `7303f39`, T2 as `6251e9f`, T4 as `ec9ce7d`, and T5 as
  `7d476fe`. T2 needed a public code of its own: a chat with no history anchor
  answered the same `sync_failed` as an upstream failure, so the walk could not
  tell a state from a failure. The core now carries `ErrNoAnchor`, the API
  answers `422 no_anchor`, and `chat sync --all` prints one line per outcome plus
  a final count of answered, skipped, timed out, and failed.
- L5 — T3 (completion for bash, zsh, and fish) is delegated to a bounded writer
  because it is the largest unit and touches only `cmd/botflota/completion.go`,
  `cmd/botflota/completion_test.go`, and the command tree. The parent reviews,
  checks, and commits it.
- L6 — T3 landed from the bounded writer and was reviewed before commit: the
  zsh script ended with an unguarded `_botflota "$@"`, which recurses forever
  when zsh autoloads the file as the function body. Replaced with the
  autoload/source guard (`$funcstack[1]`) and pinned by a test. `make check`
  passes, and `bash -n` accepts the generated bash script.
