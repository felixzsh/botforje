# ADR 0041 — The config file carries no version field

Status: Accepted (2026-09)

## Context

The root document required `version: 1`, checked against a constant in the
`config` package. Only one format was ever accepted, so the field asked the
host to declare metadata the binary already owns: the runtime knows which
format it implements and validates against it. Comparable single-binary tools
(Prometheus, Caddy) never carried such a field, and Compose deprecated its
top-level `version` in favor of the implementation's current schema.

## Decision

The config file has no `version` field. The binary's SemVer version
(`botflota --version`) identifies the config format it implements. Strict
decoding plus semantic validation in Go (ADR 0007) reject anything else, and
the generated `config.schema.json` no longer requires the field; it stays an
editor aid generated from the Go types.

## Consequences

- A host never edits a file just to keep it loadable; a format change rides
  the release that introduces it and is named in that release's notes.
- An incompatible file is detected through decode and validation errors
  instead of a declared mismatch, so a change that keeps a field name but
  alters its meaning must be called out explicitly when released.
- Supporting two formats at once would require reintroducing a declared
  version; until then, one binary accepts one format.
