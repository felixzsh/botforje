# ADR 0050 — AGPL-3.0-or-later as the project license

Status: Accepted (2026-10)

## Context

Botflota is an independently hosted runtime with a network control plane. Its
source and improvements to covered versions should remain available under a
network copyleft license. Future channel libraries may themselves require AGPL;
changing the project license is not approval to add any such dependency.

The project owner has chosen AGPL rather than the earlier MIT direction.

## Decision

- License Botflota's own code under the GNU Affero General Public License,
  version 3 or any later version (`AGPL-3.0-or-later`).
- Keep the unmodified license text in the root `LICENSE` and the project's
  copyright and license notice in `README.md`.
- Preserve third-party licenses and notices. Review dependency compatibility
  before adoption; this decision does not relicense dependencies.
- Commercial use and hosting remain allowed under the license. Distribution
  must provide Corresponding Source as required, and modified versions used
  through a network must offer it to their remote users under section 13.
- Keep independent host applications separate as in ADR 0046. Their licensing
  depends on the actual integration, not simply on consuming Botflota's API.

## Consequences

- Documentation and future packaging must identify `AGPL-3.0-or-later`, not MIT.
- Release and deployment procedures must preserve notices and provide the
  required source access for the exact covered version, including build inputs.
  A public repository alone does not prove compliance for every deployment.
- This changes the license notice and decision only: it adds no channel library,
  runtime endpoint, or source-distribution mechanism.
- Earlier grants are not revoked by this change. Contributions require rights
  compatible with the project license; no contributor agreement is introduced.
