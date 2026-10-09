# Telegram adapter findings

Recorded: 2026-10-09. Implementation and library selection are pending.
These notes preserve the investigation, not an approved dependency or a performance guarantee.
[Channel plan](../channel-plan.md), sections 5 and 8, owns the architectural reference cases;
[ROADMAP](../../ROADMAP.md), Phase 18, owns future adapter implementation.

## Owner preference

Avoid browser automation. Prefer direct Go integration with the provider's API or protocol,
without another language runtime or a heavyweight process per account where practical.
A thin wrapper is not inherently expensive; browser and process-per-account costs are separate.

## Two distinct account modes

### Bot API

- Uses provider bot accounts created through BotFather and authenticated with a bot token.
  This is not login to a person's Telegram account.
- Sending uses HTTPS; receiving uses long polling or webhooks. WebSocket is not required.
- Go's `net/http` can access the hosted API directly. Neither a browser, Node, nor a local
  Telegram client process is required for this path.
- An SDK may be convenient, but none was selected in this investigation.

### User login through MTProto

- A personal-account integration is a separate mode, not a different Bot API token.
- `gotd/td` is a candidate native Go client with a full MTProto implementation and user/bot
  authentication support. It does not require a browser or a separate language runtime.
- Its availability establishes a browser-free implementation path, not a decision to adopt it,
  a commitment to all Telegram features, or proof of Botflota's capacity.

## Resource considerations

Bot API integration can keep the local client relatively simple because Telegram hosts the API
backend. This is an architectural expectation, not a measured comparison with whatsmeow.
MTProto maintains more client-side protocol and session state; measure the chosen implementation.

No benchmark was run for 1000 bindings. Idle account count alone is insufficient: contacts,
message rate, media, reconnects, queues, and stored history affect memory, CPU, and I/O.
Go or persistent connections do not guarantee a particular resource budget.

## Revisit when implementing

- Choose the first account mode and, only if justified, its Go dependency and pinned version.
- Choose polling or webhooks for Bot API and settle acknowledgement/replay behavior against
  durable processing; neither an HTTP response nor an inbound handoff proves a committed turn.
- Verify setup, session/token custody, revocation, provider limits, and actual send eligibility.
  The credential-protection gate remains in ADR 0053.
- Measure representative idle, active, media, and reconnect workloads before publishing capacity.

The plan considered these modes to challenge common contracts. It did not choose a Telegram SDK
or require every bot to support both modes. Protocol-specific behavior stays in the adapter.

## Sources

Reviewed on 2026-10-09; upstream pages and branches may change.

- [Telegram Bot API](https://core.telegram.org/bots/api).
- [Receiving updates](https://core.telegram.org/bots/faq#how-do-i-get-updates).
- [User authorization](https://core.telegram.org/api/auth).
- [gotd/td](https://github.com/gotd/td), whose README describes its native Go implementation.
