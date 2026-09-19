# ADR 0033 — The tool pipeline: the LLM extracts, System One decides

Status: Accepted (2026-09)

## Context

ADR 0008 gives nodes parameterized tools and an LLM loop. With prescribed
replies and System One routing (ADR 0030, ADR 0032), a tool node needs a mode
where the model that reads free text is not the one that decides tool names,
maps catalog values, or reports confidence. Reading and deciding are different
tasks: an LLM reads "2 cajas de manzanas y 3 de peras" well, and it must not
invent an item identifier or compute a quantity.

Verification is a documented System One use case, not a new kind of model
call: a judgment that answers a typed question about a state.

## Decision

Two tool modes coexist. The pipeline below is the default; the LLM loop of
ADR 0008 is the opt-in alternative for nodes whose calls a pipeline cannot
bound. Both share the `inputs` validation and the rule that edges are the
only transitions.

For one inbound message, the pipeline runs:

1. Intake gate (System One): is the message actionable and clear for this
   node? The answers choose among act, ask and stay, delegate to extraction,
   or escalate.
2. Extraction (LLM): candidate parameters as literal values and spans, never
   final identifiers, validated against the tool `inputs`.
3. Selection (System One): one `Noul` per tool for real multi-select plus an
   explicit `none`, or a `Choice` when only one can apply. A confidence below
   the threshold asks instead of acting.
4. Mapping (System One): each value maps to a real catalog entry with a
   `Choice` over the catalog, in stages when it exceeds the cardinality
   limit. Code does no matching of values.
5. Verification (System One): whether the extracted lines reflect the
   message. This is a signal, not a proof.
6. Application (code): numbers and dates are parsed from the literal span,
   invariants are checked (the item exists, the quantity is positive), the
   tool is applied, and a prescribed reply confirms.

Staying is a normal outcome. A handled message takes no edge and does not
re-enter the node, so a tool node serves many messages with entry actions
running once. A tool node declares its exits (`done`, `cancel`, human) instead
of a catch-all default that would eject the user. Side effects wait for the
confirmation: the pipeline proposes, a prescribed message confirms, and only
then the tool writes. Models never compute numbers or dates.

## Consequences

- Tool selection and mapping are bounded and auditable: a value cannot be
  invented, only mismapped, and confidence gates ask versus act.
- Extraction is a normal LLM use, not reply generation, so it does not weaken
  ADR 0032.
- A tool node needs the LLM only to read text; the decision path runs without
  it.
- `inputs` needs a closed-set declaration for mapping, and large catalogs
  need a hierarchy; item names must be tested in the languages actually used.
- Extraction can drop items or misread numbers, so verification and the
  confirmation before side effects are mandatory.
- Amends ADR 0008 (tool invocation is not LLM-only) and narrows the last
  bullet of ADR 0032.
