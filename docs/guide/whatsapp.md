# Pairing and manual test (whatsmeow)

How to pair one real WhatsApp account, run the bots, and prove the channel
end to end.

## Pair an account

1. Build: `make build`.
2. Configure the bot and validate: `./bin/botforje config validate`.
3. Pair it; this prints an ASCII QR and waits:
   `./bin/botforje bot pair support-bot`
   Add `--scope name` when the bot name repeats across scopes. The QR is
   rendered half-block, so it stays narrow enough for a normal terminal; if
   your font is very large, zoom out or widen the window first. The code
   rotates every ~20 seconds and the screen clears between codes, so the
   visible one is always current. After you scan, `bot pair` reconnects once to
   confirm the companion is online before it exits. If WhatsApp resets a scan,
   `bot pair` automatically starts a fresh QR; keep the command running.
4. On the phone: WhatsApp > Linked devices > Link a device, then scan.
5. `bot pair` prints the paired phone number and exits.

For new pairings, WhatsApp should show the companion as `BotForje`. Existing
links keep their old name until they are unlinked and paired again.

To replace a local pairing, stop the fleet and run
`./bin/botforje bot pair --force support-bot`. This removes the old local device
credentials but keeps sessions and message history.

BotForje allows one bot per WhatsApp phone number. WhatsApp may list several
linked devices, but assigning the same number to two bots can duplicate replies.
The fleet and `bot pair` reject that configuration. To remove one local pairing
while keeping sessions and message history, run
`./bin/botforje bot unpair support-bot`. This does not unlink the old companion
from WhatsApp; remove it from WhatsApp's Linked devices screen if it remains
listed.

## List bots

`./bin/botforje bot list` prints a compact table with the `BOT`, `SCOPE`,
`PHONE`, `STATUS`, and `LAST CHANGE` columns. Status values are `paired`,
`not paired`, or `unpaired`. `PHONE` shows the number only for `paired`;
otherwise it shows `-`. `not paired` means there is no account record;
`unpaired` means the record exists but its local credentials are missing.
`LAST CHANGE` is the last confirmed transition, or `-` when no transition is
recorded. It is shown in local time, using the same format as the logs. Running
`bot list` does not update it.

The timestamp is persisted by pairing, unpair, and fleet operations. It reflects
when BotForje detected the transition, not necessarily when the phone changed it.

## Run the bots

`./bin/botforje fleet run` (or `make dev ARGS="fleet run"`) starts every
paired bot, answers inbound messages with the configured pacing, and stops on
Ctrl-C after draining the outbox.

Bots without a local pairing are not initialized (ADR 0022). The fleet logs a
`WARN` naming only `bot pair` and continues with the paired bots (ADR 0023); the
command's own usage explains its flags and arguments. If none are paired, the
fleet stays idle until Ctrl-C instead of failing.

## Manual checklist

Deterministic flow (a bot with no `llm`):

- [ ] `bot pair` prints a QR, pairing succeeds, and the device store is written
  under `data_dir`.
- [ ] `bot list` reports the bot as `paired` with its phone in the `PHONE`
  column.
- [ ] `fleet run` starts and a real inbound message is answered with the graph's
  messages.
- [ ] Digits and triggers route as configured; unmatched input takes the
  default edge or the graph fallback.
- [ ] Send the same text from a second device after a history: routing still
  follows node memory (ADR 0010).
- [ ] Stop the process and start it again: it connects without a new QR.
- [ ] With `read_receipts` and `simulate_typing` on, the inbound message is
  marked read and a composing indicator shows before the reply.
- [ ] On logout or ban, the fleet stops that bot and reports the state
  instead of retrying forever.
