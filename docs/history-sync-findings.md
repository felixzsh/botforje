# History sync findings

Context for the next attempt, not a plan. The file is intentionally left
uncommitted so a `git reset --hard f91198c15a75` keeps it as untracked work.

## Expected end state

- `bot pair` plus `fleet run` imports the phone's full history for every
  chat of every bot by default, with no extra flags.
- A per-chat backfill walks backwards in rounds until a chat stops growing,
  so stored messages approach what the phone shows.
- Reruns are safe: `provider_message_id` deduplicates every import.

## Protocol facts verified

- Linking a companion triggers history chunks: INITIAL_BOOTSTRAP, RECENT,
  FULL, PUSH_NAME, NON_BLOCKING_DATA, ON_DEMAND. The server decides the
  window it sends.
- The companion advertises `DeviceProps.HistorySyncConfig` and
  `RequireFullSync` at registration; the phone reads them only then.
  `RequireFullSync=true` is Baileys' `syncFullHistory`. whatsmeow defaults
  to false, so a stock pair only gets a recent window.
- History notifications arrive as protocol messages. With
  `ManualHistorySyncDownload=true` the app must download and import each
  notification itself; whatsmeow stops dispatching `events.HistorySync`.
- Per-chat paging uses a `HISTORY_SYNC_ON_DEMAND` peer request built with
  `BuildHistorySyncRequest(anchor, 50)` and sent with `SendPeerMessage` to
  our own JID; the chat travels inside the request. The anchor is the
  oldest stored message: id, from-me, timestamp, chat JID. Without a
  non-nil anchor whatsmeow dereferences it, and a keyless request gets no
  answer from a real phone.
- Correlation is `SendResponse.ID` == `HistorySyncNotification.
  PeerDataRequestSessionID`. A blob may carry metadata for many
  conversations; import all of them, but only that id answers this chat.
- Chunk end: `progress >= 100` or `EndOfHistoryTransferType` in {1,2,3}.
  There is no explicit "no more pages" marker, so page until the oldest
  message stops moving.
- 50 messages is the recommended maximum per request; older history needs
  repeated rounds (Maxar needed two rounds after pairing).
- LID addressing: current DMs are often `@lid`. Resolve PN to LID from the
  device store (`GetLIDForPN`) and address the anchor to the LID. The
  request also has `accountLid` and `supportInlineResponse` fields.
- `OldestMsgTimestampMS` unit is ambiguous: whatsmeow sends seconds,
  Baileys sends milliseconds. Anchored requests worked with seconds.
- Calls can anchor: embedded `CallLogMessage` entries and top-level
  `CallLogRecords` both carry provider ids.
- The phone must be online with WhatsApp open. "Chat history: Paused" on
  the phone does not block on-demand requests.

## Things that did not work

- Anchorless ON_DEMAND (empty `oldestMsgID`, now timestamp, LID or PN):
  the phone never answered. mautrix-whatsapp also requires an anchor.
- `FULL_HISTORY_SYNC_ON_DEMAND` (type 6) replied
  `ERROR_HOSTED_DEVICE_NOT_CONNECTED`. Those response codes belong to the
  hosted/BSP device feature; a real SMB Android primary cannot use it.
- Re-pairing does not guarantee a specific chat. Maxar only got data after
  a pairing delivered its call entries; other chats still arrive as shells.

## Why some chats stay empty

- A conversation can arrive with entries that all lack a provider id or are
  control-only (protocol, reactions, edits). The chat row is created, no
  message is stored, and `chat sync` has no anchor to page from.
- The initial window is server-decided and varies per pairing, so a chat
  visible on the phone can be absent from the sync entirely.
- The older text-only importer (87c0144) stored text and calls only; it
  could not store more than a full-kind importer. Empty chats are a
  delivery/anchor problem, not an importer regression.

## Operations

- One companion connection at a time: stop `fleet run` before `chat sync`.
- Re-pairing creates a new companion device; `--force` replaces only the
  local device and account rows, never chats or messages.
- A companion can be unlinked server-side; its next connect fails with an
  immediate websocket EOF and the instance must be paired again.

## Useful references

- mautrix-whatsapp `pkg/connector/backfill.go` (`FetchMessages`,
  `fetchMessagesFromPhone`): canonical anchor-based paging.
- whatsapp-cli "history extend" proposal (vicentereig/whatsapp-cli#20):
  `--until-stable` rounds; on-demand works despite "history paused".
- Baileys `fetchMessageHistory` and `syncFullHistory`.
- whatsmeow discussions #654 and #759; DeepWiki history sync page.
- Protos: `waHistorySync.HistorySync`, `waE2E.HistorySyncNotification`,
  `waE2E.HistorySyncOnDemandRequest`.
