# ADR 0012 — Go version and standard-library CLI

Status: Accepted (2026-09)

## Context

Phase 0 fixes two choices with real tradeoffs before product code exists: the
Go version to require and whether the CLI uses the standard library or a
package. Module path, test layout, and dependency policy are conventions, not
architectural decisions; they live in `go.mod`, `AGENTS.md`, and
`CONTRIBUTING.md`.

## Decision

- Go 1.27 line, minimum `go 1.27.1`. whatsmeow declares `go 1.26.0` and pins
  `toolchain go1.27.0`, so 1.27 is the newest line compatible with it.
- CLI on the standard library: `flag` plus explicit subcommand dispatch. No
  CLI dependency until a real limitation appears.

## Consequences

- One dependency tree; no CLI framework to learn.
- Subcommand parsing is manual; revisit only if it grows noisy.
- A clean checkout builds with Go 1.27.1 or newer.
