# ADR 0056 — Tool inputs are closed sets

Status: Proposed (2026-10)

## Context

ADR 0033 requires that a value cannot be invented, only mismapped: the LLM
extracts a literal value and its span, System One maps it to a real catalog
entry, and code validates. An `input` today declares only `type`, `required`,
`default`, `description`, and `bind`, so there is nothing to map against. The
shape is a config field, so it has to be fixed before the schema is generated.

## Decision

An input is free or closed. A free input is `string`, `integer`, `number`, or
`boolean`, and code parses it. A closed input declares the values it accepts and
System One picks one of them.

Exactly one of `choices` and `catalog`:

```yaml
inputs:
  product:
    required: true
    description: "What to look up"
    bind: node.product
    label_bind: node.product_label
    choices:
      - id: "sku-1"
        label: "Manzana roja"
        aliases: ["manzana", "manzanas"]
      - id: "sku-2"
        label: "Pera verde"
```

- `id` is what the tool receives. `label` is what the model reads and defaults
  to `id`; `aliases` are further phrasings a person may use. `catalog` is a
  template resolving to a list of the same objects, for values that come from a
  `request`.
- A closed input binds its `id` to `bind`, which is what the tool's steps read
  as `{{input.<name>}}`. The `label` is what a person should see, so an optional
  `label_bind` writes the chosen label to that node or graph variable and a
  confirmation quotes it: an order URL needs `sku-1`, and the customer needs
  "Manzana roja". A `label_bind` on a free input is a validation error, and both
  targets are validated like any other bind (ADR 0033).
- A closed input's `type` is `string` or absent, because the id is what binds.
  Declaring both `choices` and `catalog`, or a closed input with a non-string
  `type`, is a validation error.
- `score` is not a closed set an input can use: it rates ordered levels, and a
  tool needs an identifier. A `score` question appears only inside the
  pipeline's own rubric questions, never as an input.
- Mapping is one `choice` question per value, gated by the `confidence_threshold`
  of the `system_one` profile. Below it, or when no option fits, the result is a
  tool error that asks the user (ADR 0033) — never a guess and never the first
  option.
- Staged mapping: a `choice` question carries up to 255 options on the
  standard, so the wire is rarely the limit — accuracy is. `mapping_stage_size`
  defaults to 255, the wire limit, so a catalog stages only when it is larger
  than one question can hold: when the resolved closed set exceeds it, mapping
  runs in stages and still resolves exactly one `id`, inside the same tool
  budget. The number of decision calls stays bounded by the stage size; the
  algorithm is free, and a lower default needs a golden conversation that
  proves accuracy falls before it, not a hunch.
- Numbers and dates are never closed sets. Code parses them from the literal
  span and checks invariants, and no model computes one.

## Consequences

- An authored catalog is data, not code: adding an item is a config edit, and
  the schema, the fixtures, and the reference config carry it once.
- A runtime catalog larger than the stage size costs more than one call, so the
  node's budget has to match the catalog it maps.
- Aliases carry the language risk: item names must be tested in the languages
  actually used, and a missing alias shows up as an "ask the user" loop rather
  than a wrong entry.
- Two variables per closed input at most: the id the tool uses and the label a
  person reads. A node that needs more display data reads it from the same
  `request` its catalog came from.
