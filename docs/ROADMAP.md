# GITOWN execution roadmap

This roadmap converts the long-term blueprint into dependency-ordered delivery milestones. A milestone is complete only when its API, authorization denials, migrations, audit events, UI, documentation, and automated tests are committed together.

For the user-facing numbered product plan and its exact completion gates, see [PRODUCT_PHASES.md](PRODUCT_PHASES.md). The two documents use different views intentionally: this roadmap orders engineering dependencies, while the product plan tracks parity areas.

## Current product boundary

GITOWN already supports accounts, private/public/internal repositories, invitation-based collaborators with role changes and confirmed ownership transfer, HTTP and SSH Git clone/fetch/push, a forced-command SSH gateway, Git LFS batch transfer and path locks, repository browsing, issues with planning views and district-wide boards, same-repository Unite requests with formal reviews, inline diff comments, and branch rules enforced identically across browser edits, API merges, HTTPS, and SSH, a durable notification and email-outbox pipeline, public discovery, districts and crews, Drops, crate records with an unscoped npm subset and an OCI blob and manifest subset, signed webhooks (repository- and district-scoped, with Slack/Discord-formatted targets) with a durable delivery outbox, retries, and replay, OAuth applications, GITOWN Apps installed per repository, a general per-identity API rate limit, repository lifecycle recovery, a local CLI, backups, and browser/integration tests.

It remains a development alpha. It is not yet safe for untrusted public hosting. Product phases 3–10 are complete for their in-scope list, with the boundaries in [PRODUCT_PHASES.md](PRODUCT_PHASES.md). Phases 11 and 12 are partial: Routes plans work and records runners, secrets, artifacts, and quotas, and it does not execute a workflow command. The ecosystem slice includes remixes, cross-project Unite requests, a merge queue, Town Hall, Showcase pages, snippets, local supply-chain notes, validated Git-backed wiki attachments, optional OSV advisory lookups, pledges that do not charge a card, and public forge import without credentials. OSV is off by default because lookups share exact dependency names and versions; see the [Phase 12 decision note](PHASE12_ECOSYSTEM.md). Device registrations still poll the inbox; real push delivery, private-forge import, and HA are scoped but not implemented. The SSH gateway is a forced command. Drop provenance uses registered SSH keys. npm is an unscoped subset. OCI is a blob and manifest subset. Secret scanning uses a fixed marker list. GITOWN Apps install per repository only, not per district, with one long-lived installation token rather than a full short-lived app-identity model. SAML/OIDC, IP restrictions, an embedded sshd, Sigstore, card charging, a sandbox that could run untrusted work, and a full npm or container registry stay later. Phases 1–2 and the remaining delivery order below still have open gates.

## Delivery order

### 1. Collaboration core (complete)

Unite request discussions, a deduplicated timeline, formal reviews (approve, request changes, dismiss with an audit event, stale-review handling), inline diff comments anchored to commit/file/side/line with replies and resolution, assignees, mentions, subscriptions, an in-app inbox, and comment edit history all shipped in phase 3.

### 2. Protected delivery (complete)

The branch-rule data model and settings UI, required Unite requests, approvals, resolved conversations, named reviewers/crews, and status checks, consistent policy enforcement across browser edits, API merges, HTTPS pushes, and SSH pushes, squash/rebase/merge methods with protection-aware source-branch deletion, and a status/check API all shipped in phase 3. A first merge queue now lets only the front waiting Unite request merge. Deeper queue rules, such as speculative batches and required-check gating of the queue itself, stay later.

### 3. Identity and account security

- One-time verified email and password recovery are implemented; enforcement is opt-in with `GITOWN_REQUIRE_VERIFIED_EMAIL=true`. Configure and verify SMTP before enabling enforcement. Session/device management is implemented.
- TOTP MFA includes encrypted secrets, replay protection, single-use codes, and atomic recovery-code regeneration. Ten-minute step-up authentication protects sensitive credential, session, repository deletion, and transfer actions.
- New-device, password-change/reset, MFA, recovery-code, and session-revocation notices use the durable email outbox.
- Shared PostgreSQL IP/account throttles and a redacted operator abuse view are implemented. Remaining work is production delivery verification, proxy identity review, and threshold tuning.

### 4. Git and code experience

SSH transport with forced commands and identical branch policy enforcement, repository quotas and maintenance, signed-tag verification, public-source imports, all-ref Git bundle exports, a tags UI, and operator dry-run/recoverable orphan-storage quarantine are implemented. Linux/macOS CLI archives use a Sigstore-signed checksum manifest, with a verifying installer and bash/zsh/fish completions. GITOWN deliberately does not embed sshd; operators configure a separate forced-command gateway. Automatic server/token profiles remain a CLI follow-up.

### 5. Integrations and operations

Durable outbox for exact ref changes and collaboration events, and signed webhooks (repository- and district-scoped, HMAC-signed, SSRF-guarded against private/loopback targets and re-checked on every delivery to resist DNS rebinding) with delivery history, retries, and replay all shipped in phase 10. OAuth applications (a real authorization-code flow with exact `redirect_uri` matching and immediate revocation), GITOWN Apps (installed per-repository with their own bot identity, a bounded simplification of a full app-identity model — see [PRODUCT_PHASES.md](PRODUCT_PHASES.md)), and Slack/Discord targets on the same webhook delivery path also shipped in phase 10. Secret rotation is a regenerate, not a scheduled rotation policy. GITOWN App installation is repository-scoped only; district-wide installation is not built. Still open in this section:

- Centralized OpenTelemetry/OTLP spans, dependency-level tracing, deployment alert routing, retention controls, and production verification of the supplied Prometheus/Grafana starter assets.
- Online backup coordination and automated restore drills; maintenance exists but scheduled permanent purge is not built.
- Hardened process isolation, independent security review, load/soak/chaos tests, and deployment verification.

### 6. Product ecosystem

- Organizations, teams, forks, cross-repository pull requests, releases, and assets.
- Projects, wikis, discussions, pages, packages, and repository search.
- A sandboxed automation runner with workflows, logs, artifacts, secrets, cancellation, quotas, and status integration.
- Enterprise identity, compliance exports, billing, high availability, and multi-region disaster recovery.

## Release gates

Public beta requires verified identity, MFA, protected branches, durable event delivery, quotas, process isolation, observability, automated restore proof, abuse controls, and an independent security review. Features after that gate must preserve compatibility with standard Git and keep GITOWN data exportable.
