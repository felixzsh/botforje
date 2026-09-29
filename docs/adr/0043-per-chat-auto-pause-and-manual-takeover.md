# ADR 0043 — Per-chat pause on an external reply, manual takeover

Status: Accepted (2026-09)

## Context

A bot's linked account is also the operator's WhatsApp account: the operator
replies from the phone, and later from the dashboard, while the bot keeps
answering. Nothing notices today. The whatsmeow adapter drops every message
with `IsFromMe`, so a reply typed on the phone is invisible to Botflota, and
the bot may answer over it a moment later. Manual takeover is planned
(ROADMAP Phase 12), but only as an explicit `auto`/`human` switch with no
automatic trigger.

The unit that matters is the chat, not the bot: the operator steps into one
conversation, and every other chat should keep being answered. The pause is
ephemeral by nature — it belongs to a conversation the runtime is handling
right now, not to stored history.

## Decision

- **A chat, not a bot.** Control state is per chat `(scope, bot, sender
  phone)`. Pausing one chat never pauses another and never stops the bot.

- **Two ways in.** A live message that leaves the linked account without
  Botflota sending it hands the chat to the operator and starts a pause; an
  explicit `human` control from the API holds the chat indefinitely until it
  is released. Both share one state; the automatic pause is a countdown, the
  manual switch is not.

- **One pause length per bot.** `settings.external_reply_pause` is the
  milliseconds a chat stays paused after an external reply; `0` or absent uses
  the 15-minute default. Every chat of the bot shares it. A new external reply
  restarts the countdown. The value is read when the reply arrives, so
  changing it never moves a countdown already running.

- **A manual hold outranks the countdown.** An external reply finds the chat
  already held by hand and renews only the countdown, leaving the hold standing,
  so a reply from the phone cannot hand back a chat an operator is keeping.
  Releasing the hold returns the chat to automatic, and an explicit `auto`
  cancels a countdown still running: the caller asked for the bot to answer.

- **In memory, not in the database.** Control state lives in the bot actor. A
  restart forgets every pause and every manual hold: replies already cancelled
  stay cancelled in the queue, and the next inbound message is answered normally
  even though the window it arrived inside had not run out. Persisting the state
  would buy only that restart window and is deliberately out of scope.

- **The inbound is kept, the graph is not run.** While a chat is held, an
  inbound message is stored for history and deduplication, but no transition,
  no reply, and no session change happens: the person who wrote is still
  recorded as having written, and the graph stays where it was. When the hold
  ends, the next inbound message is handled normally; nothing received during
  the hold is replayed.

- **Handing over cancels what the bot still owes that chat.** Pending replies
  of that chat are failed with a reason, in one statement, before the manual
  hold takes effect, and the outbox worker rechecks a reply right before
  sending it, so a reply read before the hand-over is not sent after. A send
  already in progress cannot be recalled. The queue of the bot's other chats
  is untouched.

- **An external reply is recorded, not routed.** A live outbound message that
  Botflota did not send is stored under its chat with the operator origin, so
  history and the dashboard show it. The history sync is not a trigger: the
  past cannot say who replied, and a chunk arriving at link time must not pause
  anything.

- **Correlation is the channel's job.** The adapter knows the ids it sent, so
  it can tell its own echo from the operator's reply, and it pre-generates the
  id of an outbound message so a reply is marked before it can be echoed back.
  This covers every send Botflota performs, including an API-origin message
  (ADR 0044); none of them ever looks like an operator reply. Fleet sees only
  observations that are not the bot's own. The set of sent ids is bounded, so
  the correlation holds within a window of recent sends rather than forever;
  the echo arrives long before that window could matter.

- **The API exposes the control, not the timer.** A scoped principal may set a
  chat to `human` or `auto`, and reads the effective state in the chat list.
  Starting or renewing the automatic pause needs no route. A chat that was never
  written to does not exist yet, so holding it is not offered: the automatic
  hand-over covers the case where the operator replies first.

## Consequences

- Botflota and the operator no longer talk over each other in the same chat;
  the operator keeps every other chat answered.
- The pause is best-effort around a send already in flight; it is exact for
  everything still queued.
- A restart is a clean slate for control: acceptable because the pause is
  short-lived, and honest because it is documented here rather than a stored
  row that outlives its purpose.
- `external_reply_pause` is a bot setting that adopts live; it is not in the
  restart-required set, and `fleet.refresh` picks it up with the engine.
- A later phase that persists takeover, or adds the manual reply path, builds
  on this state instead of changing it (ROADMAP Phase 12).
