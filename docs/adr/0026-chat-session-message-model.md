# ADR 0026 — Chat, session, and message model

Status: Accepted (2026-09)

## Context

Persistence must keep every message exchanged with each person, while the
engine keeps only a bounded, resumable conversation state. The two are not the
same thing: the state starts and ends, the record does not. The first schema
conflated them. It had a `conversations` table with no unique key and no
lifecycle, the word "conversation" meant both the state and the record, and
messages had no link to the session run that handled them.

## Decision

Three concepts, three tables:

- **Chat** — the durable thread between one bot and one sender, unique per
  `(scope, bot, sender)`. It never ends and owns the history.
- **Session** — one bounded run of the graph for a chat. It starts at the root
  and ends on the graph timeout (or `default_timeout`). A chat has many over
  time.
- **Message** — one inbound or outbound message. It belongs to a chat and may
  reference the session run that handled it.

Schema (`adapters/sqlite`):

- `chats (id, scope, bot, sender_id -> senders, started_at, updated_at,
  message_count, guards)`, unique `(scope, bot, sender_id)`; `guards` is the
  per-action guard state (ADR 0027); `sender_id` and the `senders` table come
  from ADR 0029.
- `sessions (id, chat_id -> chats, node, visited_nodes, graph_context,
  node_context, created_at, updated_at, expires_at)`; the newest row per chat
  is the active state.
- `messages (id, chat_id -> chats, session_id -> sessions NULL, direction,
  kind, body, sent_at, provider_message_id, ingest_kind, origin, routing)`.
  `provider_message_id` is the channel's own id, unique per chat when present,
  and the dedup key of a history import. `ingest_kind` says how the row entered
  Botflota (`live`, `sync`, `api`, `unknown`); `origin` who produced an
  outbound body (`preset`, `llm`, `operator`, `unknown`, empty for inbound);
  `routing` how the engine chose the transition (`trigger_exact`,
  `trigger_sequence`, `trigger_words`, `trigger_fuzzy`, `system_one`,
  `default`, `fallback`, `root`, `unknown`).

Foreign keys: deleting a chat cascades to its sessions and messages; deleting a
session run sets `messages.session_id` to NULL, so history outlives the run.

"Conversation" is retired as a data-model term: the durable entity is a chat,
the bounded state is a session. The word still describes dialogue in prose.

## Consequences

- Every message is kept regardless of session expiry or reset, which is the
  point. Session runs are kept as history and can be pruned later if needed.
- The engine resolves a chat (get-or-create) and then the chat's newest
  session; an expired session starts a new run instead of overwriting.
- The store ports mirror the tables: `ChatStore`, `SessionStore`, and
  `MessageStore` (root package `botflota`).
- Message recording happens in the engine as messages flow: the inbound
  message and every reply are appended under the chat and the session run that
  handled them. Listing and tailing a chat arrive with the CLI and API.
- Provenance is recorded, never inferred: an imported outbound message says
  `sync` and `unknown`, never that a human wrote it, and only a body the model
  really produced is `llm`; a failed realization that keeps the anchor stays
  `preset`.
- Pre-release, the schema is edited in place (ADR 0006), so an existing
  development database is recreated with `make dev-reset`.
