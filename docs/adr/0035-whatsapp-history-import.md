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
- Only 1:1 chats and text messages are imported. Groups, broadcasts, status,
  channels, media, reactions, polls, edits, and deletions are skipped.
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
- Older messages beyond the delivered backlog are not requested automatically.
  A bounded, explicit `chat sync <bot> <sender>` asks the primary device for
  one page of messages older than the chat's oldest known message
  (`BuildHistorySyncRequest`) and imports the `ON_DEMAND` reply with the same
  importer. It opens the device directly, so the fleet must be stopped, and a
  missing reply is a reported outcome, not a silent success. Automatic paging
  stays out of scope until the phone is shown to answer reliably.

## Consequences

- A pairing can populate the database with many senders and chats at once,
  each with the messages WhatsApp delivered. Listing a chat starts from real
  history, and `message_count` still counts live inbound only (ADR 0029).
- The import owns only what WhatsApp sends. It cannot promise completeness;
  `chat sync` pages back one bounded request at a time, and `RequireFullSync`
  stays out of scope because the phone, not Botflota, decides what to send.
  The reference to what is missing is the conversation's end-of-transfer
  marker.
- Deduplication is in the store, not in memory: a redelivered chunk, a
  restart, or an echoed reply all collapse through the unique index on
  `(chat_id, provider_message_id)`.
- The importer runs on a bounded worker, so a large bootstrap never blocks the
  channel event loop or reconnect; chunks dropped by a full queue are the
  recoverable case the next connection covers.
- A large import can write a lot of personal data. `history_sync: false` is
  the switch for hosts that do not want the backlog.
