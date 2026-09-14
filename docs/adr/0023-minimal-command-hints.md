# ADR 0023 — Command guidance in user-facing output

Status: Accepted (2026-09)

## Context

BotForje writes logs, warnings, errors, `status`, and help. An embedded full
invocation can mislead: the reader cannot know if it fits their scope or state,
and a bot name may exist in several scopes.

## Decision

Any output a user sees names at most the base command, never an invocation with
flags, options, or entity arguments. Only command help and usage document
syntax, and each command guides the user when called incompletely or ambiguously.

## Consequences

- Messages point to `pair`, not to a specific invocation.
- Commands own their guidance through help and usage.
- Messages stay valid under scope or entity ambiguity.
