# ADR 0004 — Messaging channel and LLM provider are separate ports

Status: Accepted (2026-09). The classification role of `LLMProvider` is
superseded by ADR 0030; the rest stands.

## Context

A bot needs a messaging transport; it may also use a language model. These
are independent concerns with different vendors, failure modes, and
lifecycles. Conflating them would tie the graph engine to both WhatsApp and
one LLM vendor at the same time.

## Decision

Define two small ports, owned by the consumer:

- `MessageChannel`: connect, disconnect, send, and a stream of inbound
  messages. A channel is an implementation for a messaging provider, not the
  provider itself: WhatsApp is reached through whatsmeow today, and can be
  reached through other clients or the official Cloud API later.
- `LLMProvider`: classification and reply realization. Multiple providers
  (cloud or local) sit behind the same interface and are selected per
  instance; a SaaS deployment may let clients bring their own key.

Ports stay separate from config and persistence. Domain code never imports
whatsmeow, an LLM SDK, YAML, or SQL.

## Consequences

- The engine is testable with a fake channel and a fake provider.
- Another channel (for example the official WhatsApp Cloud API), another
  messaging provider (Telegram), or a local model is a new package, not a
  rewrite.
- Two configuration sections and two lifecycles to operate.
- Provider API drift is contained inside adapter packages.
