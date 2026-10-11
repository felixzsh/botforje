# ADR 0018 — Template resolution semantics

Status: Accepted (2026-09)

## Context

Messages and requests reference runtime conversation values as
`{{path.to.value}}` (`docs/architecture.md`). The roots are fixed, but
the edges were not: what happens when a path is missing, how a literal `{{`
is written, and which roots may be written.

Rendering an empty string for a missing path hides a typo or an unset
variable and can send a half-formed message; keeping the literal `{{...}}`
leaks an internal path to the user. Both make failures invisible where the
conversation matters most.

## Decision

A reference is `{{`, a dot-separated path, and `}}`; whitespace just inside
the braces is ignored. The first segment selects a root:

- `graph`, `node` — read-write variables (ADR 0033); `node` resets on every
  node entry.
- `peer`, `message`, `bot`, `input` — read-only for the duration of a
  message; the engine seeds them. `peer` denotes the normalized live sender,
  not the chat target or an alternate outbound recipient (ADR 0029).
- Request results live under `node.<request>`, addressed as
  `resp.body.*`, `resp.headers.*`, `status`, and `duration` (milliseconds).

Rules:

- A missing path is an error that names the path; rendering never silently
  drops text. A declared optional value may have its defined empty value,
  but an undeclared path is not made optional by an adapter's inability to
  supply it. There is no universal `contact.phone` or `peer.phone` path,
  including an empty fallback (ADR 0051).
- A whole map or slice has no text form and is an error; slice elements are
  addressed with a numeric segment.
- A literal `{{` is written `\{{`; `\\` renders one backslash.
- An unknown root fails when the reference is parsed; a missing path and a
  non-scalar value fail when it is rendered.

## Approved P1 compatibility

Neutral peer paths are `peer.id`, `peer.provider`, `peer.subject`, and `peer.name`.
The internal identity ID renders as an opaque string; external subject values
remain opaque and are not automatically destinations.

For a bot consuming the approved WhatsApp compatibility, `contact.id` and
`contact.name` translate to `peer.id` and `peer.name`; `contact.phone` translates
to `peer.subject` only under its PN normalization policy (ADR 0020). Other
adapters do not inherit these aliases. Apply adapter-declared reference mapping
to a per-bot effective view without changing the shared graph or performing
unbounded textual replacement. Validate references after that translation;
unsupported aliases are errors, never silently empty values.

P1 implements the neutral `peer` root and retires `contact`; the per-bot
WhatsApp translation of the legacy `contact.*` references needs the consuming
bot's definition and is P2 work. Remaining config, selector, output, and archive
compatibility belongs to its own phase and owner.

## Consequences

- Failures surface at the step that introduced them, not in the user's chat.
- A step renders only against a context that declares its variables;
  `initial_context` and tool `inputs` are the source of those declarations.
- Config-time validation checks every reference against the declared graph and
  node variables, tool inputs, and request results before the fleet starts
  (`config/template_validation.go`).
- Escaping is one small documented rule; no alternate template syntax
  appears.
