# ADR 0012 — Toolchain baseline

Status: Accepted (2026-09)

## Context

Phase 0 fixes the toolchain before product code exists: Go version, module
path, CLI approach, and test layout. These choices touch every later phase
and are cheap to settle now.

## Decision

- Go 1.27 line, minimum `go 1.27.1`. whatsmeow declares `go 1.26.0` and pins
  `toolchain go1.27.0`, so 1.27 is the newest line compatible with it.
- Module path `github.com/felixzsh/botforje`.
- CLI on the standard library: `flag` plus explicit subcommand dispatch. No
  CLI dependency until a real limitation appears.
- Tests: table-driven with `t.Run` subtests, fixtures in `testdata/`,
  black-box `_test` packages where practical, and hand-written fakes instead
  of a mocking library.
- Dependencies are added only when they earn their place: whatsmeow
  (ADR 0001) and SQLite (ADR 0006) so far.

## Consequences

- One dependency tree; no CLI framework to learn.
- Subcommand parsing is manual; revisit only if it grows noisy.
- A clean checkout builds with Go 1.27.1 or newer.
- Tests and their fixtures live together, so they move with the package.
