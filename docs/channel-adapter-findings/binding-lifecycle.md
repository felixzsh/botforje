# Binding lifecycle

What this documents: how a bot becomes authorized on a channel and how that
authorization changes. Covers the setup steps (token, code, QR, approval), how
the account identity is verified after setup, reauthorization, revocation,
unbind, and device or session limits. The steps differ per channel and account
mode and must not impose the WhatsApp companion-device model on others
(ADR 0053).

Record per channel/mode: the setup inputs, what proves the account identity, how
many devices or sessions are allowed, how revocation is detected, and what
survives restart. Config-only validation never performs these steps.
