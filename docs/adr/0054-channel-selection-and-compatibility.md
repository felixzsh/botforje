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

`provider` and `mode` are core-typed. `options` is validated by the channel
definition's pure validator (ADR 0052), decoded strictly (unknown fields
rejected), and preserved as authored on publication. A missing selector means
WhatsApp native during a documented transition, so every existing file loads
unchanged. An unavailable provider or mode fails load, even when its name
appears among the preparation plan's reference cases; a provider that is named
but not installed is not silently WhatsApp.

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

**Remote credentials are not config.** Account setup (ADR 0053) owns channel
credentials; publication never overwrites a runtime credential with a masked
`***`. A local, reference-based credential method is not part of this
preparation; it would extend ADR 0014 deliberately.

**Reload compares the channel.** `config/reload.go` compares provider, mode,
adapter options, and binding-relevant references. Transport changes stay
restart-required initially, and a provider or profile change is an identity
change, not a label edit or a silent history migration; it never reconnects
another scope.

**Additive API and CLI compatibility.** Neutral contact, account, and
conversation references are added, and legacy phone selectors remain for
WhatsApp (ADR 0051, ADR 0044). Numeric or opaque chat ids resolve under their
scope and bot, never by id alone. First-contact webhooks and CSV exports gain
neutral identity and provenance without leaking keys, keeping existing fields
where possible; an incompatible payload or archive change needs its own
decision. History migration refuses incompatible providers or local namespaces
until an explicit mapping exists (ADR 0042).

**Trusted auxiliary endpoints wait for their decision.** A managed local
channel endpoint (for example a SimpleX websocket) is not an action URL; it
needs its own trusted-connection boundary before one is added. ADRs 0047–0049
are accepted but not implemented, so no confinement or egress protection is
assumed.

## Consequences

- Existing YAML keeps working; the transition ends when examples and fixtures
  make the selector explicit.
- A second provider adds a definition and a builder at the composition root,
  not a switch in `cmd/`.
- The reload path can tell a channel change from an ordinary setting change
  and defer it correctly (ADR 0009).
- Implementation is P2, P5, and P6 of `docs/channel-plan.md`.
