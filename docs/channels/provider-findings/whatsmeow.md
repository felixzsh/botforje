# WhatsApp native outbound findings

Reviewed: 2026-10-09 against the repository's whatsmeow dependency
`v0.0.0-20260909164725-b25a56d63729`. This is the native baseline for the
[outbound matrix](../provider-differences/outbound-messages.md), not approval to expose every
protobuf field in Botflota.

## Outbound send contract

The common operation is `Client.SendMessage(ctx, to, message, extra...)`:

- Required: `ctx`, destination `types.JID`, and `*waE2E.Message` containing the selected payload.
- Optional `SendRequestExtra`: `ID`, `Timeout`, and specialized transport fields. These remain
  adapter-owned. The `AdditionalNodes` field explicitly warns that it requires protocol knowledge.
- Media first uses `Upload(ctx, plaintext, MediaType)` or
  `UploadReader(ctx, reader, tempFile, MediaType)`, then `SendMessage`.
- Upload returns `URL`, `DirectPath`, `MediaKey`, `FileSHA256`, `FileEncSHA256`, `FileLength`.
  Copy the applicable fields into the media protobuf. These are encrypted upload metadata,
  not an arbitrary externally hosted source URL.
- The send waits for the server acknowledgement and returns `SendResponse.ID` and `Timestamp`,
  not proof of recipient delivery or rendering.

### Text, previews, and media

All field names below are the Go protobuf field names. Required means necessary to construct
meaningful content, not that proto2 itself marks every field required.

- **Text:** `Message.Conversation`; use `ExtendedTextMessage.Text` when adding context or a
  preview. Styling is WhatsApp's inline text syntax, not Telegram entity offsets.
- **Link preview:** `ExtendedTextMessage.Text`, `MatchedText` for the URL; optional `Title`,
  `Description`, `JPEGThumbnail` and uploaded thumbnail metadata. The sender constructs metadata;
  `SendMessage` does not fetch a webpage and create a preview for arbitrary text.
- **Image:** `ImageMessage` with upload metadata and `Mimetype`; optional `Caption`, `Width`,
  `Height`, `JPEGThumbnail`, `ContextInfo`.
- **Video:** `VideoMessage` with upload metadata and `Mimetype`; optional `Caption`, `Seconds`,
  `Width`, `Height`, `JPEGThumbnail`, `ContextInfo`.
- **Animation:** `VideoMessage.GifPlayback=true` with compatible video bytes, not raw GIF bytes
  passed to a made-up GIF API. No automatic transcoding is implied.
- **Audio file:** `AudioMessage` with upload metadata and `Mimetype`; optional `Seconds`.
- **Voice note:** the audio fields plus `PTT=true`; optional `Waveform`. Use client-compatible
  voice encoding, normally Ogg/Opus. Generic audio and voice presentation are distinct.
- **Document:** `DocumentMessage` with upload metadata and `Mimetype`; optional `FileName`,
  `Title`, `Caption`, `PageCount`, thumbnail and context.
- **Sticker:** `StickerMessage` with compatible WebP bytes and upload metadata; optional `Width`,
  `Height`, `IsAnimated`. A sticker is not a captioned image.
- **View once:** wrap supported media in the corresponding `FutureProofMessage` view-once field,
  such as `ViewOnceMessageV2.Message`. Sender and recipient constraints still apply.

### Location, contact, and poll

- **Static location / named place:** `LocationMessage.DegreesLatitude`, `DegreesLongitude`;
  optional `Name`, `Address`, `URL`, `Comment`, `AccuracyInMeters`, `JPEGThumbnail`, context.
  A named place is this payload, not a separate `sendVenue` operation.
- **Contact card:** `ContactMessage.DisplayName`, `Vcard` containing contact information;
  optional `ContextInfo`. Telephone numbers, email and address belong inside the vCard.
- **Multiple cards:** `ContactsArrayMessage.Contacts[]`, each a `ContactMessage`;
  optional aggregate `DisplayName` and context.
- **Poll:** `BuildPollCreation(name, optionNames, selectableOptionCount)` creates a sendable
  `Message`; then call `SendMessage`. Question and options are strings; selection count is an
  integer. The builder adds a random message secret used to encrypt/decrypt votes. Persist the
  library's secret state if subsequent vote operations are needed.
- **Vote:** `BuildPollVote(ctx, pollInfo, optionNames)` and send its result. Requires the original
  poll's `types.MessageInfo` and stored secret, not merely a displayed question.

### Replies, mentions, and controls

- **Quote:** payload `ContextInfo` uses `StanzaID`, original `Participant`, `QuotedMessage` and,
  where needed, `RemoteJID`. A quote may require original content as well as its ID.
- **Mention:** `ContextInfo.MentionedJID[]` plus appropriately presented text. Mention addressing
  and PN/LID resolution remain native-adapter work.
- **Forward:** reuse the payload and set `ContextInfo.IsForwarded` / `ForwardingScore` as
  appropriate. This does not give the server a neutral cross-provider original-message ID.
- **Reaction:** `BuildReaction(chat, sender, id, reaction)`, then send; empty reaction removes it.
- **Edit:** `BuildEdit(chat, id, newContent)`, then send. Editing limits are service-controlled.
- **Revoke:** `BuildRevoke(chat, sender, id)`, then send. Sender ownership and group rights matter.
- **Disappearing context:** `ContextInfo.Expiration` and associated chat settings/state, not
  a universal arbitrary per-message after-read timer. Recheck the complete chat lifecycle.

### Fields that do not establish a supported send feature

- **Live location:** `LiveLocationMessage` contains coordinates, accuracy, speed, heading,
  `SequenceNumber` and `TimeOffset`. This review did not prove starting, updating and stopping
  a native live-location session. A lone payload is not that lifecycle.
- **Round video:** `Message.PtvMessage` has a video payload; existence alone does not prove
  all required transport/wrapping and rendering behavior.
- **Album:** the schema has album/group-association fields; no reviewed high-level helper
  established a Telegram-equivalent multi-message album contract.
- **Event:** `EventMessage` has `Name`, `StartTime`, optional `EndTime`, `Description`, `Location`,
  `JoinLink`, cancellation and guest/reminder fields. RSVP and message-secret behavior need
  separate validation; receiving an event does not certify reliable outbound event support.
- **Quiz:** `PollCreationMessage` has `PollType`, `CorrectAnswer` and newer poll fields beyond
  the simple builder. No working quiz send lifecycle was established here.
- **Rich cards / products:** `InteractiveMessage` and commerce protobufs exist, but server
  acceptance, account eligibility and client rendering were not established as stable support.

### Buttons and lists: deliberately not a checkmark

Old upstream discussions report buttons/lists as unsupported. Issue 1144, opened in May 2026,
reports native-flow `quick_reply` and `cta_url` rendering with additional protocol nodes, while
`flow`, `single_select` and `address_message` show an incompatible-message notice.

The candidate wire shape is `InteractiveMessage` with `Body.Text`, optional header/footer and
`NativeFlowMessage.Buttons[]`. Each button has `Name` and `ButtonParamsJSON`; extra transport
nodes may also be required. Older shapes are `ButtonsMessage` and `ListMessage`.

The issue is user-reported evidence, not maintainer certification, a supported example, or a
guarantee that those JSON keys and nodes work for this dependency/account/recipient today.
Do not advertise a common button/list capability based on serializing these structs. Use the
official Cloud API findings for its documented button/list parameter contracts.

## Botflota's current subset

The inspected [native content builder][local-content] sends text, static location, one contact,
plain polls, and image/video/audio/document/sticker. It does not expose the richer variants above.
In particular, its audio builder does not set `PTT`; ordinary audio support is not voice-note
support. Its local media-fetch cap is 16 MiB, not a claim about WhatsApp's platform maximum.

## Sources

- [Pinned sending source][send]: send acknowledgement, request extras, context, control builders.
- [Pinned upload source][upload]: encryption, reader upload and returned metadata.
- [Pinned poll helpers][polls]: creation, votes and secret handling.
- [Pinned protobuf directory][proto]: `WAWebProtobufsE2E.proto`, including speculative fields.
- [Discussion 534][buttons] and [discussion 711][lists]: older unsupported-button/list reports.
- [Issue 1144][flows]: conflicting 2026 native-flow rendering report.

[local-content]: ../../../adapters/whatsapp/channel/content.go
[send]: https://github.com/tulir/whatsmeow/blob/b25a56d63729/send.go
[upload]: https://github.com/tulir/whatsmeow/blob/b25a56d63729/upload.go
[polls]: https://github.com/tulir/whatsmeow/blob/b25a56d63729/msgsecret.go
[proto]: https://github.com/tulir/whatsmeow/tree/b25a56d63729/proto/waE2E
[buttons]: https://github.com/tulir/whatsmeow/discussions/534
[lists]: https://github.com/tulir/whatsmeow/discussions/711
[flows]: https://github.com/tulir/whatsmeow/issues/1144
