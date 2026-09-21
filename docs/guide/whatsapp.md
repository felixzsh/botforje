# Pairing and running bots

How to pair one real WhatsApp account, run the bots, and prove the channel
end to end.

## Pair an account

1. Build: `make build`.
2. Configure the bot and validate: `./bin/botflota config validate`.
3. Pair it; this prints an ASCII QR and waits:
   `./bin/botflota bot pair support-bot`
   Add `--scope name` when the bot name repeats across scopes. The QR is
   rendered half-block, so it stays narrow enough for a normal terminal; if
   your font is very large, zoom out or widen the window first. The code
   rotates every ~20 seconds and the screen clears between codes, so the
   visible one is always current. After you scan, `bot pair` reconnects once to
   confirm the companion is online before it exits. If WhatsApp resets a scan,
   `bot pair` automatically starts a fresh QR; keep the command running.
4. On the phone: WhatsApp > Linked devices > Link a device, then scan.
5. `bot pair` prints the paired phone number and exits.

For new pairings, WhatsApp should show the companion as `Botflota`. Existing
links keep their old name until they are unlinked and paired again.

To replace a local pairing, stop the fleet and run
`./bin/botflota bot pair --force support-bot`. This removes the old local device
credentials but keeps sessions and message history.

Botflota allows one bot per WhatsApp phone number. WhatsApp may list several
linked devices, but assigning the same number to two bots can duplicate replies.
The fleet and `bot pair` reject that configuration. To remove one local pairing
while keeping sessions and message history, run
`./bin/botflota bot unpair support-bot`. This does not unlink the old companion
from WhatsApp; remove it from WhatsApp's Linked devices screen if it remains
listed.

## History import

When an account links, WhatsApp pushes the account-wide history sync: the
conversations it chooses, with the recent messages it chooses. Botflota stores
what arrives, without replying:

- Only 1:1 chats are imported. Every message is stored under its kind — text,
  image, video, audio, document, sticker, location, contact, poll, event, an
  answer to buttons, a commerce message, a call (a missed call reads "Missed
  call"), or `unknown` for anything else — so a chat whose history is all media
  still has an anchor for `chat sync`. Groups, broadcasts, status, and channels
  are not stored, and neither are edits, reactions, deletions, or poll votes.
  A conversation registers its sender and chat only when it stored at least one
  message or call, so `chat list` shows conversations rather than the
  control-only shells WhatsApp sends for the account's address book.
- Every imported message is marked `sync`, with `unknown` origin and routing.
  It is never treated as if a person or the bot just wrote it.
- The account's contact names arrive with the sync and name the senders
  Botflota already knows.
- The import does not run the bot and does not reply. The bot filter does not
  apply: a chat outside `allowed_senders` is still stored.
- The sync arrives in chunks. `bot pair` captures what it sees before it
  exits; `fleet run` captures the rest when it connects, so start the fleet
  after pairing if the backlog does not appear at once.
- Set `settings.history_sync: false` on a bot to store none of it.

See which senders arrived with `./bin/botflota chat list <bot>`, then read one
thread with `./bin/botflota message list <bot> <phone>`. `chat list` shows the
stored message total split into `SYNC` and `LIVE`, and the first and last
activity.

## Older messages on demand

WhatsApp only delivers a window of history, so many chats stop at one anchor
message. `chat sync` asks the phone for older messages of one chat:

```bash
./bin/botflota chat sync support-bot 5215500000000
./bin/botflota chat sync --count 50 --timeout 120 support-bot 5215500000000
```

It is best effort, not a guarantee:

- It opens the account's device directly, so **stop `fleet run` first**. The
  command refuses to run when it can see the API answering.
- It requests up to `--count` messages older than the chat's oldest stored
  message and waits up to `--timeout` seconds for the phone to answer.
- A reply is imported like any other sync; a missing reply is reported, not
  retried automatically. Repeat it to page further back.
- WhatsApp, not Botflota, decides whether and how much to send.

## List bots

`./bin/botflota bot list` prints a compact table with the `BOT`, `SCOPE`,
`PHONE`, `STATUS`, and `LAST CHANGE` columns. Status values are `paired`,
`not paired`, or `unpaired`. `PHONE` shows the number only for `paired`;
otherwise it shows `-`. `not paired` means there is no account record;
`unpaired` means the record exists but its local credentials are missing.
`LAST CHANGE` is the last confirmed transition, or `-` when no transition is
recorded. It is shown in local time, using the same format as the logs. Running
`bot list` does not update it.

The timestamp is persisted by pairing, unpair, and fleet operations. It reflects
when Botflota detected the transition, not necessarily when the phone changed it.

## Run the bots

`./bin/botflota fleet run` starts every
paired bot, answers inbound messages with the configured pacing, and stops on
Ctrl-C after draining the outbox.

Bots without a local pairing are not initialized. The fleet logs a
`WARN` naming only `bot pair` and continues with the paired bots; the
command's own usage explains its flags and arguments. If none are paired, the
fleet stays idle until Ctrl-C instead of failing.

## Human-like pacing

Each bot paces replies through its `settings`. For one inbound message:

1. `read_receipt_delay` — wait, then mark the message read when `read_receipts`
   is on.
2. `reply_delay` — wait, showing "typing…" when `simulate_typing` is on.
3. Send the reply.

Between consecutive outbound messages the outbox waits `queue_delay`, so a burst
never leaves as one block. Every delay adds the shared random `jitter` in
`[0, jitter]` ms, drawn again each time; the configured delay is the floor, so
`jitter: 0` makes pacing deterministic.

Delays are milliseconds and per bot; `0` means no added wait. `queue_delay` does
not delay the first message; `reply_delay` does.

## Verification checklist

Deterministic flow (a bot with no `llm`):

- [ ] `bot pair` prints a QR, pairing succeeds, and the device credentials are
  written under `data_dir`.
- [ ] `bot list` reports the bot as `paired` with its phone in the `PHONE`
  column.
- [ ] `fleet run` starts and a real inbound message is answered with the graph's
  messages.
- [ ] After linking, the imported backlog appears as `sync`/`unknown` for the
  senders in the sync, and none of it generated a reply.
- [ ] The contact names arrive and name those senders.
- [ ] `chat list <bot>` lists those senders with the stored total split into
  `SYNC` and `LIVE`, and the first and last activity.
- [ ] With the fleet stopped, `chat sync <bot> <sender>` pages one request of
  older messages back from the phone, or reports that it did not answer.
- [ ] Digits and triggers route as configured; unmatched input takes the
  default edge or the graph fallback.
- [ ] Send the same text from a second device after a history: routing still
  follows the visited nodes.
- [ ] Stop the process and start it again: it connects without a new QR.
- [ ] With `read_receipts` and `simulate_typing` on, the inbound message is
  marked read and a composing indicator shows before the reply.
- [ ] On logout or ban, the fleet stops that bot and reports the state
  instead of retrying forever.
