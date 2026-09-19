# ADR 0033 — Tools: the LLM reads, System One decides, code applies

Status: Accepted (2026-09)

## Context

Inside a state, the LLM may need to look something up, ask a follow-up, or call
a business API with concrete arguments. Fixed entry actions and text-only
messages cannot express that. Since System One is the decision path (ADR 0030)
and replies are prescribed (ADR 0032), reading free text and deciding tool
names are different tasks: an LLM reads "2 cajas de manzanas y 3 de peras"
well, and it must not invent an item identifier or compute a quantity.

## Decision

Tools are actions with declared, validated `inputs`; an input may bind to a
node or graph variable to read and write it. Edges remain the only
transitions, and no model invents one.

Two tool modes coexist, sharing the `inputs` validation and the edge rule:

- Pipeline (default): the LLM extracts candidate parameters as literal values
  and spans; System One selects tools and maps values to catalog entries with
  calibrated confidence; code validates and applies, then a prescribed message
  confirms. The principles are fixed; the exact sequence, the intake gate, and
  the staged mapping are implementation choices tuned in Phase 8.
- LLM loop (opt-in): for nodes whose calls a pipeline cannot bound.

Required properties of any tool run:

- Models never compute numbers or dates; code parses them from the literal
  span and checks invariants.
- A missing required input is a tool error that asks the user, storing the
  answer in the bound variable.
- Side effects wait for confirmation: the run proposes, a prescribed message
  confirms, and only then does the tool write.
- A handled message takes no edge and does not re-run entry actions, so a tool
  node serves many messages with entry actions running once. A tool node
  declares its exits (`done`, `cancel`, human) instead of a catch-all default.

## Consequences

- Tool selection and mapping are bounded and auditable: a value cannot be
  invented, only mismapped, and confidence gates ask versus act.
- Extraction is a normal LLM use, not reply generation, so it does not weaken
  ADR 0032.
- A tool node needs the LLM only to read text; the decision path can run
  without it.
- `inputs` needs a closed-set declaration for mapping, and large catalogs may
  need a hierarchy; item names must be tested in the languages actually used.
- Extraction can drop items or misread numbers, so verification and the
  confirmation before side effects are mandatory.
