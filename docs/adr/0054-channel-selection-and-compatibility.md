# ADR 0054 — Channel selection, options, and compatibility

Status: Accepted (2026-10)

## Context

`spec.Bot` has no channel selector; every startup opens WhatsApp devices,
checks WhatsApp pairing, and translates `BotSettings` for one adapter. Common
pacing and filtering settings share a struct with WhatsApp-only presence and
history settings, so another channel would inherit defaults it never declared.
`config/reload.go` compares the existing channel settings, not a selector or
an account mode.

## Decision

**A typed selector.** `spec.Bot` gains a channel selector:

```yaml
channel:
  provider: whatsapp   # messaging service
  mode: native         # account mode within that provider
  options: {}          # adapter-specific, validated by the catalog
```

`provider` and `mode` are core-typed and **required**: every bot names one,
and there is no implicit provider or missing-selector default. `options` is
validated by the channel definition's pure validator (ADR 0052), decoded
strictly (unknown fields rejected), and preserved as authored on publication.
Two installed definitions that share a provider and a mode are rejected when
the catalog is built: the adapter kind is an implementation label, never a third
selection key or a precedence rule a caller has to reason about. An unavailable
provider or mode fails load, even when its name appears among the preparation
plan's reference cases; a provider that is named but not installed is never
silently WhatsApp.

**Common settings and adapter options stay apart.** Pacing, jitter, the
per-minute ceiling, and contact filtering are shared messaging settings.
Presence (read receipts, typing, reply delay), history import, address-book
import, and external-reply observation are adapter capabilities: a setting the
selected channel does not declare fails validation, and WhatsApp's implicit
defaults never enable a feature on another channel. Detecting external replies
requires the channel's observation capability, never a simulated one.

**One catalog everywhere.** The catalog (ADR 0052) is injected into CLI
validation, startup, watcher loads, and API publication. Strict decoding,
origin-aware errors, scopes, masking, saved and active versions, and atomic
writes are preserved. `config.schema.json` is regenerated from Go types only
when the types are implemented; hand-editing generated artifacts stays
forbidden.

Registered adapter assembly can own synchronized, instance-lifetime resources
and build per-bot logical views over them (ADR 0004). That ownership is outside
pure definitions/validation and needs no common backend, pool, or profile-switch
object. Do not use package-global mutable state or initialize live resources
for config-only operations. Resource sharing is not shared account identity.

**Remote credentials are not config.** Account setup (ADR 0053) owns channel
credentials; publication never overwrites a runtime credential with a masked
`***`. A local, reference-based credential method is not part of this
preparation; it would extend ADR 0014 deliberately.

**Reload compares the channel.** `config/reload.go` compares provider, mode,
adapter options, and binding-relevant references. Transport changes stay
restart-required initially, and a provider or profile change is an identity
change, not a label edit or a silent history migration; it never reconnects
another scope.

A temporary context selection inside an adapter-owned backend is not a config
change or binding replacement. Compare the fixed binding/profile reference,
not whichever profile the service happens to have selected (ADR 0051/0053).

**No compatibility layer.** The preparation is new design (ADR 0052, PLAN.md
"no backward compatibility"). A superseded key, template root, selector, event,
or result field is removed with its owning phase and fails validation with a
precise error; it is never translated, aliased, or accepted as a no-op.
Databases start clean, so no migration or backfill is required. A host that
keeps using WhatsApp keeps it through the neutral contracts, not by preserving
their old shapes.

**Trusted auxiliary endpoints wait for their decision.** A managed local
channel endpoint (for example a SimpleX websocket) is not an action URL; it
needs its own trusted-connection boundary before one is added. ADRs 0047–0049
are accepted but not implemented, so no confinement or egress protection is
assumed.

This pending decision includes service/store ownership, grouping of bindings,
exclusive command coordination, shutdown, and overload/replay behavior for a
shared source. A scoped caller cannot select unmanaged endpoints or another
profile through an unchecked identifier. Logical scope isolation is not an
independent physical failure or resource boundary (ADR 0046). No process
supervisor, automatic sharding, or per-service account capacity is approved.

## Consequences

- A bot names its channel; a document without a selector fails validation
  instead of assuming WhatsApp.
- A second provider adds a definition and a builder at the composition root,
  not a switch in `cmd/`.
- The reload path can tell a channel change from an ordinary setting change
  and defer it correctly (ADR 0009).
- Implementation is P2, P5, and P6 of `docs/channel-adapter-findings/channel-plan.md`.
