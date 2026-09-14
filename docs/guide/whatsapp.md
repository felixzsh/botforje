# Pairing and manual test (whatsmeow)

How to pair one real WhatsApp account, run the bots, and prove the channel
end to end.

## Pair an account

1. Build: `make build`.
2. Configure the bot and validate: `./bin/botforje validate`.
3. Pair it; this prints an ASCII QR and waits:
   `./bin/botforje auth support-bot`
   Add `--scope name` when the bot name repeats across scopes. The QR is
   rendered half-block, so it stays narrow enough for a normal terminal; if
   your font is very large, zoom out or widen the window first. The code
   rotates every ~20 seconds and the screen clears between codes, so the
   visible one is always current. After you scan, `auth` reconnects once to
   confirm the companion is online before it exits.
4. On the phone: WhatsApp > Linked devices > Link a device, then scan.
5. `auth` prints the paired JID and exits.

## Check status

`./bin/botforje status` lists every configured bot as `paired as <jid>` or
`not paired`.

## Run the bots

`./bin/botforje daemon` (or `make dev ARGS="daemon"`) starts every configured
bot, answers inbound messages with the configured pacing, and stops on Ctrl-C
after draining the outbox.

## Manual checklist

Deterministic flow (a bot with no `llm`):

- [ ] `auth` prints a QR, pairing succeeds, and the device store is written
  under `data_dir`.
- [ ] `status` reports the bot as `paired as <jid>`.
- [ ] `daemon` starts and a real inbound message is answered with the graph's
  messages.
- [ ] Digits and triggers route as configured; unmatched input takes the
  default edge or the graph fallback.
- [ ] Send the same text from a second device after a history: routing still
  follows node memory (ADR 0010).
- [ ] Stop the process and start it again: it connects without a new QR.
- [ ] With `read_receipts` and `simulate_typing` on, the inbound message is
  marked read and a composing indicator shows before the reply.
- [ ] On logout or ban, the daemon stops that bot and reports the state
  instead of retrying forever.
