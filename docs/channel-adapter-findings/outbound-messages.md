# Outbound messaging capability research

Reviewed: 2026-10-09. This is an upstream API/source comparison, not Botflota's implemented
capability catalog, a new contract, or a completed adapter gate. No live sends were tested.
[ADR 0036][kinds] owns message kinds; [ADR 0052][capabilities] owns declared support and rejection.

## Scope and evidence

Columns distinguish **channels and account modes**, not just messaging providers. WhatsApp Cloud
and whatsmeow reach the same provider but expose different features. Telegram user login cannot
inherit the capabilities of a bot token.

| Column | Examined integration | Evidence baseline |
| --- | --- | --- |
| WM | WhatsApp native, whatsmeow | Repository's `b25a56d63729` dependency |
| WC | WhatsApp Cloud API, direct HTTPS | Official documentation reviewed on the date above |
| TB | Telegram Bot API, direct HTTPS | Official Bot API 10.3 documentation |
| TU | Telegram personal account, MTProto | Official API and candidate gotd/td v0.162.0 |
| SG | Signal, signalmeow | mautrix/signal v0.2609.0, commit `2541762364a9` |
| SX | SimpleX Chat CLI WebSocket | Existing findings' 7.0.4 source, commit `479548e` |

The library pins are research baselines, not approval to add or upgrade a dependency.

- ✅: a native send method or concrete typed send path is present in the examined API/source.
  This does not certify account rights, recipient compatibility, or delivery.
- △: restricted, experimental, schema-only, or not equivalent to the named feature. Read the note.
- —: no native send path for this feature in the examined interface. A text/file workaround is
  not counted as support; this is not a claim about every future version of the service.
- ?: not established in this research; do not advertise it as supported.

## Message payloads

| Payload / feature | WM | WC | TB | TU | SG | SX |
| --- | --- | --- | --- | --- | --- | --- |
| Plain text | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Styled text | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Link preview | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Image | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Video | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Audio file / music player | ✅ | ✅ | ✅ | ✅ | ✅ | △ |
| Voice note | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Document / arbitrary file | ✅ | △ | ✅ | ✅ | ✅ | ✅ |
| Sticker | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Animation / GIF presentation | ✅ | △ | ✅ | ✅ | ✅ | △ |
| Round video note | △ | — | ✅ | ✅ | — | — |
| Live photo: still + short video | ? | — | ✅ | ✅ | — | — |
| Album / multiple attachments | △ | — | ✅ | ✅ | ✅ | △ |
| Static map location | ✅ | ✅ | ✅ | ✅ | — | — |
| Live location | △ | — | ✅ | ✅ | — | — |
| Venue / named map place | ✅ | ✅ | ✅ | ✅ | — | — |
| Contact information card | ✅ | ✅ | ✅ | ✅ | ✅ | — |
| Multiple contact cards | ✅ | ✅ | — | — | ✅ | — |
| Native poll | ✅ | — | ✅ | ✅ | ✅ | — |
| Quiz with correct answers | △ | — | ✅ | ✅ | — | — |
| Scheduled event / invitation | △ | — | — | — | — | — |
| Collaborative checklist | — | — | △ | △ | — | — |
| Dice / random game emoji | — | — | ✅ | ✅ | — | — |
| Registered bot game | — | — | ✅ | — | — | — |
| Paid media | — | — | ✅ | △ | — | — |
| Rich blocks / tables / cards | △ | — | ✅ | ? | — | — |
| Share a SimpleX connection card | — | — | — | — | — | ✅ |

### Payload qualifications

- **SX audio/GIF:** transferable as a `file`; there is no separate music-player or animation
  constructor in `MsgContent`. `voice` is a different, supported constructor.
- **WC documents:** the official document-type list is narrower than arbitrary files. Other
  formats may be accepted without a display guarantee. Animated WebP stickers and videos are
  supported, but there is no independent animation/GIF message type.
- **SG albums:** one `DataMessage` can carry repeated attachments. Telegram albums instead return
  several message IDs. **SX batches** create several chat items, not one native album.
- **WM albums, live location, round video, events, quiz and rich cards:** protobuf fields exist,
  but this review did not establish the full lifecycle or reliable client rendering. Do not turn
  schema presence into a production checkmark.
- **TB checklists:** require a connected business account. **TU checklists:** require Premium.
- **TU paid media:** restricted to eligible channels and administrator rights; not a generic
  personal-account payment message. Bot games are registered bot assets, not dice or arbitrary
  files.
- **Contact cards** are shared content, not registered peer identity. Telegram's card requires
  a telephone number; Signal's card and Cloud's richer card need not use the same shape. SX's
  connection card shares a SimpleX link, not a telephone/vCard contact.

## Interactive messages and input requests

| Interaction | WM | WC | TB | TU | SG | SX |
| --- | --- | --- | --- | --- | --- | --- |
| Reply / callback buttons | △ | ✅ | ✅ | — | — | — |
| URL button | △ | ✅ | ✅ | — | — | — |
| Native single-select list modal | △ | ✅ | — | — | — | — |
| Custom reply keyboard | — | — | ✅ | — | — | — |
| Request the user's location | — | ✅ | ✅ | — | — | — |
| Request the user's contact | — | — | ✅ | — | — | — |
| Request a delivery address | — | △ | — | — | — | — |
| Request user / chat selection | — | — | ✅ | — | — | — |
| Copy-to-clipboard button | ? | △ | ✅ | — | — | — |
| Embedded form / application | △ | ✅ | ✅ | — | — | — |
| Call-to-business button | ? | △ | — | — | — | — |
| Product / catalog message | △ | ✅ | — | — | — | — |
| Bot invoice / payment message | ? | △ | ✅ | — | — | — |
| Approved business template | — | ✅ | — | — | — | — |
| Approved carousel template | △ | ✅ | — | — | — | — |
| Profile-level command menu | — | — | ✅ | — | — | ✅ |

### Interaction qualifications

- **WM:** older button/list reports say unsupported; a 2026 upstream issue reports `quick_reply`
  and `cta_url` rendering while `flow` and `single_select` do not. This is contradictory field
  evidence, not a supported contract. Cloud provides the official, documented path.
- **TB:** selection menus can be composed with inline keyboards, but that is not WhatsApp's
  native list modal. A personal MTProto account cannot author bot keyboards simply because
  `reply_markup` exists in a request struct. Forwarding a bot result is not authoring a keyboard.
- **WC copy/payment:** copy-code buttons belong to approved templates; payment/order experiences
  have country/account-specific APIs. Neither is a generic free-form button or universal invoice.
- **WC forms:** WhatsApp Flows reference separately defined Flow assets; they are not arbitrary
  inline JSON forms. Calling needs its own enablement and recipient compatibility.
- **WC address request:** only India-based businesses and India customers at this baseline.
- **SX menus:** `ChatBotCommand` is published through profile/contact/group preferences, not
  attached as per-message callback buttons. Selection sends a command or fills its parameters.

## Message modifiers and control operations

These are not new conversation payload kinds. Editing, reacting, voting, and deleting must not
be confused with sending a new text/media message; ADR 0036 already separates control traffic.

| Modifier / operation | WM | WC | TB | TU | SG | SX |
| --- | --- | --- | --- | --- | --- | --- |
| Reply / quote previous message | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Mention a participant | ✅ | ? | ✅ | ✅ | ✅ | ✅ |
| React / remove reaction | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Edit own message | ✅ | — | ✅ | ✅ | ✅ | ✅ |
| Remote deletion / revoke | ✅ | — | ✅ | ✅ | ✅ | ✅ |
| Forward existing message | ✅ | — | ✅ | ✅ | △ | ✅ |
| Vote in a poll | ✅ | — | — | ✅ | ✅ | — |
| Stop own poll | ? | — | ✅ | ✅ | △ | — |
| Native scheduled delivery | — | — | — | ✅ | — | — |
| Disappearing / timed content | △ | — | △ | △ | ✅ | ✅ |
| View-once media | ✅ | — | — | ✅ | ✅ | — |
| Spoiler text / media | — | — | ✅ | ✅ | ✅ | ✅ |
| Silent notification | — | — | ✅ | ✅ | — | — |
| Prevent saving / forwarding | — | — | ✅ | △ | — | — |
| Incremental / live text | — | — | ✅ | △ | — | ✅ |

### Modifier qualifications

- **Disappearing is not one feature:** WM uses chat expiry context; SG uses an after-read timer;
  SX uses negotiated timed-message support. TB's new ephemeral messages are user-specific group
  messages, not a generic after-read TTL. TU's media TTL is not a universal text TTL.
- **SG forwarding:** the wire has a forwarding flag; it is not Telegram's server-side forward
  operation with the original message reference. **TU protected content** is limited by method,
  sender mode and chat configuration; the ordinary `noforwards` send flag is bot-only.
- **SG stopping polls:** `PollTerminate` exists, but the examined bridge advertises poll end as
  unsupported. **TU incremental text** can use edits; it is not Bot API's ephemeral draft API.
- Spoilers also differ: Telegram supports text entities and media overlays; Signal and SimpleX
  expose concealed text. A single boolean would misrepresent these variants.

## Parameters and source references

Each channel note gives the common send envelope, then the required content fields, optional
fields and restrictions for its supported features. Names below are **upstream wire/SDK names**,
not proposed Botflota fields. These are design-oriented inventories, not copies of every API
parameter; links resolve the complete upstream definitions.

- [WhatsApp native / whatsmeow](whatsmeow.md#outbound-send-contract).
- [WhatsApp Cloud API](whatsappcloud.md#outbound-send-contract).
- [Telegram Bot API and personal MTProto](telegram.md#outbound-send-contract).
- [Signal / signalmeow](signal.md#outbound-send-contract).
- [SimpleX Chat WebSocket](simplex.md#outbound-send-contract).

## Questions this evidence raises for the later abstraction

These are observations to evaluate, not decisions or implementation tasks:

1. Separate payload from presentation and controls: an audio file, a voice note, and a document
   containing audio are not equivalent; reply buttons are not polls; quotes are not new kinds.
2. Keep support at channel **and account-mode** level. A WhatsApp provider-wide flag would lie
   about native buttons; a Telegram provider-wide flag would lie about personal-account keyboards.
3. Model only justified variants. Poll anonymity, quiz answers, selection limits and closing are
   independently supported. Signal's `allowMultiple` cannot encode an arbitrary maximum of three.
4. Media acquisition differs: upload bytes, public URL, provider file ID, or a path visible to
   the auxiliary service. MIME, codec, thumbnail, duration, caption and animation are separate
   requirements. One URL does not describe all five integrations.
5. Preserve semantic results: Cloud button IDs, Telegram callback bytes, poll option indexes and
   SimpleX typed commands have different event and acknowledgement paths. A label is not a value.
6. Some operations return multiple IDs or involve a separate transfer lifecycle. An accepted
   message, a completed file transfer, a rendered UI and recipient delivery are different facts.
7. Keep account assets and policy outside ordinary payloads: approved templates, Flow assets,
   catalog access, business connections, Premium and a 24-hour reply window are not message kinds.

The clean domain/card work remains owned by [the channel plan](../channel-plan.md) and the
canonical ADRs. This research neither replaces those decisions nor authorizes a raw-provider
payload escape hatch or silent text fallback.

[kinds]: ../adr/0036-message-kinds-and-steps.md
[capabilities]: ../adr/0052-declared-channel-capabilities.md
