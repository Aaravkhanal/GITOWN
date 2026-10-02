# GITOWN execution roadmap

This roadmap converts the long-term blueprint into dependency-ordered delivery milestones. A milestone is complete only when its API, authorization denials, migrations, audit events, UI, documentation, and automated tests are committed together.

For the user-facing numbered product plan and its exact completion gates, see [PRODUCT_PHASES.md](PRODUCT_PHASES.md). The two documents use different views intentionally: this roadmap orders engineering dependencies, while the product plan tracks parity areas.

## Current product boundary

GITOWN already supports accounts, private/public/internal repositories, invitation-based collaborators with role changes and confirmed ownership transfer, HTTP and SSH Git clone/fetch/push, a forced-command SSH gateway, Git LFS batch transfer and path locks, repository browsing, issues with planning views and district-wide boards, same-repository Unite requests with formal reviews, inline diff comments, and branch rules enforced identically across browser edits, API merges, HTTPS, and SSH, a durable notification and email-outbox pipeline, public discovery, districts and crews, Drops, crate records with an unscoped npm subset and an OCI blob and manifest subset, signed webhooks (repository- and district-scoped, with Slack/Discord-formatted targets) with a durable delivery outbox, retries, and replay, OAuth applications, GITOWN Apps installed per repository, a general per-identity API rate limit, repository lifecycle recovery, a local CLI, backups, and browser/integration tests.

It remains a development alpha. It is not yet safe for untrusted public hosting. Product phases 3–10 are complete for their in-scope list, with the boundaries in [PRODUCT_PHASES.md](PRODUCT_PHASES.md). Phases 11 and 12 are partial: Routes plans work and records runners, secrets, artifacts, and quotas, and it does not execute a workflow command. The ecosystem slice includes remixes, cross-project Unite requests, a merge queue, Town Hall, Showcase pages, snippets, local supply-chain notes, pledges that do not charge a card, and public forge import without credentials. An invoice ledger still does not charge a card. The SSH gateway is a forced command. Drop provenance uses registered SSH keys. npm is an unscoped subset. OCI is a blob and manifest subset. Secret scanning uses a fixed marker list. GITOWN Apps install per repository only, not per district, with one long-lived installation token rather than a full short-lived app-identity model. SAML/OIDC, IP restrictions, an embedded sshd, Sigstore, card charging, a sandbox that could run untrusted work, and a full npm or container registry stay later. Phases 1–2 and the remaining delivery order below still have open gates.

## Delivery order

### 1. Collaboration core (complete)

Unite request discussions, a deduplicated timeline, formal reviews (approve, request changes, dismiss with an audit event, stale-review handling), inline diff comments anchored to commit/file/side/line with replies and resolution, assignees, mentions, subscriptions, an in-app inbox, and comment edit history all shipped in phase 3.

### 2. Protected delivery (complete)

The branch-rule data model and settings UI, required Unite requests, approvals, resolved conversations, named reviewers/crews, and status checks, consistent policy enforcement across browser edits, API merges, HTTPS pushes, and SSH pushes, squash/rebase/merge methods with protection-aware source-branch deletion, and a status/check API all shipped in phase 3. A first merge queue now lets only the front waiting Unite request merge. Deeper queue rules, such as speculative batches and required-check gating of the queue itself, stay later.

### 3. Identity and account security

- Verified email, password recovery, invitation controls, and account/device sessions.
- TOTP MFA with recovery codes and step-up authentication for destructive actions.
- SSH keys, deploy keys, scoped/rotatable credentials, and security-event notifications.
- Abuse throttles by account and IP with operator-visible decisions.

### 4. Git and code experience

SSH transport with forced commands and identical branch policy enforcement, repository size accounting and quotas, on-demand and scheduled `git gc` maintenance, tag signatures verified against the same registered-key trust store as commits, and LFS batch transfer with path locks all shipped in phase 8, alongside a deploy-key and push-activity UI. Still open in this section:

- Raw downloads, rendered sanitized Markdown, syntax highlighting, tags, blame, and pagination.
- Orphan storage reconciliation.
- CLI installers, shell completion, signed releases, and server/token profiles.

### 5. Integrations and operations

Durable outbox for exact ref changes and collaboration events, and signed webhooks (repository- and district-scoped, HMAC-signed, SSRF-guarded against private/loopback targets and re-checked on every delivery to resist DNS rebinding) with delivery history, retries, and replay all shipped in phase 10. OAuth applications (a real authorization-code flow with exact `redirect_uri` matching and immediate revocation), GITOWN Apps (installed per-repository with their own bot identity, a bounded simplification of a full app-identity model — see [PRODUCT_PHASES.md](PRODUCT_PHASES.md)), and Slack/Discord targets on the same webhook delivery path also shipped in phase 10. Secret rotation is a regenerate, not a scheduled rotation policy. GITOWN App installation is repository-scoped only; district-wide installation is not built. Still open in this section:

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
