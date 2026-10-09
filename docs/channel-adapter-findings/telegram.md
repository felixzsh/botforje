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

## Outbound send contract

Reviewed on 2026-10-09 against Bot API 10.3 and candidate gotd/td v0.162.0. See the
[cross-channel matrix](outbound-messages.md) for support qualifications. No SDK was adopted and
no live sends were tested. Required payload fields and design-relevant options follow; the
linked API is the complete reference, including specialized business/guest/paid-send settings.

### Bot API common envelope

Call `POST https://api.telegram.org/bot<token>/<method>` using JSON, except that new file uploads
use multipart/form-data. The token is credential state, not message content.

- Required destination: `chat_id` (integer or supported `@username`). Do not substitute an
  arbitrary telephone number; ordinary bot initiation/contact restrictions still apply.
- Context-dependent: `message_thread_id` for a topic; `direct_messages_topic_id` is required
  in channel direct-message chats; `business_connection_id` when acting for a connected account.
- Common optional modifiers, where the method allows them: `reply_parameters`, `reply_markup`,
  `disable_notification`, `protect_content`, and `message_effect_id`.
- Formatting: `parse_mode` or explicit `entities`; captions use `caption_entities` instead.
  Entity offsets/lengths use UTF-16 code units, not UTF-8 bytes or Go rune indexes.
- A file argument normally accepts multipart upload, reusable `file_id`, or HTTP URL.
  Exceptions matter: video notes and live photos do not accept URL input; animated/video stickers
  have their own source restrictions. A `file_unique_id` cannot be used to send a file.
- Most calls return one `Message`; albums return an array. Provider acceptance is not a read or
  delivery receipt, and a chosen callback value is not the same event as user-written text.

### Bot API text and media

- **Text — `sendMessage`:** required `text` (1–4096 characters after entity parsing).
  Optional `parse_mode`/`entities`, `link_preview_options`.
- **Link preview:** `sendMessage` with URL text and optional `link_preview_options` fields
  `is_disabled`, `url`, `prefer_small_media`, `prefer_large_media`, `show_above_text`.
- **Photo — `sendPhoto`:** required `photo`; optional `caption`, formatting, `has_spoiler`,
  `show_caption_above_media`. Hosted API limit: 10 MB; width + height at most 10000, ratio at
  most 20. Caption limit: 1024 characters after parsing.
- **Video — `sendVideo`:** required `video`; optional `duration`, `width`, `height`, `thumbnail`,
  `cover`, `start_timestamp`, `caption`, `supports_streaming`, `has_spoiler`, caption placement.
  Normal video presentation expects MPEG4; other encodings can be sent as documents.
- **Animation — `sendAnimation`:** required `animation` (GIF or silent H.264/MPEG4 AVC).
  Optional `duration`, `width`, `height`, `thumbnail`, caption/formatting and spoiler.
- **Audio/music — `sendAudio`:** required `audio` (MP3/M4A for music-player presentation).
  Optional `duration`, `performer`, `title`, `thumbnail`, caption/formatting.
- **Voice — `sendVoice`:** required `voice`; Ogg/Opus, MP3 or M4A for voice presentation.
  Optional `duration`, caption/formatting. A voice note is not `sendAudio` plus a filename.
- **Document — `sendDocument`:** required `document`; optional `thumbnail`, caption/formatting,
  `disable_content_type_detection`. New-upload filename is multipart metadata.
- **Round video — `sendVideoNote`:** required `video_note`; optional `duration`, `length`
  (diameter), `thumbnail`. MPEG4, at most one minute; no URL source or caption parameter.
- **Sticker — `sendSticker`:** required `sticker`; optional `emoji` and send context.
  WEBP for static, TGS for animated, WEBM for video. URL input supports static stickers only.
- **Live photo — `sendLivePhoto`:** required `live_photo` (video) and `photo` (still).
  Video at most 10 seconds and 10 MB; neither part accepts an HTTP URL. Optional caption,
  formatting, caption placement and spoiler.
- **Album — `sendMediaGroup`:** required `media[]` of 2–10 `InputMedia` objects with `type`,
  `media`, and per-item caption/metadata. Supports photo/live-photo/video/audio/document;
  documents and audio can only be grouped with the same type. No per-album `reply_markup`.

The hosted API documents 50 MB uploads for ordinary video, animation, audio, voice and document
methods at this baseline. URL-fetch limits and format requirements differ; a local Bot API
server can accept uploads up to 2000 MB. These are not interchangeable with MTProto limits.
Thumbnail uploads are JPEG, under 200 kB and at most 320×320 where those methods support them.

### Bot API location and contact cards

- **Location — `sendLocation`:** required `latitude`, `longitude`; optional
  `horizontal_accuracy` (0–1500 m), `live_period`, `heading`, `proximity_alert_radius`.
- **Live location:** `live_period` is 60–86400 seconds or `0x7FFFFFFF` for indefinite updates.
  Subsequent `editMessageLiveLocation` needs message identity plus new coordinates;
  `stopMessageLiveLocation` ends it. This is a lifecycle, not just a location flag.
- **Venue — `sendVenue`:** required `latitude`, `longitude`, `title`, `address`; optional
  `foursquare_id`, `foursquare_type`, `google_place_id`, `google_place_type`.
- **Contact — `sendContact`:** required `phone_number`, `first_name`; optional `last_name`,
  `vcard` (up to 2048 bytes). One card per method call, not a native multi-contact array.

### Bot API polls and other structured content

- **Poll — `sendPoll`:** required `question` (1–300 characters), `options[]` (1–12 at this
  baseline, not the older 2–10 rule). Each `InputPollOption` contains `text`; optional formatting
  entities and `media` have their own supported formats.
- Poll options include `is_anonymous`, `type` (`regular`/`quiz`), `allows_multiple_answers`,
  `allows_revoting`, `shuffle_options`, `allow_adding_options`, `hide_results_until_closes`.
  Optional `description`, description formatting, `media`, question formatting and, for channel
  polls, `members_only`, `country_codes`.
- **Quiz:** `type=quiz` additionally requires `correct_option_ids[]` (ordered zero-based indexes).
  Optional `explanation`, formatting and `explanation_media`. Do not use the older singular
  `correct_option_id` shape for this documentation baseline.
- **Closing:** `open_period` (5–2628000 seconds) or `close_date`, mutually exclusive;
  `is_closed` can create a closed preview. `stopPoll` needs `chat_id`, `message_id`.
  Bot API cannot cast a native vote as the bot.
- **Checklist — `sendChecklist`:** required `business_connection_id`, `chat_id`, `checklist`.
  The checklist needs `title` and 1–30 `tasks[]`, each with `id`, `text`; optional formatting,
  `others_can_add_tasks`, `others_can_mark_tasks_as_done`. Not available to an ordinary bot send
  without a business connection.
- **Dice — `sendDice`:** destination plus optional `emoji` chosen from 🎲, 🎯, 🏀, ⚽, 🎳, 🎰.
  Telegram generates the random outcome; the request cannot prescribe it.
- **Game — `sendGame`:** required `chat_id`, `game_short_name` identifying a game registered
  for the sending bot; optional context and inline keyboard, whose first button launches the game.
  This references an external game asset, not arbitrary text/buttons or a personal-account game.
- **Paid media — `sendPaidMedia`:** required `star_count` (1–25000), `media[]` (up to 10
  `InputPaidMedia` items, each with its type/media requirements); optional `payload` (128 bytes),
  caption/formatting and supported context. Selling access is a separate capability from
  ordinary albums or sending an invoice.
- **Rich message — `sendRichMessage`:** required `rich_message`; exactly one of `html`,
  `markdown`, `blocks`. Optional referenced `media[]`, `is_rtl`, `skip_entity_detection`.
  Blocks can represent paragraphs, headings, lists, tables, media, maps, quotations and buttons.
  This is richer structured content than ordinary `sendMessage.parse_mode`.

### Bot API buttons, keyboards, and input requests

These use `reply_markup` on a supported send method; they are not separate `sendButtons` calls.

- **Callback buttons:** `InlineKeyboardMarkup.inline_keyboard` is an array of button rows.
  Each button needs `text` and exactly one action field. `callback_data` is **1–64 bytes**.
  Taps produce `CallbackQuery`; `answerCallbackQuery(callback_query_id, ...)` acknowledges the
  UI independently of any later graph decision.
- **URL button:** button `text` plus `url`. It opens a link, not a stable selection callback.
- **Copy button:** button `text` plus `copy_text: {text}`; copy text is 1–256 characters.
- **Mini App:** inline button `text`, `web_app: {url}`; private user–bot chats only and not
  messages sent for a business account. This launches an external application, not an inline
  common form payload. Other specialized actions include login and inline-query switching.
- **Reply keyboard:** `ReplyKeyboardMarkup.keyboard` is an array of rows of `KeyboardButton`.
  Each button needs `text`; optional keyboard options include `resize_keyboard`,
  `one_time_keyboard`, `is_persistent`, `input_field_placeholder`, `selective`.
  A plain button sends its text, unlike an inline callback's opaque value.
- **Request location/contact:** keyboard button `text` plus `request_location=true` or
  `request_contact=true`; private chats only. Sends the user's content after consent, not the
  location/contact content specified by the bot.
- **Request poll:** keyboard button `request_poll: {type}`; optional type `regular`/`quiz`.
- **Request users:** button `request_users` needs `request_id`; optional user/bot/Premium filters,
  `max_quantity`, and requested name/username/photo fields.
- **Request chat:** button `request_chat` needs `request_id`, `chat_is_channel`; optional rights,
  ownership/username/forum filters and requested title/username/photo fields. These selection
  request variants are private-chat features, not inline callback lists.
- **Force reply / keyboard removal:** `ForceReply.force_reply=true` or
  `ReplyKeyboardRemove.remove_keyboard=true`; optional selective targeting/placeholder.
- **Command menu:** `setMyCommands(commands[], scope?, language_code?)`; each command has
  `command`, `description`. This configures bot UI, not a per-message keyboard.
- **Invoice — `sendInvoice`:** required `title`, `description`, `payload`, `currency`,
  `prices[]` (`label`, integer `amount`), plus destination. `provider_token` is provider-specific
  and empty for `XTR` Stars; optional photo, tips, buyer-data requirements and shipping settings.
  Currency amounts use smallest units, not floats; payment acceptance needs its separate flow.

### Bot API modifiers and controls

- **Reply/quote:** `reply_parameters.message_id`; optional `chat_id`, `quote`, quote entities,
  position and `allow_sending_without_reply`. Some contexts accept `ephemeral_message_id`.
- **Mention:** ordinary `@username` text or explicit mention/link `MessageEntity`; respect privacy
  and UTF-16 offsets. Mentions are not a universal telephone-addressing mechanism.
- **Reaction:** `setMessageReaction(chat_id, message_id, reaction[]?, is_big?)`;
  `ReactionTypeEmoji` carries `type=emoji`, `emoji`; empty array clears. Bot and chat restrictions
  apply, including custom-emoji restrictions and a single reaction per bot at this baseline.
- **Edit:** `editMessageText`, `editMessageCaption`, `editMessageMedia`, `editMessageReplyMarkup`;
  chat/message IDs or supported `inline_message_id`, and replacement content.
- **Delete:** `deleteMessage(chat_id, message_id)`; time/permission rules apply, commonly 48 hours.
- **Forward:** `forwardMessage(chat_id, from_chat_id, message_id)`; `copyMessage` omits forward
  provenance. Batch variants exist; protected/service content has restrictions.
- **Silent/protected/spoiler:** `disable_notification`, `protect_content`; text `spoiler` entity
  or supported media `has_spoiler`. Protection is not end-to-end encryption.
- **Ephemeral group message:** supported send methods accept `ephemeral_message_parameters`
  with required `receiver_user_id`, optional `callback_query_id`,
  `replace_callback_query_message`. This is selective visibility, not a general expiry timer.
- **Incremental draft:** `sendMessageDraft(chat_id, draft_id, text?, ...)` or
  `sendRichMessageDraft(chat_id, draft_id, rich_message, ...)`; optional `can_stop`, `keep_on_stop`.
  Private chats; draft preview lasts 30 seconds and **must** be finalized with a regular send
  to persist. Not equivalent to provider-native scheduled delivery, which Bot API lacks.

### Personal MTProto through candidate gotd/td

This is a separate account mode. A generated Go struct's fields do not override Telegram's
bot-only, Premium, chat-rights, privacy or paid-message restrictions. Secret chats and calls
were not examined.

- **Text:** `tg.Client.MessagesSendMessage(ctx, *tg.MessagesSendMessageRequest)`;
  required `Peer`, `Message`, `RandomID`; optional `Entities`, `ReplyTo`, `Silent`, `NoWebpage`,
  `ScheduleDate`. `Peer` is an `InputPeer`, not a bare Bot API `chat_id`; access-hash resolution
  stays in the adapter. `RandomID` is the caller's deduplication token.
- **All single media:** `MessagesSendMedia(ctx, *tg.MessagesSendMediaRequest)`;
  required `Peer`, `Media`, `Message` (caption, possibly empty), `RandomID`; optional context,
  caption entities, notification and scheduling. gotd's `telegram/message` helpers wrap it.
- **Photo:** `tg.InputMediaUploadedPhoto.File` or existing `InputMediaPhoto.ID`.
  Optional `Spoiler`, `TTLSeconds`, sticker associations; upload helpers provide `InputFile`.
- **Live photo:** the same photo constructors with `LivePhoto=true` and `Video` reference.
  gotd v0.162.0 contains these fields; no separate `InputMediaLivePhoto` is required.
- **Video/audio/voice/sticker/document/animation:** `InputMediaUploadedDocument` needs `File`,
  `MimeType`, `Attributes`; reusable `InputMediaDocument` uses a document reference.
  `DocumentAttributeVideo` supplies `Duration`, `W`, `H`, streaming and `RoundMessage` flags;
  `DocumentAttributeAudio` supplies `Duration`, optional `Title`, `Performer`, `Waveform`,
  `Voice`; filename/sticker/animated attributes select the other presentations.
  Optional upload `Thumb`, `Spoiler`, `ForceFile`, and `TTLSeconds` are distinct.
- **Album:** `MessagesSendMultiMedia(Peer, MultiMedia[])`; each `InputSingleMedia` has
  `Media`, `RandomID`, `Message`, optional entities. Observe media grouping restrictions.
- **Location:** `InputMediaGeoPoint.GeoPoint` (`InputGeoPoint.Lat`, `Long`, optional accuracy).
- **Live location:** `InputMediaGeoLive` with `GeoPoint`, `Period`, optional `Heading`,
  `ProximityNotificationRadius`, `Stopped`; updates use `MessagesEditMessage`.
- **Venue:** `InputMediaVenue` with `GeoPoint`, `Title`, `Address`, `Provider`, `VenueID`,
  `VenueType`; unused optional semantics are encoded as empty strings where TL requires a field.
- **Contact:** `InputMediaContact` with `PhoneNumber`, `FirstName`, `LastName`, `Vcard`;
  last name/vCard may be empty. No native array of contact cards was established.
- **Poll/quiz:** `InputMediaPoll.Poll` with `ID`, `Question` (`TextWithEntities`), `Answers[]`
  (`Text`, opaque `Option` bytes), `PublicVoters`, `MultipleChoice`, `Quiz` and closing fields.
  gotd's `message.Poll(...)` helper constructs it; quiz adds `CorrectAnswers` and optional
  `Solution`/entities/media. Newer Bot API fields must not be copied blindly into a pinned TL type.
- **Vote/stop:** `MessagesSendVote(Peer, MsgID, Options[][]byte)` casts a vote;
  `MessagesEditMessage` with a closed `InputMediaPoll` stops an owned poll.
- **Dice:** `InputMediaDice.Emoticon`; outcome is server-generated.
- **Checklist:** `InputMediaTodo.Todo` (`TodoList.Title`, `List[]` with item `ID`, `Title`,
  modification permissions); Premium required. Not a bot-authored inline selection list.
- **Paid media:** `InputMediaPaidMedia` with `StarsAmount`, `ExtendedMedia[]`, optional `Payload`,
  sent via `MessagesSendMedia`, not `MessagesSendMultiMedia`. Only eligible channels with
  `paid_media_allowed` and administrator rights; the maximum price comes from client config.
- **Link preview:** text send's `NoWebpage` and, where explicit control is needed,
  `InputMediaWebPage` URL/presentation fields. Styled text/mentions use `MessageEntity` values.
- **Quote/reaction/edit/delete/forward:** `ReplyTo` / `InputReplyToMessage`,
  `MessagesSendReaction(Peer, MsgID, Reaction[])`, `MessagesEditMessage(Peer, ID, ...)`,
  `MessagesDeleteMessages(ID[], Revoke)` (channel variant separately),
  `MessagesForwardMessages(FromPeer, ToPeer, ID[], RandomID[])`.
- **Scheduling:** optional `ScheduleDate` on sends; Telegram stores the scheduled message.
  **TTL/view once:** supported media `TTLSeconds`; **spoiler:** text entities or supported
  media `Spoiler`; none makes arbitrary text a disappearing message.
- **Buttons/forms/invoices:** do not advertise bot-authored markup/payment capabilities for
  personal login. `reply_markup` is bot markup even though the send request can represent it.
  Rich Bot API block messages/drafts were not established in this pinned personal-client path.

## Sources

Reviewed on 2026-10-09; upstream pages and branches may change.

- [Telegram Bot API](https://core.telegram.org/bots/api).
- [Receiving updates](https://core.telegram.org/bots/faq#how-do-i-get-updates).
- [User authorization](https://core.telegram.org/api/auth).
- [gotd/td](https://github.com/gotd/td), whose README describes its native Go implementation.
- [Pinned gotd media types][gotd-types] and [poll helper][gotd-polls].
- [MTProto text send][mt-text], [media send][mt-media], [file handling][mt-files],
  [checklists][mt-todo] and [polls][mt-polls].
- Bot API sections for [polls][bot-polls], [keyboards][bot-keys], [rich messages][bot-rich],
  [live photos][bot-live], [checklists][bot-todo], [invoices][bot-invoices],
  and [drafts][bot-drafts].
- Paid media: [Bot API](https://core.telegram.org/bots/api#sendpaidmedia) and
  [MTProto](https://core.telegram.org/api/paid-media); [games][bot-games].

[gotd-types]: https://github.com/gotd/td/blob/v0.162.0/tg/tl_input_media_gen.go
[gotd-polls]: https://github.com/gotd/td/blob/v0.162.0/telegram/message/poll.go
[mt-text]: https://core.telegram.org/method/messages.sendMessage
[mt-media]: https://core.telegram.org/method/messages.sendMedia
[mt-files]: https://core.telegram.org/api/files
[mt-todo]: https://core.telegram.org/api/todo
[mt-polls]: https://core.telegram.org/api/poll
[bot-polls]: https://core.telegram.org/bots/api#sendpoll
[bot-keys]: https://core.telegram.org/bots/api#inlinekeyboardbutton
[bot-rich]: https://core.telegram.org/bots/api#sendrichmessage
[bot-live]: https://core.telegram.org/bots/api#sendlivephoto
[bot-todo]: https://core.telegram.org/bots/api#sendchecklist
[bot-invoices]: https://core.telegram.org/bots/api#sendinvoice
[bot-drafts]: https://core.telegram.org/bots/api#sendmessagedraft
[bot-games]: https://core.telegram.org/bots/api#sendgame
