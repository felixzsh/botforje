# Delivery and receipts

What this documents: what a channel can report about an outbound message after
it was accepted, and how that maps to failure handling in the outbox. Ranges from
"accepted only" to sent, delivered, read or played, plus errors that arrive after
acceptance. Also covers the operational limits that shape retry: rate limits,
send or reply windows, file quotas, cooldowns, blocks and temporary
restrictions.

Record per channel/mode: which states exist, how they correlate to the original
message, what is knowable at all, and which failures are permanent, transient or
an uncertain timeout. Acceptance is never delivery (ADR 0040, ADR 0052).
