# ADR 0007 — Config schema generated from Go types, validation in Go

Status: Accepted (2026-09)

## Context

The configuration DSL is expected to keep changing as the project evolves
(intents, policies, LLM modes). The earlier plan kept a hand-written
`config.schema.json` as the structural source of truth with hand-written Go
structs beside it, and relied on fixtures to keep them coherent. That is a
drift risk: two artifacts must change together, and a JSON Schema cannot
express cross-field semantics anyway.

## Decision

Go structs are the single source of truth for the config shape. In Phase 1
the project generates `config.schema.json` from those structs for editor
support (autocompletion and inline hints). Runtime validation does not use a
JSON Schema library:

1. Strict YAML decoding into the Go structs rejects unknown fields and wrong
   types with precise locations.
2. Semantic validation in Go covers what a schema cannot express: reference
   resolution, scope isolation, uniqueness, reachability, and mode rules
   (for example, `policy` requires `intent` or `body`).

The generated schema file is never edited by hand once generation exists.

## Consequences

- One artifact to maintain; the DSL and its editor schema cannot drift.
- No JSON Schema validator at runtime; fewer dependencies and better error
  messages from the decoder and semantic checks.
- A generation step and a small generator dependency are needed (candidate:
  `invopop/jsonschema`; approval is required before adding it).
- Editor hints depend on regenerating the schema after struct changes; a
  build or make target must do it.
