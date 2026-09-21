# ADR 0037 — WhatsApp contact import

Status: Accepted (2026-09)

## Context

Linking a companion gives WhatsApp an account identity, and the account carries
an address book. WhatsApp syncs that book to the companion through app-state
sync, and the library keeps it in its own contact store. Botflota ignored it:
the history importer only applied the `PUSH_NAME` chunk, which names senders
already known, so a number the account knows but never wrote to was invisible.

The book is not a conversation. A contact may be a person this account never
messaged, and it can overlap with the address book of a personal account linked
on the same phone, because both apps read the phone's contacts. Importing it
into `senders` and creating a chat per entry would invent threads that do not
exist and would put people into the conversation model who never wrote.

WhatsApp also sends a conversation shell for many of those numbers: a
conversation with a single control entry and no message. The history importer
created a sender and a chat for it, so a fresh pairing produced dozens of empty
chats.

## Decision

Keep contacts and chats apart, and let a bot opt in:

- `settings.import_contacts` is a per-bot switch, off by default. It is per
  bot, not per scope, because the address book belongs to the linked account:
  two bots of one scope may link accounts with different books.
- An imported contact is its own record, keyed by `(scope, bot, phone)`, and is
  never a sender, a chat, or a message. The address-book name, the push name,
  and the verified business name live in separate fields and never clear each
  other; a reader shows the address-book name first, then the business name,
  then the push name.
- The import reads the channel's contact data. A full address-book sync is
  applied silently by the library unless events are requested, so the channel
  asks for those events while the import is on, and also copies the cached book
  once per connection, which covers a snapshot that was already applied and a
  switch flipped after pairing.
- The import stores the phone number. A group, broadcast, or unresolved hidden
  user is skipped, like the history import (ADR 0020).
- `bot pair` and `fleet run` both import, like the history sync: whichever
  connection is alive sees the events (ADR 0035).
- A conversation whose entries are all control traffic registers nothing: no
  sender, no chat, no row. It carries no message, and WhatsApp sends one for
  every number the book knows.
- `contact list` and the scope `contacts` route read the imported book. An
  empty result is the honest answer for a bot that never turned the import on.

## Consequences

- A bot that wants a CRM-style address book sets `import_contacts: true`; a bot
  that wants only real threads leaves it off and keeps the store free of
  personal data it did not ask for.
- The address book can hold personal data of people who never wrote to the bot.
  The switch is the control, and retention follows the scope's rules like any
  other record.
- A chat now appears only when at least one message or call was stored, so
  `chat list` shows conversations rather than address-book shells. A chat whose
  every entry is an unrecognized payload still appears, because the import
  keeps it as `unknown` (ADR 0036).
- Contacts do not take part in the engine, the filter, or routing. They are
  read-only data for the API and the dashboard.
- The library's own contact store is unaffected: it stays the channel's cache,
  whether or not Botflota copies from it.
