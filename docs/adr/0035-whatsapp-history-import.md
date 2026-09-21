# ADR 0035 — WhatsApp history import

Status: Accepted (2026-09)

## Context

Linking a companion to WhatsApp triggers an account-wide history sync: the
server pushes chunks with conversations for every chat it chooses, each with
the messages it chooses (recent activity, a limited number per chat). It is
not per sender, it is not the whole thread, and it arrives once, on whichever
connection is alive right after linking. If Botflota does not capture it, it
is gone.

A bot that only records live traffic starts every conversation blind, even one
the sender has been having with the phone for months. Keeping the delivered
backlog gives the dashboard and the future LLM context something honest to
read, and it costs the engine nothing: the sync is data, not a conversation
turn.

## Decision

Import the history sync, inertly and honestly:

- The channel listens for `HistorySync` in both places a connection lives:
  the pairing session (`bot pair`) and the running fleet (`fleet run`). Both
  use the same importer, because the server may deliver different chunks to
  each connection.
- A pairing always imports what WhatsApp delivers while its connection is
  alive, because the server sends the account-wide sync once, to whichever
  connection is up right after linking.
- `bot pair --import-history` additionally advertises a Desktop companion with
  `RequireFullSync=true` and a wide window (`full_sync_days_limit`,
  `full_sync_size_mb_limit`, `storage_quota_mb`), overriding only those fields
  so the other capability flags keep the defaults the library ships, and keeps
  the connection alive until that transfer reaches progress 100, until chunks
  stop arriving for a tolerant idle gap, or until a long wait budget of its
  own, longer than the QR session, runs out. A recent or bootstrap chunk
  completing early does not end the wait, because the full transfer follows it.
  Without the flag the companion registers like a plain client, only the
  default recent window arrives, and `chat sync` pages older messages per chat.
  WhatsApp reads the window only while registering, so an existing link needs a
  new registration to change it. The phone still decides the actual window and
  may deliver less.
- Only 1:1 chats are imported. Every content message is stored under its
  channel-neutral kind (ADR 0036): text, image, video, audio, document,
  sticker, location, contact, poll, event, interactive, commerce, call, and an
  `unknown` fallback. A control message — an edit, reaction, deletion, poll
  vote, pin, keep, or key-distribution traffic — stores no row. Groups,
  broadcasts, status, and channels are skipped. A conversation registers its
  sender and chat only when it stored at least one message or call: WhatsApp
  sends a control-only shell for every number the account's address book knows,
  and a chat with no message is not history (ADR 0037).
- The account-wide `PUSH_NAME` chunk names the senders Botflota already knows,
  without creating a profile for a contact that never wrote and without moving
  first or last seen. A name learned this way is remembered for a sender a
  later chunk creates.
- Importing a conversation widens the chat's activity to cover what arrived:
  `started_at` moves back to the oldest imported message and `updated_at`
  forward to the newest, never the other way, so newer live activity is left
  alone. The live inbound `message_count` of ADR 0029 is not touched.
- Each imported message is stored as `sync`, with `origin` and `routing`
  `unknown` (origin empty for inbound), and no session run (`session_id = 0`).
  The past cannot say who wrote an outbound message outside Botflota, so the
  import never guesses.
- The import is inert: it never runs the engine, queues a reply, or touches
  the outbox. The bot's filter (`ignore_groups`, `allowed_senders`,
  `ignored_senders`) does not apply, because the import is not a conversation.
- The conversation's chat phone number resolves as usual (ADR 0020): the
  conversation's phone JID first, then the LID store. A chat with no known
  phone number is skipped.
- `provider_message_id` is the dedup key, unique per chat. `Append` reports a
  new `botflota.ErrDuplicate` sentinel when the id is already present; the
  importer counts it as already imported instead of failing or aborting the
  sync.
- A live outbound reply is recorded before it is sent, and the channel returns
  its own message id after delivery. The fleet writes that id back onto the
  stored row, so a later sync that echoes the reply deduplicates it too.
- `settings.history_sync` disables the import for a bot; absent means on.
- Older messages beyond the delivered backlog are requested by `chat sync
  <bot> <sender>`, the path a pairing without `--import-history` relies on. It
  pages backwards from the chat's oldest stored message,
  asking the primary device for batches of up to 50 messages older than it
  (`BuildHistorySyncRequest`) and importing every correlated `ON_DEMAND` chunk
  with the same importer. The next cursor is the oldest message the phone
  returned, so paging stops when a page carries nothing older or the phone
  reports no access. Only a stored message is a valid anchor: a control entry
  and a call record carry a provider id but no position in the chat's own
  message list, so the phone cannot page from them. The store never returns a
  call row as the oldest anchor, and a chat that arrived with only calls or
  control entries reports that it has no anchor instead of a silent zero.
- `chat sync` runs over the live connection of the running fleet, not over a
  second connection, because one phone number has one companion connection at
  a time. The fleet exposes the operation through `botflota.HistorySyncer` and
  `api` serves it as `POST /api/v1/scopes/{scope}/chats/sync?bot=&sender=`,
  so the CLI is an API client and a dashboard asks for a backfill the same
  way. `?count=N` limits the total requested entries and zero means all
  accessible history; one sync runs per bot at a time. A missing reply is a
  reported outcome, not a silent success.
- A sync request never uses a bootstrap chunk's download path: the running
  fleet keeps importing every chunk, and the request only observes the chunks
  that carry its own `PeerDataRequestSessionID`.

## Consequences

- A pairing can populate the database with many senders and chats at once,
  each with the messages WhatsApp delivered. Listing a chat starts from real
  history, and `message_count` still counts live inbound only (ADR 0029). A
  chat appears only when it stored at least one message or call. A chat with no
  row is not listed and cannot be synced on demand until a real message lands
  in it.
- The import owns only what WhatsApp sends. It cannot promise completeness;
  `chat sync` asks in recommended batches until the cursor stops moving or the
  phone reports no access. `RequireFullSync` increases the requested initial
  window, but the phone still decides what to send. The reference to what is
  missing is the conversation's end-of-transfer marker.
- Deduplication is in the store, not in memory: a redelivered chunk, a
  restart, or an echoed reply all collapse through the unique index on
  `(chat_id, provider_message_id)`.
- The importer runs on a bounded worker, so a large bootstrap never blocks the
  channel event loop; the queue applies backpressure instead of dropping a
  chunk, and the worker drains what is queued before the adapter stops.
- A large import can write a lot of personal data. `history_sync: false` is
  the switch for hosts that do not want the backlog.
