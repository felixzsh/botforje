# Glossary

- Bot — one WhatsApp account bound to a graph, its settings, and optional
  system_one and llm; the deployable unit.
- Fleet — the set of bots a single Botflota instance runs.
- Scope — the top-level config namespace that isolates entities, references,
  and sessions; its YAML key is its stable identifier, never derived from
  folders.
- Display name — optional `display_name` presentation label on a scope or
  entity. Interfaces fall back to its YAML key; it never changes identity.
- Graph — conversation state machine owned by one bot; may override the
  routing mode of the bot's system_one.
- Node — a state: goal, policy, entry/exit actions, tools, and optional
  node-local context.
- Goal — the LLM's objective in a node, short and clear.
- Edge — transition between nodes with a required user intent, except the
  default (catch-all) edge; triggers is an optional deterministic fast path.
- Intent — on an edge, the user trigger; on a message, the reply's goal.
  Short, clear text (not snake_case), like goal.
- Action — reusable pipeline of steps (`message`, `request`, `location`,
  `media`, `contact`, `poll`) with guards and optional declared inputs.
- Tool — an action a node exposes to the LLM, invoked with validated
  arguments (`inputs`).
- Input — named, typed argument of a tool; may bind to a node or graph
  variable to read and write it.
- Node context — node variables; reset when the node is entered again.
- Graph context — session-wide variables; persist across nodes.
- Visited nodes — ordered list of nodes visited in a session, current last;
  used for routing and as LLM context.
- Tool pipeline — the default tool mode: the LLM extracts candidate
  parameters from the message, System One selects tools and maps values to
  real catalog entries, and code validates and applies.
- Tool loop — the LLM-driven tool mode, the alternative to the tool pipeline
  for nodes a pipeline cannot bound.
- Step — single operation inside an action.
- Guard — rule that limits how often an action runs per sender, such as a
  cooldown (minimum interval) or a rate limit (maximum per window).
- Policy — constraints for LLM decisions, tool selection, and generated text.
  Node policy is ambient; message policy refines it.
- Message kind — the channel-neutral payload class of a message: text, image,
  video, audio, document, sticker, location, contact, poll, event, interactive,
  commerce, call, or unknown. A channel implements the subset it can produce
  and send (ADR 0036).
- Chat — durable thread between one bot and one sender, keyed by
  (scope, bot, sender); it never ends and gathers every message.
- Session — one bounded run of a bot's graph for a chat, stored in the
  database; it starts at the root and ends on the graph timeout.
- PN (phone number) — WhatsApp's phone-number address; Botflota keys sessions
  on the plain number (`5215500000000`), without the JID suffix.
- LID (hidden user) — WhatsApp's `user@lid` address; kept internal to
  WhatsApp handling for resolution and delivery.
- Sender identity — the resolved phone number in plain digits, resolved by the
  WhatsApp channel.
- Sender — the durable profile of one person who writes to bots in a scope:
  their phone, push name, and first/last seen. One per (scope, phone), shared by
  the scope's bots; the bot-person thread is the chat.
- Contact — an address-book entry of one bot's linked account, keyed by
  (scope, bot, phone), copied only when the bot sets `import_contacts`. It is
  an identity the account knows, never a chat or a sender (ADR 0037).
- Messaging provider — the service end users are on (WhatsApp today); one
  provider is reached through one or more channels.
- Channel — one implementation that reaches a messaging provider. WhatsApp
  has several: the native client today, other clients or the official Cloud API
  later.
- Linked account — a bot's identity on its messaging provider: for WhatsApp,
  the JID that identifies the paired companion device; keyed by (scope, bot).
  Pairing is the lifecycle around it: paired, not paired, or unpaired.
- Shared catalog — the instance-level `system_one` and `llms` a host provides, in
  the main config file only; a bot resolves `system_one` and `llm` in its scope first
  and then in `shared`, which is not a scope.
- System One model (SOM) — a model class that answers typed questions with calibrated
  confidence instead of generating text; the project's main AI path. Its three
  question types are `choice` (pick one option), `score` (rate on a rubric)
  and `noul` (probability that a statement is true). Jev (TypeSafe) is the
  first implementation.
- LLM — a named model profile (provider, model, credentials, realization
  defaults, bounds); a bot references one with `llm` or replies from
  prescribed bodies without it.
- Agent — a conventional external agent runtime (for example OpenClaw), a
  possible future integration. It does not name Botflota's own LLM behavior;
  that is the tool loop.
- Filter — one bot's inbound rules (ignore groups, allow/ignore senders)
  applied before the engine; the fleet routes events to the bot.
- Outbox — per-bot FIFO queue that paces outbound messages.
- Webhook — an instance-level signed HTTP delivery of an outbound event to an
  external system; the first event is `sender.first_seen` (ADR 0045).
- Secret reference — a `${ENV_VAR}` placeholder, allowed only in fields enabled
  for secrets (model `api_key`, request header values), resolved from the
  process environment at load; the whole string is sensitive.
- Template — a string with `{{path.to.value}}` references resolved against the
  runtime context; a missing path is an error.
- Runtime context — the values a template reads: graph and node variables,
  sender, message, bot, tool inputs, and request results.
- Device store — the WhatsApp device credentials that keep a companion device
  linked: its identity keys, prekeys, and registration. It lives in the
  database, so a bot can run on a server.
- Passkey — a WebAuthn credential bound to one device; WhatsApp may require it to
  link a companion or to open a connection.
- Pairing code — the eight-digit code a phone types in WhatsApp to link a
  companion without a QR code.
- Owner token — the instance owner's credential, created on first run and
  written once to `owner.token` under `data_dir` (mode 0600). It authorizes
  every scope, instance settings, and credential management; it is the only
  credential that covers the whole instance.
- Access token — an opaque random credential for the API, stored hashed,
  optionally expiring, and revocable. A scoped access token names the scopes
  it may use and both reads and changes them; it sees nothing else, and a
  scoped principal requesting another scope gets 404, never 403. It has full
  authority over its own scopes, and whether two access tokens may hold the
  same scope is the instance's choice (`exclusive_scopes`; docs/api.md).
- Limit set — an owner-managed policy joining several scopes under shared
  ceilings: bots, running bots, graphs, actions, model profiles, outbound
  messages per anchored UTC monthly period, and stored message bytes. The
  period starts at provisioning or when the first scope joins an empty set.
  It is not a scope, a UI group, or a token, and it carries no ceiling on how
  many scopes it holds; the owner provisions those (`docs/access-policy.md`).
- Structural limit — how large one definition may grow: nodes per graph,
  edges per node, steps and inputs per action. An instance setting, a
  positive integer and restart-required, that belongs to no limit set and
  never multiplies by scope.
- Stored message bytes — the logical size of stored message content: the
  UTF-8 length of `body` and `content` over a set's scopes, inbound,
  outbound, and imported alike. It is not the size of the database file.
- History import ceiling — `settings.history_import_max_bytes`, the bytes of
  imported history one bot keeps for its linked account. Ordinary bot config
  in bytes; Botflota knows no tier and no percentage.
- History migration — moving, never copying, a bot's chats, messages, and
  contacts to another bot of the same scope, so a recreated or peer bot starts
  from the same conversations; it runs with both bots stopped
  (`docs/current-plan.md`). Unlinking an account never deletes history.
