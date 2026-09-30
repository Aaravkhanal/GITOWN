# GITOWN execution roadmap

This roadmap converts the long-term blueprint into dependency-ordered delivery milestones. A milestone is complete only when its API, authorization denials, migrations, audit events, UI, documentation, and automated tests are committed together.

For the user-facing numbered product plan and its exact completion gates, see [PRODUCT_PHASES.md](PRODUCT_PHASES.md). The two documents use different views intentionally: this roadmap orders engineering dependencies, while the product plan tracks parity areas.

## Current product boundary

GITOWN already supports accounts, private/public/internal repositories, invitation-based collaborators with role changes and confirmed ownership transfer, HTTP and SSH Git clone/fetch/push, a forced-command SSH gateway, Git LFS batch transfer and path locks, repository browsing, issues with planning views and district-wide boards, same-repository Unite requests with formal reviews, inline diff comments, and branch rules enforced identically across browser edits, API merges, HTTPS, and SSH, a durable notification and email-outbox pipeline, public discovery, districts and crews, Drops, crate records with an unscoped npm subset and an OCI blob and manifest subset, repository lifecycle recovery, a local CLI, backups, and browser/integration tests.

It remains a development alpha. It is not yet safe for untrusted public hosting. Product phases 3–9 are complete for their in-scope list, with the boundaries in [PRODUCT_PHASES.md](PRODUCT_PHASES.md): no merge queue or forks yet, an invoice ledger that does not charge a card, a forced-command SSH gateway, SSH-key provenance, an unscoped npm subset, an OCI blob and manifest subset, and a fixed pattern list. SAML/OIDC, IP restrictions, an embedded sshd, Sigstore, card charging, and a full npm or container registry stay later. Phases 1–2 and the remaining delivery order below still have open gates.

## Delivery order

### 1. Collaboration core (complete)

Unite request discussions, a deduplicated timeline, formal reviews (approve, request changes, dismiss with an audit event, stale-review handling), inline diff comments anchored to commit/file/side/line with replies and resolution, assignees, mentions, subscriptions, an in-app inbox, and comment edit history all shipped in phase 3.

### 2. Protected delivery (complete)

The branch-rule data model and settings UI, required Unite requests, approvals, resolved conversations, named reviewers/crews, and status checks, consistent policy enforcement across browser edits, API merges, HTTPS pushes, and SSH pushes, squash/rebase/merge methods with protection-aware source-branch deletion, and a status/check API all shipped in phase 3. A safe merge queue stays later.

### 3. Identity and account security

- Verified email, password recovery, invitation controls, and account/device sessions.
- TOTP MFA with recovery codes and step-up authentication for destructive actions.
- SSH keys, deploy keys, scoped/rotatable credentials, and security-event notifications.
- Abuse throttles by account and IP with operator-visible decisions.

### 4. Git and code experience

- SSH transport with forced commands and identical branch policy enforcement.
- Raw downloads, rendered sanitized Markdown, syntax highlighting, tags, blame, and pagination.
- Repository size accounting, quotas, maintenance, orphan reconciliation, and LFS.
- CLI installers, shell completion, signed releases, and server/token profiles.

### 5. Integrations and operations

- Durable outbox for exact ref changes and collaboration events.
- Signed webhooks with delivery history, retries, replay, and secret rotation.
- Metrics, tracing, dashboards, alerts, retention controls, and operator runbooks.
- Scheduled purge/maintenance jobs, online backup coordination, and automated restore drills.
- Hardened process isolation, independent security review, load/soak/chaos tests, and deployment verification.

### 6. Product ecosystem

- Organizations, teams, forks, cross-repository pull requests, releases, and assets.
- Projects, wikis, discussions, pages, packages, and repository search.
- A sandboxed automation runner with workflows, logs, artifacts, secrets, cancellation, quotas, and status integration.
- Enterprise identity, compliance exports, billing, high availability, and multi-region disaster recovery.

## Release gates

Public beta requires verified identity, MFA, protected branches, durable event delivery, quotas, process isolation, observability, automated restore proof, abuse controls, and an independent security review. Features after that gate must preserve compatibility with standard Git and keep GITOWN data exportable.
