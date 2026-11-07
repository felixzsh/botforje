# ADR 0055 — A tool node stays, declares its exits, and confirms before writing

Status: Proposed (2026-10)

## Context

ADR 0033 fixes the tool principles and leaves the sequence, the intake gate,
and the staged mapping to Phase 8. Three of the things it names have no shape
yet, so they cannot be validated, generated into the schema, or tested: how a
tool node ends, how a side effect waits for confirmation, and how a message is
handled in place instead of taking an edge. Left to code, those become
permanent without review.

## Decision

**Handling in place.** A node with `tools` handles an inbound message where it
is: no edge is taken and `on_enter` does not run again, so one node serves many
messages and the node context carries what it learned. Only an entry is an
entry.

**Exits are declared.** A node with `tools` declares no default edge
(`intent: ""`); every exit names an intent. `done` and `cancel` are reserved —
`done` means the node's work is complete, `cancel` means the user abandoned it
— and any other intent is an ordinary decision. A trigger match takes an exit
deterministically; otherwise the pipeline may take a declared exit when System
One answers the exit question at or above the threshold. No model invents a
target.

**Two modes, one field.** `node.tool_mode` is `pipeline` (default) or `loop`.
Both share the `inputs` validation of ADR 0056 and the edge rule above; the
loop is for calls a pipeline cannot bound (ADR 0033).

**Confirmation is the side-effect marker.** An action declares `confirm`, and
declaring it is what marks the side effect: there is no second flag to disagree
with. The pipeline never applies such an action directly. It stores a proposal
— the tool and its resolved arguments — in the node context, so a restart keeps
it, and queues the action's prescribed confirmation `body`, which reads the
arguments as `{{input.<name>}}`.

```yaml
actions:
  start-order:
    inputs:
      product:
        required: true
        bind: node.product
        label_bind: node.product_label
        choices: [{id: "sku-1", label: "Manzana roja"}]
      quantity:
        type: "integer"
        required: true
    confirm:
      body: "¿Confirmas {{input.quantity}} de {{node.product_label}}?"
      expires: 300              # seconds the proposal stays valid; default 300
    steps:
      - request: {name: create, url: "https://api.example.com/orders", method: POST}
      - message: {body: "Listo, tu pedido quedó registrado."}
```

`expires` is seconds, like `cooldown.duration`, and unlike the millisecond
`bounds.timeout`, so the two never read as the same unit.

**Resolving a proposal.** While one is pending, the node's triggers and exits
are evaluated first, so a user who says "cancel" leaves deterministically. If
the message stays, System One answers one `choice` over `confirm`, `decline`,
and `other`, with the proposal's arguments as the option descriptions. At or
above the threshold `confirm` applies the tool, `decline` discards it, and
`other` discards it and handles the message through the normal pipeline; below
the threshold the proposal is discarded. The proposal is consumed in the same
turn transaction that applies the tool (ADR 0040), so a redelivery or a
repeated confirmation never writes twice. It expires after `confirm.expires`
seconds, and an expired proposal is discarded without a message. Expiry is
checked when the next message arrives, so a pending proposal needs no timer and
no background work.

**Budget.** A turn applies no more than `bounds.max_tools_per_message` tools and
takes no longer than `bounds.timeout` (ADR 0031). A side-effecting tool is never
applied once its budget is spent.

**What a tool node requires.** A node with `tools` needs a resolved
`system_one`: the intake gate, selection, mapping, and confirmation are all
decision questions, so a bot without one fails validation instead of running
tools on a guess. Without a resolved `llm` the pipeline never extracts — it
uses bound and defaulted inputs only — so a required input with neither `bind`
nor `default` is a validation error for that bot.

**Results need no new syntax.** An action's own steps already read what they
call — `{{node.<request>.resp.body.*}}`, `{{node.<request>.status}}` — and the
confirmation reads `{{input.*}}`. A message sent after the write is a later step
of the same action. This closes the open "tool result template syntax" question
with nothing new.

## Consequences

- A tool node is a small conversation of its own: it answers, asks, confirms,
  and leaves only through an exit it declared.
- The proposal lives in the node context, so the persisted state of a tool node
  is part of the graph run and needs no new table.
- Confirmation costs one decision call per reply while a proposal is pending,
  and an unrelated message in that window pays for it too.
- A tool node cannot exist on a bot with no System One profile, which makes the
  recommended shape (ADR 0032) the required one for tools.
- The intake gate decides whether extraction runs, so an unclear message costs
  one decision instead of a decision plus an extraction.
