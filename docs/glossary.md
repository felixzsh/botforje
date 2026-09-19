# Glossary

- Bot — one WhatsApp account bound to a graph, its settings, and optional
  ldm and llm; the deployable unit.
- Fleet — the set of bots a single Botflota instance runs.
- Scope — the top-level config namespace that isolates entities, references,
  and sessions (ADR 0016); a free name, never derived from folders.
- Graph — conversation state machine owned by one bot; may override the
  routing mode of the bot's ldm (ADR 0015).
- Node — a state: goal, policy, entry/exit actions, tools, and optional
  node-local context.
- Goal — the LLM's objective in a node, short and clear.
- Edge — transition between nodes with a required user intent, except the
  default (catch-all) edge; triggers is an optional deterministic fast path.
- Intent — on an edge, the user trigger; on a message, the reply's goal.
  Short, clear text (not snake_case), like goal.
- Action — reusable pipeline of steps (`message`, `request`, `location`)
  with guards and optional declared inputs.
- Tool — an action a node exposes to the LLM, invoked with validated
  arguments (`inputs`).
- Input — named, typed argument of a tool; may bind to a node or graph
  variable to read and write it.
- Node context — node variables; reset when the node is entered again.
- Graph context — session-wide variables; persist across nodes.
- Visited nodes — ordered list of nodes visited in a session, current last;
  used for routing and as LLM context (ADR 0010).
- Tool loop — bounded sequence of tool calls and replies the LLM runs for one
  incoming message inside a node (ADR 0008).
- Step — single operation inside an action.
- Guard — rule that limits how often an action runs per sender, such as a
  cooldown (minimum interval) or a rate limit (maximum per window).
- Policy — constraints for LLM decisions, tool selection, and generated text.
  Node policy is ambient; message policy refines it.
- Chat — durable thread between one bot and one sender, keyed by
  (scope, bot, sender); it never ends and gathers every message (ADR 0026).
- Session — one bounded run of a bot's graph for a chat, stored in SQLite; it
  starts at the root and ends on the graph timeout (ADR 0026).
- PN (phone number) — WhatsApp's phone-number address; Botflota keys sessions
  on the plain number (`5215500000000`), without the JID suffix (ADR 0020).
- LID (hidden user) — WhatsApp's `user@lid` address; confined to the channel
  adapter for resolution and delivery (ADR 0020).
- Sender identity — the resolved phone number in plain digits, resolved by the
  channel adapter (ADR 0020).
- Sender — the durable profile of one person who writes to bots in a scope:
  their phone, push name, and first/last seen. One per (scope, phone), shared by
  the scope's bots; the bot-person thread is the chat (ADR 0029).
- Messaging provider — the service end users are on (WhatsApp today); one
  provider is reached through one or more channels.
- Channel — one implementation that reaches a messaging provider. WhatsApp
  has several: whatsmeow today, other clients or the official Cloud API
  later.
- Linked account — a bot's identity on its messaging provider: for WhatsApp,
  the JID that identifies the paired companion device; keyed by (scope, bot).
  Pairing is the lifecycle around it: paired, not paired, or unpaired
  (ADR 0006).
- Shared catalog — the instance-level `ldms` and `llms` a host provides, in
  the main config file only; a bot resolves `ldm` and `llm` in its scope first
  and then in `shared`, which is not a scope (ADR 0031).
- LDM (Large Decision Model) — a model class that answers typed questions with
  calibrated confidence instead of generating text; the project's main AI
  path. Jev (TypeSafe System One) is the first implementation (ADR 0030).
- LLM — a named model profile (provider, model, credentials, realization
  defaults, bounds); a bot references one with `llm` or replies from
  prescribed bodies without it (ADR 0017, ADR 0031).
- Agent — a conventional external agent runtime (for example OpenClaw), a
  possible future integration behind a port. It does not name Botflota's own
  LLM behavior; that is the tool loop.
- Filter — one bot's inbound rules (ignore groups, allow/ignore senders)
  applied before the engine; the fleet routes events to the bot.
- Outbox — per-bot FIFO queue that paces outbound messages.
- Secret reference — a `${ENV_VAR}` placeholder in config, resolved from the
  process environment at load and masked in logs (ADR 0014).
- Template — a string with `{{path.to.value}}` references resolved against the
  runtime context; a missing path is an error (ADR 0018).
- Runtime context — the values a template reads: graph and node variables,
  sender, message, bot, tool inputs, and request results (ADR 0018).
- Device store — the whatsmeow state that keeps a companion device linked: its
  identity keys, prekeys, registration, and LID map. It lives in SQLite, so a bot
  can run on a server (ADR 0006, ADR 0028).
- Passkey — a WebAuthn credential bound to one device; WhatsApp may require it to
  link a companion or to open a connection (ADR 0028).
- Pairing code — the eight-digit code a phone types in WhatsApp to link a
  companion without a QR code (ADR 0028).
- Shortcake — Meta's internal name for the passkey companion-linking flow
  (ADR 0028).
