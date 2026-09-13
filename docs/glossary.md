# Glossary

- Bot — one WhatsApp account plus the graph that gives it behavior.
- Fleet — the set of bots a single BotForje instance runs.
- Scope — generic isolation namespace; references and sessions never cross
  scopes.
- Graph — conversation state machine owned by one bot; may override the
  instance routing mode.
- Node — a state: goal, policy, entry/exit actions, agent tools, and
  optional node-local context.
- Goal — the agent's objective in a node, short and clear.
- Edge — transition between nodes with a required user intent, except the
  default (catch-all) edge; triggers is an optional deterministic fast path.
- Intent — on an edge, the user trigger; on a message, the reply's goal.
  Short, clear text (not snake_case), like goal.
- Action — reusable pipeline of steps (`message`, `request`, `location`)
  with guards and optional declared inputs.
- Tool — an action a node exposes to the agent, invoked with validated
  arguments (`inputs`).
- Input — named, typed argument of a tool; may bind to a node or graph
  variable to read and write it.
- Node context — node variables; reset when the node is entered again.
- Graph context — session-wide variables; persist across nodes.
- Node memory — ordered list of visited nodes per session, current last; used
  for routing and as agent context.
- Agent loop — bounded sequence of tool calls and replies the LLM runs for
  one incoming message inside a node.
- Step — single operation inside an action.
- Guard — rule that limits execution, for example a cooldown per sender.
- Policy — constraints for agent decisions, tool selection, and generated
  text. Node policy is ambient; message policy refines it.
- Session — per-sender conversation state stored in SQLite.
- PN (phone number) — the classic WhatsApp address `user@s.whatsapp.net`;
  the identity BotForje uses to key sessions.
- LID (hidden user) — WhatsApp's `user@lid` address, used as messages move
  away from phone numbers.
- Sender identity — the resolved PN for a sender, with the LID kept when
  known (ADR 0013).
- Messaging provider — the service end users are on (WhatsApp today); one
  provider is reached through one or more channels.
- Channel — one implementation that reaches a messaging provider. WhatsApp
  has several: whatsmeow today, other clients or the official Cloud API
  later.
- LLM provider — language model implementation (cloud or local) used for
  routing and replies.
- Inbox — routes inbound events to the right bot and session.
- Outbox — per-bot FIFO queue that paces outbound messages.
