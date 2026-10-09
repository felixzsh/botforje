# SimpleX Chat adapter findings

Recorded: 2026-10-09. Adapter implementation, deployment, and capacity remain pending.
[Channel plan](../channel-plan.md), sections 5 and 8, owns the fixed-profile reference case;
ADR 0054 owns the unresolved trusted auxiliary-endpoint boundary.
The shared-service design below is a candidate to evaluate when this adapter starts, not an
accepted implementation requirement or a new P1–P6 gate.

## Browser-free integration path

The official bot API exposes a local WebSocket server from the `simplex-chat` CLI.
A Go adapter can exchange JSON commands and events directly, without its TypeScript SDK,
Node, a browser, or a graphical desktop app.

`simplex-chat` is a native executable compiled from Haskell, with precompiled releases.
It does not require a Haskell interpreter at runtime; system-library requirements depend on
the selected build. Docker is optional.

This is still an auxiliary process: it owns profiles, protocol connections, cryptography, and
its stores. A small Go WebSocket client does not remove that process's resource cost or make
the integration equivalent to whatsmeow running inside Botflota's Go process.

## Multiple independent profiles are supported

- Profiles have their own addresses, contacts, and conversations; they need not be related.
- Several profiles remain connected together. Selecting one does not disconnect the others,
  and an unselected profile can receive messages.
- Upstream tests cover multiple-profile reception and subscriptions after restart.
- Documentation permits an unspecified number of profiles. That is not a tested capacity
  guarantee for 1000 active profiles or any particular message rate.

This concerns profiles resident in the managed service. It does not establish independent
server login to 1000 existing mobile profiles or concurrent use of a copied database.
The profile guide warns against using one exported database on multiple devices simultaneously
and says that its export/import moves the whole database, not selected profiles.

## Observed API constraints

Source inspection used stable 7.0.4 at commit
`479548ee53ffb73db73841e77acbeee5a78dbbd5`. Recheck these observations before implementation.

### One shared active profile

The controller has a shared `currentUser`. `APISendMessages` uses that current user;
some operations with an explicit `userId` require it to match the active user.
Commands are not independent stateless operations scoped by a caller's profile.

Selecting a profile and executing its dependent command therefore need shared coordination.
This is selection of an execution context, not unbinding one bot and binding another account.
Other profiles can keep receiving, and accepted network deliveries need not all be serialized.

### One shared event queue

Each WebSocket client's output loop reads the same controller `outputQ`.
Multiple clients compete for events; they do not receive isolated per-profile subscriptions
or a broadcast copy of every event. Opening one WebSocket per bot does not provide isolation.
Message events identify their profile through the `user` field.

### Logical separation, not a tenant security boundary

Profiles share a process, stores, and some settings. The local WebSocket API has no built-in
authentication or per-profile caller authorization. It must not be exposed directly to scoped
callers. One service failure affects every profile it hosts.

## Candidate shared-service arrangement

Evaluate a managed service hosting a measured, reasonable group of profiles. Neither one process
per bot nor one process for 1000 profiles was selected in this discussion.
Several services with groups of profiles are an option if capacity or isolation requires them;
group size and grouping by scope or trust boundary remain undecided.

Within the adapter, a service-level coordinator could:

- Keep each bot bound to one fixed, verified profile.
- Own one WebSocket event reader and route events to the corresponding bot/profile.
- Serialize profile selection together with its dependent command, across every bot using that
  service and every operation relying on active-profile state, not just sends.
- Skip selection only when the selected profile is known; revalidate after reconnect, restart,
  or an ambiguous operation outcome, and prevent uncoordinated writers from changing it.
- Advance after the command result, not after recipient delivery/read confirmation. Preserve
  uncertainty on timeouts instead of blindly retrying a possibly accepted send.
- Stop or detach one bot without stopping the shared service or destroying another profile.

This coordination would remain adapter-owned. It must not replace Botflota's durable outbox,
introduce profile switching into the common engine/fleet/API, or change a bot's binding identity
or generation merely because another profile becomes active in the auxiliary process.

## Resources and validation still needed

No load test was run, and no reliable CLI benchmark for this scenario was found.
There is no established RAM/CPU cost per process or profile, or equivalence with whatsmeow.
Measure the auxiliary service as well as the Go adapter: process baseline, connections,
contacts/groups, cryptographic state, queues, files, history, CPU, and disk I/O all matter.
Separate processes repeat baseline costs; shared profiles do not necessarily cost the same as
that many independent processes. Browser-free does not mean negligible resource use.

For scale context, 1000 profiles receiving one message per minute produce about 17 inbound
messages/s; one per second produces 1000/s, before replies and other events. Account count alone
does not describe the workload.

Before selecting a deployment, test growing profile counts with representative contacts and
traffic, bursts, reconnects/restarts, slow or disconnected WebSocket readers, and profile-switch
races. Verify routing, store/profile ownership, command uncertainty, and peer-bot lifetime safety.
Approve the trusted connection, deployment ownership, and any shared-service lifecycle decisions
under ADR 0054 before implementing them. No process supervisor or automatic sharding is approved.

## Sources

- [Pinned upstream source][source], stable 7.0.4.
- [Bot API][bot-api]: CLI WebSocket operation, JSON messages, and security considerations.
- [Profile documentation][profiles]: local profiles and whole-database export/import.
- [Multiple-profile announcement][announcement]: simultaneous connections, not a scale benchmark.
- [Commands implementation][commands], especially lines 472–478, 642–645, and 706–719.
- [Active-user helpers][internal], lines 3169–3193.
- [WebSocket server][server], lines 97–132.
- [Direct-chat tests][tests], lines 1823–1896.
- [Message events][events]: `NewChatItems` includes `user`.

[source]: https://github.com/simplex-chat/simplex-chat/tree/479548e
[bot-api]: https://github.com/simplex-chat/simplex-chat/blob/479548e/bots/README.md
[profiles]: https://simplex.chat/docs/guide/chat-profiles.html
[announcement]:
  https://simplex.chat/blog/20230204-simplex-chat-v4-5-user-chat-profiles.html
[commands]:
  https://github.com/simplex-chat/simplex-chat/blob/479548e/src/Simplex/Chat/Library/Commands.hs
[internal]:
  https://github.com/simplex-chat/simplex-chat/blob/479548e/src/Simplex/Chat/Library/Internal.hs
[server]: https://github.com/simplex-chat/simplex-chat/blob/479548e/apps/simplex-chat/Server.hs
[tests]: https://github.com/simplex-chat/simplex-chat/blob/479548e/tests/ChatTests/Direct.hs
[events]: https://github.com/simplex-chat/simplex-chat/blob/479548e/bots/api/EVENTS.md
