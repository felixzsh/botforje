# WhatsApp Cloud API adapter findings

Recorded: 2026-10-09. Technical investigation and implementation are pending.
[ROADMAP](../../ROADMAP.md), Phase 18, lists `adapters/whatsappcloud` as a future channel.
This entry preserves its existing context; the discussion did not investigate a concrete SDK,
setup flow, deployment, or resource benchmark for it.

## Existing direction

- This is another channel reaching WhatsApp, not a new messaging provider or an extension of
  the native whatsmeow device model.
- The roadmap keeps it behind the same common messaging contracts. Onboarding and pricing
  differ from the native channel; graph behavior does not become Cloud API-specific.
- The owner's browser-free, direct-integration preference should inform the future investigation.
  It does not establish a selected library or a finalized deployment here.

## Revisit when implementing

- Investigate official setup, credential custody, inbound delivery, outbound acceptance, and
  implemented capabilities rather than inheriting native-client assumptions.
- Evaluate direct Go integration before introducing another language runtime or SDK dependency.
- Verify provider policy, pricing, limits, and eligibility at that time.
- Resolve any new credential requirements under ADR 0053.
- Measure the selected path; do not extrapolate RAM, CPU, or 1000-account capacity from whatsmeow.

No Cloud API library, resource estimate, new dependency, or completed plan gate was established
by this conversation.
