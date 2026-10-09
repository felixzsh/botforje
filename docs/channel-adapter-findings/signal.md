# Signal adapter findings

Recorded: 2026-10-09. Implementation and supported-flow investigation are pending.
[Channel plan](../channel-plan.md), sections 5 and 8, owns the provider-neutral reference case.
This note records the owner's library direction, not an implemented adapter or a completed gate.

## Owner-selected direction

The owner explicitly chose **signalmeow** for the future Signal adapter in the discussion.
The desired approach is protocol-oriented integration, comparable in spirit to whatsmeow,
without browser automation or an unnecessary runtime wrapper.

This choice was not part of the existing channel plan. It is preserved here for the future
adapter work; no dependency, pinned version, code, or canonical ADR was changed by this note.

## What was and was not established

- The channel plan considered Signal's identity semantics, not a particular library or transport.
  Its privacy, username, and service-identifier analysis remains in section 5.2.
- The exact signalmeow upstream repository/module, supported setup flows, build requirements,
  native dependencies, and license were not investigated in this conversation.
- No benchmark was run. Similarity to whatsmeow does not establish equal RAM, CPU, or capacity.
  There is no verified resource estimate for 1000 Signal bindings here.

## Revisit when implementing

- Verify the selected upstream implementation and pin the intended module/version.
- Record its concrete setup, credential storage, reconnect, send, and receive requirements in
  the appropriate decision owners before they constrain shared contracts.
- Verify any native/runtime dependencies rather than assuming a fully self-contained Go binary.
- Resolve ADR 0053's credential-protection gate before shipping a new credential type.
- Measure the actual client under representative workloads and declare only implemented features.

The selection does not authorize provider-specific fields in common contracts or assume that
phone registration makes telephone numbers universal peer or binding identities.
