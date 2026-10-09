# SimpleX Chat adapter findings

Recorded: 2026-10-09. Adapter implementation, deployment, and capacity remain pending.
[Channel plan](../channel-plan.md), sections 5 and 8, owns the fixed-profile reference case;
ADR 0054 owns the unresolved trusted auxiliary-endpoint boundary.
The concrete shared-service design below remains a candidate for this future adapter, not an
approved deployment. The owner-approved common contract constraints and fake gates now live in
the channel plan's section 2.1; they do not implement this production coordinator.

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

## Outbound send contract

Reviewed on 2026-10-09 against the same pinned 7.0.4 source above. See the
[outbound matrix](outbound-messages.md). JSON commands use the CLI WebSocket protocol, not a
new Go SDK dependency. No live sends or recipient-rendering tests were performed.

### Common command and content envelope

The WebSocket request has a correlation `corrId` and a `cmd` string. Message sending uses
`APISendMessages`:

```text
/_send <chatRef>[ live=on][ ttl=<seconds>][ sign=on] json <composedMessages>
```

- Destination `ChatRef` uses `@<contactId>` for direct, `#<groupId>` for group, with optional
  group scope. These IDs are local to the selected profile/store, not provider-global identities.
- Required `composedMessages` array entries contain `msgContent` and a `mentions` map (use `{}`
  when empty); optional `fileSource`, `quotedItemId`.
- `fileSource` is a `CryptoFile` with required `filePath`, optional `cryptoArgs` (`fileKey`,
  `fileNonce`). Paths are read by **simplex-chat**, not by the Go WebSocket client. A remote
  auxiliary service must be able to access the staged bytes; an arbitrary Botflota path or URL
  is not automatically meaningful there.
- Response `NewChatItems` identifies `user` and created chat items. A batch can yield multiple
  item IDs; file-transfer completion and recipient delivery arrive separately.
- Active-profile selection and command execution require the shared coordination already
  described above. A message payload's local IDs do not remove that requirement.

### Payload parameters

`msgContent` is a tagged union. Required fields below follow the generated API types; a caption
may be an empty `text` string. The `image` string contains inline image/preview data, not the
attachment's filesystem path or a replacement for `fileSource`.

- **Text:** `{type: "text", text}`. Formatting uses SimpleX's supported text syntax; the receive
  model's `formattedText` is not a writable Telegram-style entity list.
- **Link preview:** `{type: "link", text, preview}`; `preview` needs `uri`, `title`,
  `description`, `image`, with optional `content` (`page`, `image`, `video` with optional duration).
  The sender supplies preview data; receiving it need not fetch a third-party website.
- **Image:** `{type: "image", text, image}` plus `fileSource` for the transferred image.
  Separate inline preview data from the original binary attachment.
- **Video:** `{type: "video", text, image, duration}` plus `fileSource`; `image` is the preview,
  `duration` is seconds. Sending does not automatically generate that preview or transcode video.
- **Voice:** `{type: "voice", text, duration}` plus `fileSource`; duration is seconds.
  Actual encoding and recipient compatibility must be checked, not inferred from the integer.
- **Document/file:** `{type: "file", text}` plus `fileSource`. Audio/GIF can be sent as files,
  but that is not native music-player, sticker or animation presentation.
- **Batch:** multiple composed messages in one `/_send`. This produces multiple chat items;
  it does not declare an album with atomic delivery or one shared provider message ID.
- **SimpleX connection card:** `{type: "chat", text, chatLink, ownerSig?}`.
  `MsgChatLink` variants are `contact` (`connLink`, `profile`, `business`), `invitation`
  (`invLink`, `profile`), and `group` (`connLink`, `groupProfile`). Only short links are allowed.
  Prefer the library's `APIShareChatMsgContent` / `APIShareMyAddress` paths to manually inventing
  link/profile/signature content. This is not a telephone contact card.

Other tags include moderation `report` and a forward-compatible `unknown`. They are not a
generic extension mechanism for Botflota or proof that arbitrary new payloads render natively.

### Quotes, mentions, and live/timed messages

- **Quote:** `ComposedMessage.quotedItemId` is the prior local chat item ID. The CLI constructs
  the wire quote from its stored item, unlike Signal's caller-supplied author/timestamp quote.
- **Group mention:** `mentions` maps textual mention names to group-member IDs alongside
  the message text. The receive model uses a different, richer `CIMention` representation.
- **Timed message:** optional send `ttl` in seconds; requires timed-message support/preferences
  for the conversation. Do not confuse `contact.chatItemTTL` local history retention with a
  remotely negotiated disappearing-message timer.
- **Live text:** `live=on` marks a live message; `APIUpdateChatItem` can continue it.
  This is a persistent chat-item update flow, not Telegram's 30-second temporary draft.
- **Spoiler text:** supported concealed-text formatting in the body; there is no reviewed
  Telegram-equivalent per-media spoiler overlay.

### Control command parameters

- **Edit — `APIUpdateChatItem`:** `chatRef`, `chatItemId`, optional `live=on`, JSON
  `UpdatedMessage` containing `msgContent`, `mentions`.
  Syntax: `/_update item <chatRef> <chatItemId>[ live=on] json <updatedMessage>`.
- **Delete — `APIDeleteChatItem`:** `chatRef`, one or more `chatItemIds`, `deleteMode`.
  `broadcast` requests remote deletion; `internal`, `internalMark`, `history` are not equivalent.
  Syntax: `/_delete item <chatRef> <comma-separated-ids> broadcast|internal|internalMark|history`.
- **Reaction — `APIChatItemReaction`:** `chatRef`, `chatItemId`, add/remove boolean and
  `{type: "emoji", emoji}`. Syntax: `/_reaction <chatRef> <id> on|off <reaction-json>`.
- **Forward — `APIForwardChatItems`:** destination/source chat references, item IDs, optional
  send-as-group and TTL. Source and destination belong to the active profile; unreceived or
  unavailable file content can need confirmation or prevent forwarding.
  Syntax: `/_forward <toChatRef>[ as_group=on] <fromChatRef> <ids>[ ttl=<seconds>]`.

Editing, deletion, files, voice and reactions depend on chat/group preferences, ownership and
role. A successful local command does not certify final transfer/delivery.

### Command menus, not per-message buttons

`ChatBotCommand` supports:

- Command: `{type: "command", keyword, label, params?}`.
- Nested menu: `{type: "menu", label, commands[]}`.

Publish them in `Profile.preferences.commands`, per-contact `Preferences.commands`, or
`GroupProfile.groupPreferences.commands`; `peerType="bot"` identifies a bot profile.
Relevant operations are `APIUpdateProfile` (`/_profile <userId> <profile-json>`),
`APISetContactPrefs` (`/_set prefs @<contactId> <preferences-json>`) and `APIUpdateGroupProfile`.
Preserve the other profile/preference fields when updating them.

The source explains that choosing a command without `params` sends it; with `params`, the UI
asks for/fills parameters. This does not carry a per-message opaque callback value or the
same acknowledgement semantics as Telegram/Cloud reply buttons.

### Unsupported native payloads at this baseline

`MsgContent` has no dedicated location/live-location/venue, telephone contact card, poll/quiz,
sticker, arbitrary callback buttons/list, checklist, invoice or inline form. A map link, vCard
file, image or numbered text menu is a separately chosen fallback, not native feature support.
No provider-native future scheduling, view-once media or notification-silent send option was
established. Calls are a separate signaling/media lifecycle, outside this send-content inventory.

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
- [Generated message commands][send-commands] and [generated payload types][send-types].
- [Preference source][preferences]: command menus and selection semantics.
- [Message guide][message-guide]: media, text formatting, quoting, live and timed messages.

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
[send-commands]: https://github.com/simplex-chat/simplex-chat/blob/479548e/bots/api/COMMANDS.md
[send-types]: https://github.com/simplex-chat/simplex-chat/blob/479548e/bots/api/TYPES.md
[preferences]:
  https://github.com/simplex-chat/simplex-chat/blob/479548e/src/Simplex/Chat/Types/Preferences.hs
[message-guide]: https://simplex.chat/docs/guide/send-messages.html
