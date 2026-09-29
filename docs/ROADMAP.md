# GITOWN execution roadmap

This roadmap converts the long-term blueprint into dependency-ordered delivery milestones. A milestone is complete only when its API, authorization denials, migrations, audit events, UI, documentation, and automated tests are committed together.

For the user-facing numbered product plan and its exact completion gates, see [PRODUCT_PHASES.md](PRODUCT_PHASES.md). The two documents use different views intentionally: this roadmap orders engineering dependencies, while the product plan tracks parity areas.

## Current product boundary

GITOWN already supports accounts, private/public repositories, role-based collaborators, HTTP Git clone/fetch/push, repository browsing, issues with comments and labels, pull requests with real diffs and race-safe merge commits, repository lifecycle recovery, a local CLI, backups, and browser/integration tests.

It remains a development alpha. It is not yet safe for untrusted public hosting.

## Delivery order

### 1. Collaboration core

- Pull-request discussions and a unified timeline.
- Formal reviews: approve, request changes, dismiss, and stale-review handling.
- Inline diff comments anchored to commit, file, side, and line.
- PR assignees, mentions, subscriptions, and an in-app inbox. Issue assignees and milestones are shipped.
- Edit history and moderation controls for user-authored content.

### 2. Protected delivery

- Branch-rule data model and repository settings UI.
- Required pull requests, approvals, resolved conversations, and status checks.
- Consistent policy enforcement in HTTP push and API merges.
- Squash/rebase merge methods, source-branch deletion, and deterministic stale-head behavior.
- Status/check API followed by a safe merge queue.

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
