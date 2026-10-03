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
  every reply, the chat activity, the guard state, and the queued replies in
  one transaction. Either the whole turn lands or none of it does.
- Deduplicate first: before loading or routing, the engine drops a message
  whose provider id the chat already stores, reading the contact and chat
  without writing them, so a redelivery never repeats an action or an HTTP
  side effect and never moves `last_seen`.
- Queue with the turn: every reply is inserted into the `outbox` table in
  the same transaction that stores its message row. Delivery is
  at-least-once: a crash between sending and marking resends on restart, and
  the provider id dedup absorbs the echo.
- A per-bot worker drains the queue: it keeps the pacing of ADR 0011
  (`queue_delay`, `jitter`, `max_messages_per_minute`), wakes on each
  committed turn, retries a rejected send with exponential backoff capped at
  one minute, gives up after a bounded number of attempts (a `failed` row
  stays for diagnosis), and records a successful delivery as `sent` with the
  channel's id on the stored message.
- The queue is strictly FIFO: the oldest pending reply heads it and waits
  out its retry there, so a later reply never overtakes it, and a shutdown
  leaves a scheduled retry to the same account's next run. Unlinking or
  replacing a link fails its queued replies with the reason, so a new
  account never sends the old link's replies. Unlink finishes only a send
  already in progress; it does not drain the remaining queue.
- Inbound applies backpressure: a full channel queue blocks the whatsmeow
  event handler until the bot actor takes the message; it never discards
  (ADR 0011 promised bounded queues, not drops).
- Stored timestamps are fixed-width RFC 3339 UTC, so ordering the stored
  text is chronological: the retry schedule, chat activity, and session
  expiry all compare as text.

## Consequences

- A failed turn leaves no partial state among what it commits, so history
  and sessions can be trusted without reconciliation. The contact profile and
  the chat are prepared before that transaction, outside it: a failed turn
  can leave a contact and an empty chat, never a half-written session,
  message, guard, or queued reply.
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
