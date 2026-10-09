# External messages

What this documents: every outbound message the bot's graph did not produce, and
how its origin is told apart. Three cases stay distinct: the channel's own echo
of a Botflota send, a message a person sends from another client or device, and a
send Botflota itself issued through the API (`origin=api` or `operator`). The
echo must not trigger a handoff, the person must pause automation, and the API
send is an explicit origin, not a human reply (ADR 0043, ADR 0044).

Record per channel/mode: whether outside sends are visible at all, what
identification they carry, whether they arrive live or only in history, and how
they behave across reconnect or restart.
