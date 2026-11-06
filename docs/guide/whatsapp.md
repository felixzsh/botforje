# Binding and running bots

How to bind one real WhatsApp account, run the bots, and prove the channel
end to end.

## Bind an account

1. Build: `make build`.
2. Configure the bot and validate: `./bin/botflota config validate`.
3. Bind it; the WhatsApp `qr` method prints an ASCII QR and waits:
   `./bin/botflota bot bind support-bot`
   Add `--scope name` when the bot name repeats across scopes. The QR is
   rendered half-block, so it stays narrow enough for a normal terminal; if
   your font is very large, zoom out or widen the window first. WhatsApp
   rotates the challenge, and the command re-renders the current one, so keep
   it running.
   When the phone is easier than a scan, ask for the pairing code instead:
   `./bin/botflota bot bind --method code --param phone=<international number> support-bot`.
   The number is the account's own, in international form; `+`, spaces, and
   dashes are ignored and a national leading zero is rejected. The command
   prints the pairing code and the instruction to type it on that phone, then
   keeps running while the phone finishes the link; `qr` stays the default
   method.
4. On the phone: WhatsApp > Linked devices > Link a device, then scan; with the
   pairing code, choose Link with phone number, enter the code the command
   printed, and confirm the passkey step there — the phone performs it, and
   Botflota only receives the link.
5. `bot bind` prints the bound identity (`provider/subject`) and exits. Add
   `--verbose` to also print the structured log at the configured `log_level`.

For a new QR binding, WhatsApp shows the companion as `Botflota`, or as the
bot's `channel.options.device_name` when it sets one. Existing links keep their
old name until they are unbound and bound again. A pairing-code link is
different: the code registration declares its own platform descriptor
(`Chrome (Linux)`), which the provider validates against a closed set of client
and platform pairs, so `device_name` does not name a code link.

To replace a binding, run `./bin/botflota bot bind --force support-bot`; it
detaches the committed binding first and removes the same things `bot unbind`
does: the binding row, the pairing status, and the adapter's device row
with its keys, sessions, app-state cache, hidden-user map, contact cache, and
message secrets. Botflota's own peers, chats, messages, graph runs, and
directory are never touched.

Two things to expect. The phone keeps listing the old companion until you remove
it in WhatsApp > Linked devices, so a rebind adds a device rather than
replacing one. And a fresh registration is the only way to change what the
registration carries — the requested history window above all — because WhatsApp
reads it once, at linking time.

A pairing-code setup prints the code as its display step, and the session
stays `awaiting_approval` while the owner types it; the code expires on its
own, and the setup session reports its own expiry as a state instead of waiting
for it. If the provider asks the companion itself for the passkey during a code
link, the session says so and the phone can still finish that step. A QR link
has no such phone: for that method the session states that the account cannot
be linked from here. That is a coverage limit, not a retry loop (ADR 0028).

`bot bind` and `bot unbind` adapt to a running instance: while `fleet run` owns
the instance both drive its API — the QR comes from the setup session, the bot
joins the fleet as soon as setup commits, and an unbind stops the bot before
its credentials go — and both work locally under the instance lock when the
fleet is stopped. One phone number keeps one companion connection at a time,
which is why replacing a binding is `--force`, whichever way the instance is
running.

Botflota allows one bot per WhatsApp phone number. WhatsApp may list several
linked devices, but assigning the same number to two bots can duplicate replies.
The fleet and `bot bind` reject that configuration. To remove one binding
while keeping sessions and message history, run
`./bin/botflota bot unbind support-bot`. This does not unlink the old companion
from WhatsApp; remove it from WhatsApp's Linked devices screen if it remains
listed.

## History import

When an account links, WhatsApp pushes the account-wide history sync: the
conversations it chooses, with the messages it chooses. Botflota stores what
arrives, without replying. The binding connection is the only one that sees
this sync, so `bot bind` always imports it while it waits.

`--param full_history=true` is what decides how much arrives:

- With it, the companion registers asking for the full window
  (`RequireFullSync` plus a ten-year, 10 GB limit) and the command waits until
  that transfer completes, the chunks go idle, or a ten-minute budget runs out.
  This is the mode that brings chats older than the default window: on a real
  account it took one binding from 13 to 184 conversations.
- Without it, the companion registers like a plain client and only the default
  recent window arrives. `chat sync` pages older messages per chat afterwards.

The account remains the authority: it decides what it sends, and the window is
read only while registering, so changing it needs a new registration
(`--force`).

- Only 1:1 chats are imported. Every message is stored under its kind — text,
  image, video, audio, document, sticker, location, contact, poll, event, an
  answer to buttons, a commerce message, a call (a missed call reads "Missed
  call"), or `unknown` for anything else — so a chat whose history is all media
  still has an anchor for `chat sync`. Groups, broadcasts, status, and channels
  are not stored, and neither are edits, reactions, deletions, or poll votes.
  A conversation registers its peer and chat only when it stored at least one
  message or call, so `chat list` shows conversations rather than the
  control-only shells WhatsApp sends for the account's directory.
- Every imported message is marked `sync`, with `unknown` origin and routing.
  It is never treated as if a person or the bot just wrote it.
- The account's directory names arrive with the sync and name the peers
  Botflota already knows.
- The import does not run the bot and does not reply. The bot filter does not
  apply: a chat outside `allowed_peers` is still stored.
- The sync arrives in chunks. `bot bind` captures what it sees before it
  exits; `fleet run` captures the rest when it connects, so start the fleet
  after binding if the backlog does not appear at once. A sync delivered to a
  connection nobody captured is gone: the server sends it once.
- Set `channel.options.history_sync: false` on a bot to store none of it.

See which peers arrived with `./bin/botflota chat list <bot>`, then read one
thread with `./bin/botflota message list <bot> --chat-id <id>`. `chat list`
shows the stored message total split into `SYNC` and `LIVE`, and the first and
last activity.

## Older messages on demand

WhatsApp only delivers a window of history, so many chats stop at one anchor
message. `chat sync` asks the account for older messages of one chat:

```bash
./bin/botflota chat sync support-bot --chat-id 3
./bin/botflota chat sync --count 50 --timeout 120 support-bot --chat-id 3
```

To walk every chat instead of one, add `--all`; the command takes only the bot
and goes through the chats newest first, one at a time, printing a line per
chat and a total:

```bash
./bin/botflota chat sync --all support-bot
```

```
syncing 184 chat(s) through the fleet...
3        imported 52 (duplicate 0, skipped 2) [nothing older]
7        imported 18 (duplicate 1, skipped 0) [no more access]
...
synced 184 chat(s), imported 1204 (duplicate 31, skipped 12), 0 failed
```

A chat that fails is reported and skipped, so one unreachable chat does not stop
the walk, and running it again is cheap: a chat with nothing older answers at
once.

It is best effort, not a guarantee:

- It runs on the live connection of `fleet run`, so **start the fleet first**;
  one phone number keeps one companion connection at a time.
- It requests up to `--count` messages older than the chat's oldest stored
  message and waits up to `--timeout` seconds for the account to answer.
- A reply is imported like any other sync; a missing reply is reported, not
  retried automatically. Repeat it to page further back.
- WhatsApp, not Botflota, decides whether and how much to send.

## Directory

A directory is not a conversation. WhatsApp Business knows every number in
the phone's directory, including people you only talk to from a personal
account on the same phone, and it sends a control-only conversation shell for
each of them. Those shells are skipped, so they never appear as chats.

To copy the directory anyway, opt in per bot:

```yaml
bots:
  support-bot:
    graph: "support"
    channel:
      provider: "whatsapp"
      mode: "native"
      options:
        import_contacts: true
```

The import stores one entry per account identity, keyed by bot, and never
creates a chat or a peer interaction:

```bash
./bin/botflota directory list support-bot
./bin/botflota directory list --limit 500 support-bot
```

The name shown is the directory name, falling back to the verified business
name and then the push name. The import runs at `bot bind` and at `fleet run`,
like the history sync; a bot that never sets `import_contacts` answers with an
empty list.

## List bots

`./bin/botflota bot list` prints a compact table with the `BOT`, `SCOPE`,
`BINDING`, and `STATE` columns. `BINDING` shows `provider/subject` for a
committed binding and `-` otherwise. `STATE` is the running channel's transport
state — `connected`, `connecting`, `stopped`, `logged_out`, `banned`,
`passkey_required`, or `reachout_timelock` — or `-` when the bot is configured
but not running. A rejection no retry can fix stops the bot in its own state
instead of looping: `passkey_required` means the account needs the passkey
ceremony on the owner's device, and `reachout_timelock` means the provider
locked new conversations for now (ADR 0028). `bot list` reads the running
instance through the REST API, so `fleet run` must be up and the command
changes no stored state.

## Run the bots

`./bin/botflota fleet run` starts every bound bot, answers inbound messages
with the configured pacing, and stops on Ctrl-C after draining the outbox.

Bots without a committed binding are not initialized. The fleet logs a
`WARN` for each one (`bot not ready; skipping it`, with its scope, bot, and
state) and continues with the bound bots. If none are bound, the fleet stays
idle until Ctrl-C instead of failing.

## Human-like pacing

Each bot paces replies through its shared settings and its channel options. For
one inbound message:

1. `channel.options.read_receipt_delay` — wait, then mark the message read when
   `channel.options.read_receipts` is on.
2. `channel.options.reply_delay` — wait, showing "typing…" when
   `channel.options.simulate_typing` is on.
3. Send the reply.

Between consecutive outbound messages the outbox waits `settings.queue_delay`,
so a burst never leaves as one block. Every delay adds the shared random
`settings.jitter` in `[0, jitter]` ms, drawn again each time; the configured
delay is the floor, so `jitter: 0` makes pacing deterministic.

Delays are milliseconds and per bot; `0` means no added wait. `queue_delay` does
not delay the first message; `reply_delay` does.

## Verification checklist

Deterministic flow (a bot with no `llm`):

- [ ] `bot bind` prints a QR, binding succeeds, and the device credentials are
  written under `data_dir`.
- [ ] `bot bind --method code --param phone=<number>` prints the eight-digit
  code, the session reports `awaiting_approval` while the phone is used, and
  the same binding lands once the code is entered.
- [ ] `fleet run` starts and a real inbound message is answered with the graph's
  messages.
- [ ] With the fleet running, `bot list` reports the bot's binding
  (`provider/subject`) and its transport state.
- [ ] After linking, the imported backlog appears as `sync`/`unknown` for the
  peers in the sync, and none of it generated a reply.
- [ ] The directory names arrive and name those peers.
- [ ] `chat list <bot>` lists those peers with the stored total split into
  `SYNC` and `LIVE`, and the first and last activity.
- [ ] With the fleet running, `chat sync <bot> --chat-id <id>` pages one request
  of older messages back from the account, or reports that it did not answer.
- [ ] Digits and triggers route as configured; unmatched input takes the
  default edge or the graph fallback.
- [ ] Send the same text from a second device after a history: routing still
  follows the visited nodes.
- [ ] Stop the process and start it again: it connects without a new QR.
- [ ] With `read_receipts` and `simulate_typing` on, the inbound message is
  marked read and a composing indicator shows before the reply.
- [ ] On logout or ban, the fleet stops that bot and reports the state
  instead of retrying forever.
