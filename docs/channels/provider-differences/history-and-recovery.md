# History and recovery

What this documents: the two separate ways a channel surfaces messages that were
not received live. **History import** is reading and storing prior conversations
that already exist. **Recovery** is receiving traffic that arrived while the bot
was disconnected, plus redelivery and the acknowledgement that processing
committed. Both differ per channel and account mode.

Record per channel/mode: what can be enumerated, how far back, pagination and
anchors, whether attachments remain available, whether a walk resumes after
restart, and how duplicates are prevented against live traffic. An imported
entry stays inert and never runs the engine; recovery feeds real turns and needs
its own commit boundary (ADR 0035, ADR 0040, ADR 0051).
