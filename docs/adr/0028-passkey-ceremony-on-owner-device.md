# ADR 0028 — Passkey ceremonies happen on the account owner's device

Status: Accepted (2026-09)

## Context

WhatsApp requires passkey (WebAuthn) verification to link some accounts and, on
some of them, on every new connection. The linking flow is named Shortcake
internally; the phone shows "Continue on your other device", and a client that
cannot answer sees connection error 428. Reports concentrate on Business
accounts, but Meta has not documented the triggers, and accounts have been
observed entering the mode with no change on the owner's side.

The server sends a challenge and expects a signed WebAuthn assertion. The private
key lives in an authenticator (TPM, Secure Enclave, Keystore, a security key, or
a phone reached over Bluetooth) and never leaves it. The relying party id is
`whatsapp.com`, so only a page whose origin is under that domain can request the
assertion. The ceremony also needs a user gesture and verification.

Two key systems are involved, and only one is software:

- The companion identity (Noise and Signal keys, prekeys, registration id) is
  device-store state. It can run on a server, which is what makes a headless
  client possible at all.
- The passkey is hardware state: it cannot be copied, and copying session
  credentials does not copy the ability to answer the next challenge.

Third-party results match that boundary. Importing an authenticated WhatsApp Web
session into a headless client can produce valid credentials and still fail with
428, because the server asks again on every connection; virtual authenticators
are rejected. Beeper's bridge (mautrix/whatsapp) added a passkey login step that
relays the challenge to the app on the owner's device, which performs the
ceremony (issue #935; PR #936, released in v26.07).

## Decision

The passkey ceremony always happens on a device the account owner controls: their
phone, or their own browser. Botflota never imports browser sessions or passkey
material, and never asks for a challenge to be signed on its behalf.

- Linking keeps the QR flow, unchanged for most accounts.
- The pairing code keeps the ceremony on the phone: after the code is entered,
  the phone performs the passkey step and the companion only receives the
  handoff (`PairPasskeyRequest`, `PairPasskeyConfirmation`,
  `SendPasskeyResponse`, `SendPasskeyConfirmation`).
- When the server asks the companion for the assertion instead, the pending step
  is reported as a state, never a silent loop. That case needs a browser context
  the account owner controls, and this decision does not cover it.
- A connection rejected with 428 or 463 stops the bot and reports, instead of
  retrying in a way that cannot succeed.
- The official Cloud API is the documented escape for accounts the unofficial
  path cannot serve.

## Consequences

- Linking is answerable from the phone alone: the ceremony happens there, and
  the credentials land in the device store like any other pairing.
- An account that requires a passkey per connection cannot be automated. It is a
  coverage gap to detect and communicate, not a bug to fix.
- 428 and 463 join logout and ban as states that stop the bot (ADR 0011).
- The pairing code becomes a first-class method beside the QR, which widens the
  CLI and the future pairing session in the API.
- Cloud API becomes a product path, not a detail: it exposes no history sync, so
  onboarding cannot bootstrap from history there.
