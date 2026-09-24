# ADR 0027 — Guard state lives on the chat

Status: Accepted (2026-09)

## Context

A guard is declared on an action (`cooldown`, `rate_limit`) and applies per
sender, but the rule alone cannot enforce anything: it needs to remember what
already happened for that person. The first implementation kept that memory on
the session. That ties protection to a graph run: if the session expires (for
example after `default_timeout`) while a rate limit window is still open, the
counter resets and the protection lapses, even though the person did nothing to
deserve it.

## Decision

Guard state lives on the **chat**, keyed by **action key**, in one JSON blob
(`chats.guards`):

- The chat already pins `(scope, bot, sender)`, so state is per bot and per
  person, isolated across bots and scopes.
- The action is the right key: a guard belongs to an action, and when two nodes
  invoke the same action they share its limit for that person.
- The session keeps only graph state; guards survive session expiry and
  restarts.

State per action is a small typed struct with one field per guard type
(`cooldown` last run, `rate_limit` counter and window start). Adding a guard
type adds a field; unknown or removed entries are ignored.

## Config changes

The stored state is a snapshot of facts (names, timestamps, counters), never a
reference to config, so a guard change never needs a data migration:

- Changing a guard's numbers re-interprets the same state under the new rule
  (a lower limit blocks sooner; a shorter window ages the counter out).
- Removing a guard leaves its entry unread; re-adding it revives the entry,
  which self-expires by time.
- Changing an action's `display_name` preserves the key and its guard state.
  Changing or deleting its key starts fresh or orphans entries; harmless and
  bounded by config.
- A config reload (ADR 0009) does not reset guard state.

## Consequences

- Guards outlive sessions; a person cannot wait out a limit by going quiet.
- The engine loads guard state with the chat and saves it only when an action
  runs under a guard.
- `chats.guards` is a JSON blob, consistent with the other map-shaped state
  (contexts, visited nodes). If ops ever need to query or prune it, it can move
  to a `guard_state (chat_id, action, guard, state)` table without changing the
  semantics.
- No foreign key to config exists or can exist (config is not in the database),
  so action keys are the contract, as with nodes and request names.
