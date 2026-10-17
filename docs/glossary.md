# Glossary

- Bot — one channel account bound to a graph, its settings, and optional
  system_one and llm; the deployable unit.
- Fleet — the set of bots a single Botflota instance runs.
- Scope — the top-level config namespace that isolates entities, references,
  and graph runs; its YAML key is its stable identifier, never derived from
  folders.
- File root — an optional, owner-managed directory in the main config file that
  bounds the local files one scope's steps may read; a scope without one uses
  the process's own file permissions (ADR 0047). It bounds local reads only and
  is not a sandbox.
- Egress policy — an optional, owner-managed instance setting for outbound HTTP.
  `public_only` lets a call reach only a public destination, checked on the
  connection it actually makes; the default `open` filters nothing (ADR 0048).
- Secret catalog — an optional, owner-managed file per scope, declared in the
  main config file and kept outside every file root. A scope with one resolves
  its `${NAME}` references only from that file, with no fallback to the process
  environment (ADR 0049).
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
- Graph context — graph-run-wide variables; persist across nodes.
- Visited nodes — ordered list of nodes visited in a session, current last;
  used for routing and as LLM context.
- Tool pipeline — the default tool mode: the LLM extracts candidate
  parameters from the message, System One selects tools and maps values to
  real catalog entries, and code validates and applies.
- Tool loop — the LLM-driven tool mode, the alternative to the tool pipeline
  for nodes a pipeline cannot bound.
- Step — single operation inside an action.
- Guard — rule that limits how often an action runs per contact, such as a
  cooldown (minimum interval) or a rate limit (maximum per window).
- Policy — constraints for LLM decisions, tool selection, and generated text.
  Node policy is ambient; message policy refines it.
- Message kind — the channel-neutral payload class of a message: text, image,
  video, audio, document, sticker, location, contact card (`contact`), poll,
  event, interactive, commerce, call, or unknown. A channel implements the
  subset it can produce and send (ADR 0036).
- Chat — durable conversation thread between one bot and one conversation,
  keyed by (scope, bot, provider, kind, thread, topic) and never ends. A direct
  thread may name a participant; a shared one does not (ADR 0051).
- Graph run (`GraphRun`; currently `Session`) — one bounded execution of a
  bot's graph for a chat, stored in the database; it starts at the root and
  ends on the graph timeout. Collections use `GraphRuns`, never bare `Runs`;
  bind attempts and SDK sessions are separate concepts.
- PN (phone number) — WhatsApp's phone-number address; for WhatsApp it is the
  peer's subject and the direct conversation's thread
  (`5215500000000`), without the JID suffix.
- LID (hidden user) — WhatsApp's `user@lid` address; kept internal to
  WhatsApp handling for resolution and delivery.
- Peer identity — the key a messaging channel maps its own
  addresses to: the provider and an opaque subject. When a provider's ids are
  only meaningful inside one store or profile, the adapter composes that
  space's stable identifier into the subject (ADR 0051).
- Peer (the scope-level `Peer` record) — a durable messaging identity
  a scope knows, keyed by (scope, provider, subject) and shared by its bots.
  Knowing a peer does not require any prior message exchange: a directory
  import or an API send may register the identity. Neither counts as live
  inbound activity; `first_seen` belongs to the first allowed live inbound,
  never to an import or an API send. Identity, directory membership, and the
  chat's interaction history are distinct (ADR 0029, ADR 0051).
- Directory entry (the bot-level `DirectoryEntry` record) — a recorded contact-list
  membership of a bot's bound account, as exposed by its channel. It may
  exist without any prior inbound or outbound message. It is not a peer's
  interaction history or a chat, and does not guarantee current reachability
  or permission to send. Importing it may register a scope peer without
  creating a chat or triggering first-seen (ADR 0037, ADR 0051). Reading or
  importing the upstream directory is an optional channel capability, separate
  from its persistence through `DirectoryStore`; access to the complete address
  book of a physical device is not assumed. A retained or migrated snapshot keeps
  its source account identity; its current data owner need not be the account
  that imported it. Moving it never establishes current membership (ADR 0042).
- Contact card — a message payload that shares someone's contact information,
  such as a name, phone, email, or both. The implemented payload type is
  `ContactContent` and the declared step is `spec.Contact`; the selected target
  names `ContactCardContent` and `spec.ContactCard` are still pending and the
  catalog owns that rename. The person described need not be a registered peer
  or a directory member; sharing the card does not register them, add
  membership, or create a chat with them. A `ContactEntry` is one
  kind/value/label inside the card, not a `DirectoryEntry`. Its phone or email
  value is opaque payload data, not a messaging identity or routing
  instruction. The implemented Go name is `KindContact`; the serialized kind
  remains `contact`, and each adapter supports only the variants it can
  represent. See [the terminology catalog](channels/domain-terminology.md).
- Messaging provider — the service end users are on (WhatsApp today); one
  provider is reached through one or more channels.
- Channel — one implementation that reaches a messaging provider. WhatsApp
  has several: the native client today, other clients or the official Cloud API
  later.
- Binding (the `Binding` record) — the fixed verified association of a
  scoped bot with its normalized own messaging identity: provider, adapter
  kind, account mode, stable identity-space context, normalized own
  `IdentityKey`, and generation, with commit times. Its SDK device/session
  locator and credentials are separate and adapter-owned. Renewing the same
  identity space keeps its generation; an unrelated replacement advances it
  (ADR 0051, ADR 0053). A temporary backend-context selection does not
  change the association.
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
- Filter — one bot's inbound rules (refuse shared conversations, allow/ignore peers)
  applied before the engine; the fleet routes events to the bot.
- Outbox — per-bot FIFO queue that paces outbound messages.
- Webhook — an instance-level signed HTTP delivery of an outbound event to an
  external system; the first event is `peer.first_seen` (ADR 0045).
- Secret reference — a `${NAME}` placeholder, allowed only in fields enabled for
  secrets (model `api_key`, request header values), resolved from the process
  environment at load or from the scope's secret catalog when it has one; the
  whole string is sensitive (ADR 0014, ADR 0049).
- Template — a string with `{{path.to.value}}` references resolved against the
  runtime context; a missing path is an error.
- Runtime context — the values a template reads: graph and node variables,
  peer, message, bot, tool inputs, and request results. The P1 peer values are
  `id`, `provider`, `subject`, and `name`. The old `contact.*` root is retired
  with no alias (ADR 0018, ADR 0020).
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
- Limit set — a TBD candidate for grouping scopes under aggregate resource
  ceilings, not an implemented object or accepted policy contract. Ownership,
  membership, catalog, and periods remain open (`docs/access-policy.md`).
- Structural limit — a technical bound on one definition's size, such as nodes
  per graph or steps per action. Proposed config fields and defaults are TBD;
  the concept is independent of aggregate host policy.
- Stored message bytes — a possible logical content measurement, distinct from
  database file size. Whether it defines an enforced ceiling, which content it
  counts, and exhaustion behavior are TBD (`docs/access-policy.md`).
- History import ceiling — a proposed per-bot bound on imported content.
  `settings.history_import_max_bytes` is not implemented; its semantics and
  default remain TBD (`docs/access-policy.md`).
- History migration — moving, never copying, a bot's chats, messages, and
  directory to another bot of the same scope, so a recreated or peer bot starts
  from the same conversations; it runs with both bots stopped
  (`docs/next-plan.md`). Unlinking an account never deletes history.
