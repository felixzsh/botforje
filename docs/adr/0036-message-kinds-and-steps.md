# ADR 0036 — Message kinds and outbound message steps

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

## Decision

Keep one channel-neutral kind vocabulary in the core, and let each channel
implement the subset it can.

- `MessageKind` names the payload: `text`, `image`, `video`, `audio`,
  `document`, `sticker`, `location`, `contact`, `poll`, `event`,
  `interactive`, `commerce`, `call`, and `unknown`. An inbound message carries
  its kind and its visible text (caption, file name, question, name), never the
  attachment bytes.
- Import and live traffic share one classifier. A recognized kind is stored;
  `unknown` is the fallback for content a channel cannot name, so the message
  still has an anchor for `chat sync` (ADR 0035). A control message — an edit,
  reaction, deletion, poll vote, pin, keep, or key-distribution traffic — is
  never stored.
- Live traffic delivers every recognized kind to the engine; an unclassifiable
  live payload is dropped instead of becoming a conversation turn, because it
  has no text to route on. The import keeps it as an anchor.
- An action may emit the kinds a bot can send: `message` (text), `location`,
  `media` (image, video, audio, document, sticker from a templated `url` or a
  local `path`), `contact`, and `poll`. A media step names exactly one source;
  the channel reads the bytes, so a large file or a secret stays out of the
  runtime context.
- A channel that cannot deliver a kind answers `botflota.ErrUnsupportedKind`
  rather than dropping the message silently. The channel reads remote media
  with a bounded size, so a large file or a slow URL cannot exhaust the
  process.
- Numbers, dates, and file contents are never computed by a model; the kind
  only classifies, and the engine still renders templates and checks secrets
  as before (ADR 0014, ADR 0018).

## Consequences

- `chat sync` can page back from any chat that holds at least one content
  message with a channel id, not only a text or call row.
- The read API and `message list` show the kind beside the provenance, so a
  reader can tell an image from a call without a text body.
- Sending media needs the live connection to upload the bytes; the engine and
  the outbound record stay channel-free, and only the caption or file name is
  persisted.
- A second channel implements the kinds it supports and rejects the rest with
  one sentinel error; no core type names a wire field.
- The stored rows for media, polls, and the like hold no content, only an
  anchor and visible text. Retention and export still treat them as message
  rows.
