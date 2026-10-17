# ADR 0040 — One transaction per turn, a durable outbox

Status: Accepted (2026-09)

## Context

The engine wrote the session, the messages, the chat activity, and the guards
one call after another, and replies lived only in an in-memory FIFO. A failure
mid-turn left a session advanced with lost history; a crash lost every queued
reply while their stored rows already claimed to be sent; a redelivered
message could re-run an action (an HTTP side effect included) after its row
existed; and a full inbound queue discarded the user's message with a warning
that nobody reads.

## Decision

- One unit per turn: the engine hands a `botflota.TurnRecord` to a
  `TurnStore`. The SQLite adapter runs the session save, the inbound message,
  every reply, each reply recipient's contact identity, the chat activity, the
  guard state, and the queued replies in one transaction. A reply recipient
  gets an identity only: no chat or inbound interaction is implied. Either the
  whole turn lands or none of it does.
- Deduplicate first: before loading or routing, the engine drops a message
  already recorded in its adapter-declared message space and actual
  destination (ADR 0026), reading the contact and chat without writing them, so
  a redelivery never repeats an action or an HTTP side effect and never moves
  `last_seen`.
- Queue with the turn: every reply is inserted into the `outbox` table in
  the same transaction that stores its message row. Delivery is
  at-least-once: a crash between sending and marking resends on restart, and
  the provider id dedup absorbs the echo.
- Queue entries persist the exact conversation target and the account binding
  generation they were accepted under (ADR 0051). A send rechecks the
  generation, so a replacement account never sends the previous binding's
  pending entries, and a permanent rejection fails immediately instead of
  consuming the retry budget (ADR 0052).
- A per-bot worker drains the queue: it keeps the pacing of ADR 0011
  (`queue_delay`, `jitter`, `max_messages_per_minute`), wakes on each
  committed turn, retries a transient rejection with exponential backoff
  capped at one minute, gives up after a bounded number of attempts (a
  `failed` row stays for diagnosis), and records a successful delivery as
  `sent` with the channel's id on the stored message. A permanent rejection
  fails the entry immediately, and an uncertain outcome — typically a send
  timeout after the provider may have accepted the message — is retried and
  recorded as unconfirmed rather than as a proven non-delivery (ADR 0052).
- The queue is strictly FIFO: the oldest pending reply heads it and waits
  out its retry there, so a later reply never overtakes it, and a shutdown
  leaves a scheduled retry to the same account's next run. Unlinking or
  replacing a link fails its queued replies with the reason, so a new
  account never sends the old link's replies. Unlink finishes only a send
  already in progress; it does not drain the remaining queue.
- Inbound applies backpressure: a full channel queue blocks the whatsmeow
  event handler until the bot actor takes the message; it never discards
  (ADR 0011 promised bounded queues, not drops).
  A shared-source adapter keeps buffers bounded too, but may couple consumers'
  progress. A wait on a shared source is cancelable, and releasing one logical
  view frees its own waits without closing a peer's view. It must expose the
  actual replay/backpressure boundary rather than silently drop traffic or
  promise isolation that the source cannot provide (ADR 0052). Additional
  durable buffering is not approved by this rule, and Botflota promises no
  losslessness for a source that cannot replay.
- Receive confirmation follows an agreed processing outcome, not queue handoff.
  ADR 0004 owns the closed outcome contract: committed, already-recorded
  duplicate, deliberate discard, and failed processing. A failed persistence is
  never successful confirmation, and a cumulative cursor never advances past a
  failed entry. Provider update IDs/cursors do not become persisted message
  identities (ADR 0026).
- Stored timestamps are fixed-width RFC 3339 UTC, so ordering the stored
  text is chronological: the retry schedule, chat activity, and session
  expiry all compare as text.

## Consequences

- A failed turn leaves no partial state among what it commits, so history
  and sessions can be trusted without reconciliation. The inbound contact and
  current chat are prepared outside it; a failed turn can leave them behind.
  Alternate reply-recipient identities are prepared inside the transaction,
  so they do not survive a failed turn.
- A reply to an alternate recipient also registers that number as a contact,
  in the same transaction as the reply. A failed turn leaves no orphan
  recipient identity, and the identity alone does not count as interaction.
- Replies survive restarts, `fleet status` reports the queue's real depth,
  and a permanently failing reply becomes a visible `failed` row instead of
  a log line that scrolls away.
- Delivery is at-least-once, not exactly-once: when a channel cannot report
  its message id, a crash after sending can produce one duplicate, absorbed
  by inbound dedup wherever the provider echoes the message.
- Shutdown drains the stored queue with its pacing, so SIGTERM still waits
  out `queue_delay` between the remaining sends.
- The engine no longer writes message, chat, or guard rows directly. A new
  write that belongs to a turn belongs in `TurnRecord`; anything else needs
  its own port and contract — the API-origin send is `SendStore.CommitSend`
  (ADR 0044).
