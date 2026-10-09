# Identity, directory, and targets

What this documents: how a channel names people and conversations, and how the
runtime can address them. Covers contact resolution, the account's address book
as a directory, stable identifiers over time, and whether a bot may start a
conversation or only reply. Identity, directory membership, and send permission
are separate facts (ADR 0029, ADR 0051).

Record per channel/mode: what identifiers exist, how to resolve one, whether a
directory can be imported or enumerated, how usernames or aliases behave, and
whether a well-formed target is actually reachable. A phone number is not a
universal identity and directory membership never grants sending.
