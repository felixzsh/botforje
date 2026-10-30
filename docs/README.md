# Documentation

Map of the documentation.

## Project references

- `scope.md` — what Botflota includes.
- `glossary.md` — terms used across the docs.
- `api.md` — API authority, scope coverage, and current versus target behavior.
- `next-plan.md` — Phase 7 delivery plan for API publication and account
  history, with its retired vocabulary marked.
- `channels/plan.md` — provider-neutral architecture, rationale, pending
  decisions, and P1–P6 gates (ADR 0051–0054); progress is tracked in `../PLAN.md` Phase 7.
- `channels/domain-terminology.md` — compact current-to-target contract, field,
  helper, and command renames; architectural reasons and pending decisions belong to
  `channels/plan.md`.
- `integration.md` — runtime versus host responsibilities, application flow,
  and the shared-process trust boundary (ADR 0046).
- `access-policy.md` — TBD resource-policy and provisioning decisions; not an
  approved limit catalog or implementation requirement.
- `../PLAN.md` — phases to the MVP, progress checkboxes, and open decisions.
- `../ROADMAP.md` — phases after the MVP and the order they arrive in.

## Developer docs

For contributors and agents working on this repository:

- `architecture.md` — components, boundaries, message lifecycle.
- `channels/load-testing-plan.md` — planned multi-bot integration tests and adjustable
  synthetic capacity experiments; not implemented behavior or measured capacity.
- `adr/` — why each decision was made.

### Channel adapter findings

Research notes recorded on 2026-10-09, not implemented behavior or completed plan gates.
`channels/plan.md` and the canonical ADRs still own shared architecture
and accepted contracts.

Provider send capabilities (`provider-findings/`):

- [Outbound matrix][outbound] — send types, feature variants, restrictions, and parameters for
  each channel and account mode.
- [WhatsApp native][whatsmeow] — whatsmeow baseline and uncertain protobuf-only/interactive
  send features.
- [Telegram][telegram] — Bot API versus personal MTProto, and send fields.
- [Signal][signal] — signalmeow outbound types; setup research pending.
- [SimpleX Chat][simplex] — shared profiles, WebSocket limits, and capacity.
- [WhatsApp Cloud API][whatsappcloud] — official send fields, interactive/template variants,
  and eligibility restrictions.

Cross-provider capability research (`provider-differences/`, scaffolds to be filled):

- [History and recovery][history-recovery].
- [Delivery and receipts][delivery-receipts].
- [External messages][external-messages].
- [Identity, directory, and targets][identity-targets].
- [Binding lifecycle][binding-lifecycle].
- [Optional operations][optional-operations].

[outbound]: channels/provider-differences/outbound-messages.md
[whatsmeow]: channels/provider-findings/whatsmeow.md
[telegram]: channels/provider-findings/telegram.md
[signal]: channels/provider-findings/signal.md
[simplex]: channels/provider-findings/simplex.md
[whatsappcloud]: channels/provider-findings/whatsappcloud.md
[history-recovery]: channels/provider-differences/history-and-recovery.md
[delivery-receipts]: channels/provider-differences/delivery-and-receipts.md
[external-messages]: channels/provider-differences/external-messages.md
[identity-targets]: channels/provider-differences/identity-directory-targets.md
[binding-lifecycle]: channels/provider-differences/binding-lifecycle.md
[optional-operations]: channels/provider-differences/optional-operations.md

## User guide

For people running bots. It lives in `guide/`.

- `guide/whatsapp.md` — binding an account and running bots.
- `guide/systemd.md` — running Botflota as a systemd service.

## Where things live

- Identity and principles: `../README.md`.
- Scope: `scope.md`.
- Mechanics: `architecture.md`.
- Rationale: `adr/`.
- Direction: `../PLAN.md` and `../ROADMAP.md`.
- Config shapes: `../config.schema.json` and
  `../config/examples/reference.yml`.
- Working rules for agents: `../AGENTS.md`.

## Rules

- One owner per topic. User pages are self-contained: they explain behavior
  without citing internal design notes.
- Reference is generated from code, not written by hand: config schema,
  CLI help, and API reference.
- Do not invent behavior. Pages are written against frozen shapes and
  working code.
