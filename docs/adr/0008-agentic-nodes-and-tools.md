# ADR 0008 — Agentic nodes with parameterized tools

Status: Accepted (2026-09)

## Context

Giving the LLM only two roles (routing to a node and realizing reply text)
falls short of real conversations. Inside a state, the agent may need to look
something up, ask a follow-up, or call a business API with concrete arguments
(for example, order two units of product X). Fixed entry actions and
text-only messages cannot express that.

## Decision

A node exposes a set of tools the agent may invoke while the conversation is
in that state:

- `goal`: what the node is trying to achieve, used as agent context. It is
  not a routing target; routing lives on edges.
- `policy`: constraints for generated text and agent behavior.
- `tools`: actions available in this node. Each action declares `inputs`
  (type, required, default, description) and consumes them in its steps as
  `{{input.<name>}}`. An input may `bind` to a declared node or graph
  variable: the value is read from it when the agent omits the argument and
  written back when the agent provides it.
- `edges`: the only allowed transitions, each with a user intent (or the
  default edge) and optional triggers. The agent never invents a transition
  outside them.

Flow for one incoming message in a node:

1. Deterministic edge matching runs first (edge triggers, no LLM).
2. If nothing matches and LLM routing is enabled, the agent decides: it may
   call one or more tools, with validated arguments, and may produce a reply.
3. Tool results return to the agent as context; the agent can chain calls,
   stay in the node, or take a declared edge.
4. When a reply is produced, the policy check runs before queueing.
5. Every tool call, argument set, and decision is logged.

Bounds and safety:

- Maximum tool calls per incoming message and a loop timeout, configured
  under `llm.agent`.
- Arguments are validated against the declared `inputs`; the model never
  writes template strings directly.
- A call with a missing required argument returns a tool error to the agent,
  which asks the user; the answer is remembered in the bound variable. Agents
  fill declared variables only; they never create new ones.
- Tools with side effects may require a confirmation guard before executing.
- With no LLM configured, tools are unavailable and the bot stays fully
  deterministic (`on_enter`, `on_exit`, edges).

## Consequences

- Bots gain real agentic capability while state transitions stay explicit.
- Actions become tool contracts: `inputs` are part of their public API and
  need names and descriptions that read well to a model.
- New failure modes (loops, wrong arguments, side effects) must be bounded
  and logged; cost per message grows only where tools are enabled.
- `can_use` is replaced by `tools`, and `node.intent` becomes `node.goal`.
