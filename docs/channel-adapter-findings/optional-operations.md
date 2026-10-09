# Optional operations

What this documents: channel features that are useful but not baseline, so a bot
must declare and validate them instead of assuming them. Covers typing/presence,
marking inbound read, read receipts, and downloading received or historical
attachments, plus other optional interactions such as reactions and control
events. A setting the selected channel or account mode does not declare must fail
validation, never be silently ignored (ADR 0052).

Record per channel/mode: which optional operations exist, on what scope they
apply, and whether each is adapter support, account authorization or current
conversation eligibility. These stay optional ports, not baseline obligations.
