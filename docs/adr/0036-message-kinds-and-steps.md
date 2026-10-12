# ADR 0036 — Message kinds, typed content, and outbound steps

Status: Accepted (2026-09)

## Context

WhatsApp carries far more than text: images, video, audio, documents,
stickers, locations, contacts, polls, events, commerce messages, call logs,
and answers to interactive messages. The history import of ADR 0035 kept only
text and calls, so a chat whose entries were all media had no anchor to page
back from, and the engine could not tell one non-text message from another.

The wire format is one channel's vocabulary, not the product's. Copying
whatsmeow's protobuf into the domain would tie every other channel and the
config DSL to one library's field names, and most of those fields are
wrappers, key-distribution traffic, edits, or future placeholders rather than
content.

A flat outbound message with one optional pointer per kind does not scale
either: it grows with every kind and admits impossible states, such as a poll
kind carrying a media pointer.

## Decision

Keep one channel-neutral kind vocabulary in the core, carry each message's
payload as typed content, and let each channel implement the subset it can.

- `MessageKind` names the payload: `text`, `image`, `video`, `audio`,
  `document`, `sticker`, `location`, `contact`, `poll`, `event`,
  `interactive`, `commerce`, `call`, and `unknown`.
- `MessageContent` is a sealed interface — `MessageKind()`, `VisibleText()`,
  and an unexported marker — implemented by one concrete type per kind:
  `TextContent`, `MediaContent`, `LocationContent`, `ContactContent`,
  `PollContent`, `EventContent`, `InteractiveContent`, `CommerceContent`,
  `CallContent`, and `UnknownContent`. `VisibleText` is the text a reader
  shows and the engine routes on; it may be empty for a binary payload.
- `SendableContent` is the subset an action may emit: text, media, location,
  contact, and poll. A kind the engine only reads from a channel — a call log,
  an event, a commerce message, an unrecognized payload — is a
  `MessageContent` but not a `SendableContent`, so the compiler rejects an
  attempt to send it.
- `IncomingMessage` and `OutgoingMessage` carry `Content`; the engine and the
  channel switch over the content type instead of reading a kind beside
  optional fields, so a kind and its payload cannot disagree.
- Import and live traffic share one classifier. A recognized kind is stored;
  `unknown` is the fallback for content a channel cannot name, so the message
  still has an anchor for `chat sync` (ADR 0035). A control message — an edit,
  reaction, deletion, poll vote, pin, keep, or key-distribution traffic — is
  never stored.
- Live traffic delivers every recognized kind to the engine; an unclassifiable
  live payload is dropped instead of becoming a conversation turn, because it
  has no text to route on. The import keeps it as an anchor.
- An action may emit `message` (text), `location`, `media` (image, video,
  audio, document, sticker from a templated `url` or a local `path`),
  `contact`, and `poll`. A media step names exactly one source; the channel
  reads the bytes, so a large file or a secret stays out of the runtime
  context.
- A `contact` step carries a neutral card: a display name plus reachability
  entries whose `kind` (`phone`, `email`, `address`, `url`) is a payload label
  shared code never parses. Each definition declares which entry kinds it can
  compose, config validation refuses the rest for the consuming bot, and the
  adapter builds its own native card object (vCard for WhatsApp) inside the
  adapter (ADR 0052).
- A stored message keeps its kind and visible text as derived columns for
  filtering and search, and its structured payload as `content` JSON, so a
  poll's options or a pin's coordinates survive the store.
- A channel that cannot deliver a content type answers
  `botflota.ErrUnsupportedKind` rather than dropping the message silently. The
  channel reads remote media with a bounded size, so a large file or a slow URL
  cannot exhaust the process.

## Consequences

- `chat sync` can page back from any chat that holds at least one content
  message with a channel id, not only a text or call row.
- The read API and `message list` show the kind beside the provenance, and the
  API serves the structured `content`, so a reader can tell an image from a
  call, and a dashboard can render a poll or a map without parsing text.
- Sending media needs the live connection to upload the bytes; the engine and
  the outbound record stay channel-free.
- A second channel implements the content types it supports and rejects the
  rest with one sentinel error; no core type names a wire field.
- A card variant one adapter cannot compose fails validation for the bots that
  would send it and passes for the adapters that declare it, so no shared type
  gains a telephone field to keep a card working (ADR 0052, ADR 0054).
- Adding a kind means adding one content type, not one more optional field on
  every message.
- Rows written before the `content` column existed decode from their kind and
  visible text, so a pre-release database keeps working.
