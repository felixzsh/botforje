# Signal adapter findings

Recorded: 2026-10-09. Implementation and setup-flow investigation are pending.
[Channel plan](../plan.md) owns the goal; [domain cleanup](../domain-cleanup.md)
owns the candidate direction and [open decisions](../open-decisions.md) the
unresolved questions.
This note records the owner's library direction and outbound research, not an implemented adapter.

## Owner-selected direction

The owner explicitly chose **signalmeow** for the future Signal adapter in the discussion.
The desired approach is protocol-oriented integration, comparable in spirit to whatsmeow,
without browser automation or an unnecessary runtime wrapper.

This choice was not part of the existing channel plan. It is preserved here for the future
adapter work; no dependency, pinned version, code, or canonical ADR was changed by this note.

## What was and was not established

- The channel plan considered Signal's identity semantics, not a particular library or transport.
  Its privacy, username, and service-identifier analysis is contrast
  material for [domain cleanup](../domain-cleanup.md).
- Outbound research below identifies the upstream module/source. Supported setup flows, build
  requirements, native dependencies, and license compatibility remain implementation research.
- No benchmark was run. Similarity to whatsmeow does not establish equal RAM, CPU, or capacity.
  There is no verified resource estimate for 1000 Signal bindings here.

## Revisit when implementing

- Verify the selected upstream implementation and pin the intended module/version.
- Record its concrete setup, credential storage, reconnect, send, and receive requirements in
  the appropriate decision owners before they constrain shared contracts.
- Verify any native/runtime dependencies rather than assuming a fully self-contained Go binary.
- Resolve ADR 0053's credential-protection gate before shipping a new credential type.
- Measure the actual client under representative workloads and declare only implemented features.

The selection does not authorize provider-specific fields in common contracts or assume that
phone registration makes telephone numbers universal peer or binding identities.

## Outbound send contract

Reviewed on 2026-10-09 against **mautrix/signal v0.2609.0**, commit `2541762364a9`, module
`go.mau.fi/mautrix-signal`, package `pkg/signalmeow`. See the
[matrix](../provider-differences/outbound-messages.md).
This is signalmeow, not signal-cli or a REST wrapper. No live sends were tested.

### Common send path

- Direct: `Client.SendMessage(ctx, recipientID, content) SendMessageResult`.
  `recipientID` is `libsignalgo.ServiceID`; `content` is `*signalpb.Content`.
- Group: `Client.SendGroupMessage(ctx, gid, content) (*GroupMessageSendResult, error)`;
  `gid` is the library's group identifier. The client supplies group metadata/recipient handling.
- Ordinary content: construct `signalpb.DataMessage`, set **`Timestamp` in Unix milliseconds**,
  then `signalmeow.WrapDataMessage(dm)`. There is no separate `SendPhoto`/`SendPoll` method.
  Although proto fields are optional, the direct send path dereferences the timestamp.
- Inspect `SendMessageResult.WasSuccessful` / `Error`; group results can distinguish successful
  and failed recipients. An accepted send is not proof that every group participant received it.
- File upload: `Client.UploadAttachment(ctx, body []byte) (*signalpb.AttachmentPointer, error)`.
  It supplies encryption/CDN metadata; the caller supplies presentation metadata below.

### Text, preview, and attachment payloads

- **Text:** `DataMessage.Body`; optional `BodyRanges[]` for bold, italic, monospace,
  strikethrough, spoiler and group mentions. Ranges use UTF-16 offsets/lengths; a mention carries
  the participant's ACI, not a telephone number in common content.
- **Preview:** `DataMessage.Preview[]`; each `Preview` includes `Url`, optional `Title`,
  `Description`, `Date`, uploaded `Image`. The adapter must obtain preview content explicitly.
- **Image/video/audio/document:** `DataMessage.Attachments[]` with uploaded pointers;
  `AttachmentPointer.ContentType`, `FileName`, optional `Width`, `Height`, `Caption`, `BlurHash`.
  The uploaded pointer already contains CDN identifier, encryption key, digest and byte size.
  Use `DataMessage.Body` when sending message-level caption text with formatting.
- **Multiple attachments:** repeated pointers in the same data message, not multiple Telegram
  album message IDs. All required files must be uploaded before constructing the send.
- **Voice:** audio attachment with `Flags=VOICE_MESSAGE`; the examined upstream converter uses
  `audio/aac`. It converts other encodings only when its separate FFmpeg support is available.
  Do not assume signalmeow itself transcodes or produces waveform/duration metadata.
- **Animation/GIF:** supported attachment MIME plus `Flags=GIF` when applicable;
  the upstream converter handles image GIF and MP4 animation presentation.
- **Sticker:** `DataMessage.Sticker` with `PackId` (16 bytes), `PackKey` (32 bytes), `StickerId`,
  uploaded `Data`, optional `Emoji`. The converter supports PNG/APNG and static WebP; conversion
  from GIF to APNG needs FFmpeg. iOS validates pack key/ID lengths even for an ad-hoc sticker.
- **View once:** `DataMessage.IsViewOnce=true` for compatible media, with the appropriate
  `RequiredProtocolVersion`. No native view-once arbitrary-text contract was established.

The examined bridge advertises a 100 MiB attachment cap and 2000-character text limit, but those
are **bridge implementation values**, not a verified signalmeow/service maximum. Its TODO still
mentions long-text attachment handling. Do not publish these as provider-wide hard limits.

### Contact information and polls

- **Contact card:** `DataMessage.Contact[]` of `DataMessage_Contact`; nested `Name` (`GivenName`,
  `FamilyName`, `Prefix`, `Suffix`, `MiddleName`, `Nickname`), `Number[]` / `Email[]` (`Value`,
  `Type`, `Label`), `Address[]` (street, city, region, postcode, country, etc.), optional uploaded
  `Avatar` and `Organization`. Multiple cards are representable. This is native typed shared
  contact information, not the adapter's address-book sync operation or a universal peer key.
- **Poll:** `DataMessage.PollCreate` with `Question`, `Options[]` (strings), `AllowMultiple`.
  Set `RequiredProtocolVersion=POLLS` (8). The upstream connector actually constructs and sends
  this payload: poll support is not inferred solely from a dormant protobuf.
- The bridge advertises at most 10 options and 100 characters per option; distinguish that
  validation from a universally certified service limit. Signal's boolean `AllowMultiple` does
  not express an arbitrary numeric maximum, anonymity, correct quiz answers or timed closing.
- **Vote:** `DataMessage.PollVote` needs original `TargetAuthorAciBinary`,
  `TargetSentTimestamp`, selected zero-based `OptionIndexes[]`, and increasing `VoteCount`.
  The original author plus timestamp identifies the poll; a timestamp alone is not global.
- **End poll:** `PollTerminate.TargetSentTimestamp` exists in the protobuf, but the examined
  bridge explicitly declares `PollEnd` unsupported and has no corresponding send handler.
  Treat this as a candidate requiring confirmation, not a declared implementation capability.

### Quotes, controls, and disappearing messages

- **Quote:** `DataMessage.Quote` with `Id` (original timestamp), `AuthorAciBinary` or `AuthorAci`,
  optional `Text`, `BodyRanges`, quoted attachment metadata, and `Type`. Poll quotes have a
  separate quote type. Keep message author and destination distinct.
- **Reaction:** `DataMessage.Reaction` with `Emoji`, `Remove`, `TargetAuthorAciBinary`,
  `TargetSentTimestamp`; set the applicable protocol version and a new send timestamp.
- **Edit:** `signalpb.EditMessage.TargetSentTimestamp`, replacement `DataMessage` with a new
  timestamp; send `WrapEditMessage(edit)`. The bridge advertises 24-hour / 10-edit bounds;
  recipient/service rules still need adapter-level enforcement.
- **Delete:** `DataMessage.Delete.TargetSentTimestamp`, outer new timestamp. Original ownership
  matters; newer administrator-delete protobufs are not proof of implemented admin support.
- **Forward presentation:** `DataMessage.Flags=FORWARD` exists. This does not provide a
  server-side `ForwardMessage(originalID)` operation; validate payload reconstruction separately.
- **Disappearing:** `ExpireTimer` in seconds, `ExpireTimerVersion`, and applicable conversation
  state. Timer-change messages use `Flags=EXPIRATION_TIMER_UPDATE`. The upstream capability
  describes after-read expiry, not provider-native scheduled delivery.

### No native location or interactive keyboard

The examined `DataMessage` has no map-location, venue, live-location, arbitrary button/list,
invoice, checklist, or embedded-form constructor. The upstream location converter produces a
formatted text URL instead. That is an explicit fallback possibility, **not** native location
support. Sharing a contact card also does not request that the recipient share their own contact.

Calls, stories, payment records, pinning and administrator deletion are not ordinary outbound
message kinds merely because other protocol structures exist. They were not established here.

## Sources

- [Pinned sending source][sending]: common sends, timestamp handling and wrappers.
- [Attachment upload][attachments] and [protobuf directory][protobuf], `SignalService.proto`.
- [Actual outbound converter][converter]: media, stickers, quotes and location-as-text.
- [Poll/reaction/edit send handlers][handlers] and [bridge capabilities][caps].

[sending]: https://github.com/mautrix/signal/blob/v0.2609.0/pkg/signalmeow/sending.go
[attachments]: https://github.com/mautrix/signal/blob/v0.2609.0/pkg/signalmeow/attachments.go
[protobuf]: https://github.com/mautrix/signal/tree/v0.2609.0/pkg/signalmeow/protobuf/signalpb
[converter]: https://github.com/mautrix/signal/blob/v0.2609.0/pkg/msgconv/from-matrix.go
[handlers]: https://github.com/mautrix/signal/blob/v0.2609.0/pkg/connector/handlematrix.go
[caps]: https://github.com/mautrix/signal/blob/v0.2609.0/pkg/connector/capabilities.go
