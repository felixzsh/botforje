# ADR 0052 — Channel capabilities are declared and enforced

Status: Accepted (2026-10)

## Context

The runtime assumes WhatsApp everywhere: `api/api.go` requires linked-account,
pairing, and address-book dependencies even for a channel that cannot support
them; `cmd/botflota/fleet_cmd.go` opens WhatsApp devices and builds only that
channel; and the outbox retries every send error until its attempt budget is
exhausted, including content a channel can never deliver (ADR 0036, ADR 0040).
Presence, history import, typing, and external-reply observation are optional
WhatsApp capabilities another channel may not implement.

A second channel must not become a set of conditional statements that lie
about support: config validation, startup, and the API all need the same
declared facts.

## Decision

**One injected catalog.** The composition root assembles an immutable catalog
of installed channel definitions. Each definition declares its provider,
adapter kind, account modes, implemented capabilities, a pure config and
content validator, and a builder. There are no runtime plugins, reflective
registration, or `init`. `config`, CLI validation, startup, watcher loads, and
API publication consume the same catalog; `config` never imports adapters or
`engine`, and the core keeps standard-library-only contracts.

**Capabilities describe the implementation, not the platform.** A definition
declares the content kinds it can send and receive, its feature variants
(response buttons, links, menus, selection input, and the like), and its
limits in provider-correct units (bytes for callback data, characters for some
text, bytes for media). Granularity is the installed definition and its account
mode, never the provider: two modes of one provider may declare different
support. Three answers stay distinct: supported by this
adapter, authorized for this account, and currently usable for this
conversation. Dynamic rights, block state, and reply windows cannot be
certified by offline YAML validation, and unknown permission is not granted.
`GET /api/v1/scopes/{scope}/bots/{bot}/channel` exposes `provider`, `adapter`,
`mode`, and `capabilities`: the installed definition's `send`, `receive`,
`features`, and `card_variants`, without credentials or backend locators. A
provider-correct limit is exposed only when the definition actually declares
it; discovery never fabricates a generic limit map from validation code.

**P4 public discovery (owner decision 2026-10-12).** Channel declarations,
binding readiness, and conversation eligibility are separate facts and views.
ADR 0053 owns `GET .../binding` and its `{binding, condition, reasons, method}`
shape. Authorization and transport observations must be reported separately,
with explicit unknown values/reasons when no observation exists; `configured`
is not a grant. A disconnected bot keeps its declared capabilities without
invented current permission. Discovery is authorized under scope and bot before
any read, and never enumerates another scope's bindings or managed endpoints.

An operation or submitted content the selected implementation does not support
answers 422 `unsupported_operation`, before invocation or queue admission;
missing optional ports never become an internal error. Reading a retained
history or directory snapshot is distinct from invoking current history or
directory import, so stored reads need no invented live capability. Public
names follow the selected domain vocabulary (ADR 0054). These contracts are
approved for P4; their runtime gates remain phase-tracked.

**Validation at three points, one vocabulary.**

1. Load and publication: channel kind and typed options; each bot's referenced
   graph, entry and exit actions, tools, and guard `on_blocked` steps checked
   against the declared support. A shared action is checked in the context of
   each consuming bot, not globally against one provider.
2. Execution and API submission: rendered content and target are validated
   before queueing, and any explicitly configured fallback is chosen before
   actions or HTTP side effects that depend on it.
3. Delivery: dynamic eligibility is rechecked; unsupported or permanently
   invalid input is rejected immediately.

**No silent downgrade.** The default is rejection. A textual fallback is
author-declared and keeps the same intended choice values. A known unsupported
configuration fails validation, and queued messages that can never be delivered
become failed without exhausting their retries.

**Acceptance is not delivery.** `Send` success means the documented downstream
transport or queue accepted the message, never that the recipient received or
read it. Delivery stays at-least-once, and a timeout has an uncertain outcome
that is never presented as a proven rejection (ADR 0040). That uncertain
outcome retries under the same rule, and the state a spent attempt budget
leaves says acceptance was not confirmed rather than that the recipient did not
receive the message. When a provider accepts an idempotency key or a
pre-assigned id, the adapter reuses the same one for the attempts of that
durable delivery.

**Failure classes are explicit.** Permanent failures — an unsupported kind, an
invalid target, an identity the binding does not cover, a stale binding
generation — fail the entry immediately. Transient failures retry with bounded
backoff, honoring provider retry hints when supplied. Losing the authorization
of the same binding stops the bot with its queue pending; replacing the binding
fails the old entries (ADR 0040). A stopped instance is not reused unless its
contract says so, and a partially started instance is cleaned up.

Transport health and authorization remain separate (ADR 0053). Losing a shared
backend connection can affect several logical views without revoking any of
their bindings. Physical event readers and resource coordination belong to
the adapter; disconnecting one bot releases its view, not its peers' views.
No common profile-switch, service pool, or subprocess-management port follows.

Bounded buffering and logical isolation do not imply independent progress
under overload: a full consumer queue can stall a shared source. Waits on a
shared source are cancelable, and releasing one logical view frees its own waits
without closing a peer's view. Preserve the no-silent-drop contract and state
the actual replay/backpressure boundary. Botflota promises it does not silently
drop traffic; it does not promise that a source without replay keeps every event
across an outage. Deployment-specific grouping, fairness, capacity, and any
justified durable buffering remain TBD under ADR 0046/0054, not advertised
guarantees or new mandatory capabilities. Preparation tests exercise shared-view
teardown and bounded backpressure without claiming a production capacity.

**Optional operations stay optional ports.** Media subtypes, location, contact
cards, polls, response buttons, formatting, groups, topics, typing, marking
inbound read, delivery receipts, history import, address-book import, editing,
deletion, and external-reply observation are advertised capabilities, not
baseline obligations. `MessageChannel` keeps its four operations, and there is
no `SendX` method per content type. Typed content stays; raw provider JSON
never enters the domain.

**The interaction extension waits for its first consumer.** The recipe is
frozen but not built: a neutral outbound choice payload with labels and stable
values, a distinct inbound selection payload, JSON codecs, advertised variants
and limits, and deterministic graph handling. A selection is never routed as
user-written text, fuzzy matching, or an LLM input, and adapter-level
acknowledgement is distinct from graph acceptance. Poll votes and edits are
not new turns (ADR 0036).

## Consequences

- One fake text-only channel can join the fleet without a WhatsApp device
  store, a pair manager, or an address-book store.
- Unsupported settings, content, or graph features fail at the earliest
  boundary that can see them instead of at delivery or never.
- The outbox distinguishes a permanent rejection from a transient one, so a
  dead message does not block FIFO with useless retries (ADR 0011).
- A second provider adds a definition and a builder at the composition root,
  not a switch in `cmd/`.
