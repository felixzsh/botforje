# ADR 0037 — WhatsApp address-book import

Status: Accepted (2026-09)

## Context

Linking a companion gives WhatsApp an account identity, and the account carries
an address book. WhatsApp syncs that book to the companion through app-state
sync, and the library keeps it in its own contact store. Botflota ignored it:
the history importer only applied the `PUSH_NAME` chunk, which names contacts
already known, so a number the account knows but never wrote to was invisible.

The book is not a conversation. An entry may be a person this account never
messaged, and it can overlap with the address book of a personal account linked
on the same phone, because both apps read the phone's contacts. Creating a chat
per entry would invent threads that do not exist and put people into the
conversation model who never wrote; registering the identity without a thread
does not, and it is what makes the directory complete.

WhatsApp also sends a conversation shell for many of those numbers: a
conversation with a single control entry and no message. The history importer
created a contact and a chat for it, so a fresh pairing produced dozens of empty
chats.

## Decision

Let a bot opt in, and keep the book a relation over the scope's directory:

- `settings.import_contacts` is a per-bot switch, off by default. It is per
  bot, not per scope, because the address book belongs to the linked account:
  two bots of one scope may link accounts with different books.
- An imported entry is its own record, keyed by the bot and the contact's
  subject in `bot_contacts` (ADR 0051; for WhatsApp the subject is the phone),
  and is never a chat or a message. A contact-list event (or a
  cached entry with a first/full name) creates membership. Push-name and
  business-name events only update an existing member; names learned from a
  message do not claim that the phone is in the address book. A cached contact
  without a first/full name cannot prove membership and is not marked imported.
- The address-book name, push name, and verified business name stay in separate
  fields and never clear each other; a reader shows the address-book name
  first, then the business name, then the push name. A known push name fills
  the shared contact display name only when it is otherwise empty.
- Importing registers each entry's subject as a scope contact through
  `ContactStore.EnsureContact`, so the directory holds the people the account
  knows. The identity carries no first message: `first_seen` stays empty until
  the person actually writes (ADR 0029). Membership in a bot's book is derived
  from `bot_contacts` by subject, and the contact read exposes it as
  `address_book_imported`, beside `has_chat` and `has_history` (ADR 0044).
- The import reads the channel's contact data. A full address-book sync is
  applied silently by the library unless events are requested, so the channel
  asks for those events while the import is on, and also copies the cached book
  once per connection, which covers a snapshot that was already applied and a
  switch flipped after pairing.
- The import stores WhatsApp's subject, the phone number. A group, broadcast,
  or unresolved hidden user is skipped, like the history import (ADR 0020).
- `bot pair` and `fleet run` both import, like the history sync: whichever
  connection is alive sees the events (ADR 0035).
- A conversation whose entries are all control traffic registers nothing: no
  contact, no chat, no row. It carries no message, and WhatsApp sends one for
  every number the book knows.
- `address-book list` and the scope `/bots/{bot}/contacts` route read the
  imported book. An empty result is the honest answer for a bot that never
  turned the import on.
- Importing the book never claims a contact's `first_seen`; only a live inbound
  message does, so a number known only from the book still fires the
  first-contact hook when it first writes (ADR 0029, ADR 0045).
- `address_book_imported` records that an address-book import supplied the
  identity; it is not a promise that the linked account still has the phone
  saved. WhatsApp removals are not currently applied to the imported table.

## Consequences

- A bot that wants a CRM-style address book sets `import_contacts: true`; a bot
  that wants only real threads leaves it off and keeps its store free of the
  names it did not ask for. Importing still registers identities in the shared
  scope directory, which is the price of a complete contact read.
- The import can register personal data of people who never wrote to the bot.
  The switch is the control, and retention follows the scope's rules like any
  other record.
- A chat now appears only when at least one message or call was stored, so
  `chat list` shows conversations rather than address-book shells. A chat whose
  every entry is an unrecognized payload still appears, because the import
  keeps it as `unknown` (ADR 0036).
- A registered identity takes part in nothing by itself: no chat, no routing,
  no first-contact event. It is read-only data for the API and the dashboard
  until a real message arrives.
- The library's own contact store is unaffected: it stays the channel's cache,
  whether or not Botflota copies from it.
