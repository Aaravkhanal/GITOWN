# GITOWN product phases

This file is the product-parity plan discussed for GITOWN. It complements the dependency-ordered engineering roadmap. A phase is complete only when its listed capabilities, authorization rules, audit trail, UI, API contract, documentation, and automated tests are shipped.

Status legend: **partial** means usable capabilities exist but the phase gate has not been met; **planned** means implementation has not reached the gate.

## Phase 1 — Safe public accounts (partial)

Shipped: registration and login, Argon2id passwords, authenticated password changes that revoke other sessions and can revoke every access token, signed-in device visibility, scoped personal access tokens, CSRF/origin checks, and basic login throttling.

Remaining gate: verified email, password recovery, TOTP MFA and recovery codes, step-up authentication for destructive actions, security-event notifications, invitation controls, stronger account/IP abuse throttles, and operator-visible abuse decisions.

## Phase 2 — Everyday repository work (partial)

Shipped: public/private repository creation, collaborators and roles, real bare Git storage, smart HTTP clone/fetch/push, branch/tag transport, tree and text browsing, raw downloads, repository and file-specific history, lifecycle controls, friendly CLI commands, and browser file creation/editing/deletion with atomic stale-head protection.

Remaining gate: file rename/upload, sanitized Markdown, syntax highlighting, blame, compare UI, import/export, orphan reconciliation, and an embedded SSH server. This slice adds env-based repository and account quotas, `git gc` maintenance, a basic Git LFS batch upload/download path, and a forced-command SSH gateway. Quotas are checked before Git runs; one accepted pack can still overshoot because size is measured after unpack. LFS has no locking or multipart transfer. SSH is not an embedded sshd.

## Phase 3 — Collaboration and protected delivery (partial)

Shipped foundation: issues, assignees, labels, milestones with due dates and progress, discussions, issue following, Unite following, and an in-app inbox for issue and Unite events; same-repository Unite requests, diffs, discussions, close/reopen, formal approve/request-changes reviews with stale-head visibility, per-branch Merge Guards requiring fresh approvals and blocking current change requests, optional direct-push/browser-edit protection that requires Unite for a branch, merge commits, expected-SHA checks, and interrupted-merge recovery.

Shipped in this slice: draft Unite requests, inline comments on an exact commit/file/side/line with replies, resolution, and outdated marking, requested reviewers, review dismissal with an audit event, squash/rebase/merge plus optional source-branch deletion, Unite assignees/labels/linked issues, auto-close from the Unite body, linked issues, and commit messages when no blocker is open, a timeline, diff pagination, invitations with a durable email outbox, and ownership-transfer confirmation. Branch rules can require resolved conversations, an up-to-date branch, owner or maintainer approval, successful check contexts, signed commits, and owner/maintainer-only pushes. The same rules apply to browser edits, API merges, and HTTPS pushes. Require Unite stays off unless an owner enables it. Force pushes and branch deletions stay rejected globally.

Remaining gate: a merge queue, forks (Remixes), and cross-repository Unite requests. Crew review requests notify crew members; they do not change Merge Guard math. Maintainer approval still means the owner or a member with the maintain role, or a district owner/admin on a district repository. Email is stored in an outbox and is handed to SMTP only when GITOWN_SMTP_ADDR and GITOWN_SMTP_FROM are set; without SMTP, immediate messages are recorded as sent and are not delivered to an inbox.

## Phase 4 — Boards and planning (partial)

Shipped foundation: a repository issue board with To do, In progress, and Done columns, synchronized with issue close/reopen actions; owner-managed issue templates prefill new issues; same-repository issue dependencies reject cycles and block completion while prerequisites remain open.

Shipped in this slice: comment editing with history, label and text filtering, bug/feature/custom template kinds, duplicate marking, pinning, issue transfer between repositories the same owner controls, priority, estimate, due date, and iteration, saved searches, and board, table, and roadmap views with drag-and-drop on the three columns. Dependencies remain the sub-issue model. `#42` references are listed from the issue body and comments. Closing keywords in a Unite request or its commits can close an issue.

Remaining gate: district-wide boards. Districts can own repositories, but boards stay inside one repository.

## Phase 5 — Social and discovery (partial)

Shipped foundation: public builder profiles, configurable repository showcases, one-click repository Sparks with visible counts, builder follows with counts and self-follow protection, a public following feed for repository creation/Sparks/issues/Unite requests, owner-managed repository topics, public repository text/topic search with Spark sorting and 30-day trending, and paged public builder search by username/name/bio.

Shipped in this slice: watch, participate, and ignore subscription modes; mentions; assignment, review-request, merge, check, invitation, and ownership notifications; mark-all-read; configurable email preference with unsubscribe tokens; profile skills, availability, and open-to-collaborators; contribution counts and badges computed from public merged Unite requests, approvals, closed issues, and public repositories; repository homepage and tech stack; Explore search for public issues and Unite requests; and an open-to-collaborators builder filter.

Remaining gate: custom domains and district boards. Badges are not commit counts. Code search in this slice is fixed-string `git grep` over a small set of recent public repositories, not an index.

## Phase 6 — Discovery (partial)

Shipped in this slice: language and beginner-friendly filters, recently updated sorting, fixed-string public code search, help-wanted and good-first-task discovery, topic-overlap recommendations with a trending fallback, an hourly Spark cap counted from audit events, and user-owned community collections of public repositories.

Remaining gate: ranked code search, staff-featured collections, contribution graphs, and custom domains. Recommendations are not a trained model.

## Phase 7 — Districts and crews (partial)

Shipped in this slice: districts (organizations) with public or private visibility, owner and admin or member roles, repository creation policy, base permission on district repositories, internal repository visibility, crews (teams), crew review requests that notify members, encrypted district secrets when `GITOWN_SECRET_KEY` is set, and a district audit export.

Remaining gate: SAML/OIDC, billing and usage, enterprise policy management, IP restrictions, and district-wide boards. A crew request does not count as a review by itself.

## Phase 8 — Git transport depth (partial)

Shipped in this slice: user SSH keys, repository deploy keys, a forced-command `ssh-shell` gateway using the same branch protections as HTTPS, basic Git LFS batch upload and download, a shallow-clone proof through the existing smart HTTP backend, repository and account quotas from `GITOWN_REPO_QUOTA_BYTES` and `GITOWN_USER_QUOTA_BYTES`, ref-change events with old and new SHAs, maintenance via `git gc`, and tag listings that report whether a signature block is present.

Remaining gate: an embedded or separately hardened sshd, LFS locking, identical client testing beyond the shallow clone, and signature trust. The gateway trusts the fingerprint argument because sshd must bind it with `ForceCommand`. Run that binary as a dedicated user. Signed means a PGP or SSH signature block is present, not that a keyring trusts it. One pack can exceed a quota once; the next push is rejected.

## Phase 9 — Drops and crates (partial)

Shipped in this slice: drops (releases) for tags that already exist, draft and prerelease flags, notes, a generated changelog from `git log`, binary assets with SHA-256 checksums, public download counts, and publisher-supplied provenance text. Crates store package name, version, metadata, a file up to 512 KB, retention, deletion, package-scoped tokens, and a pattern check for private-key headers and a few token prefixes.

Remaining gate: npm and container wire compatibility, malware scanning, and verified provenance. The `npm` ecosystem value is a label. Provenance is not Sigstore or signature verification. The pattern check is not a malware scanner.

## Later phases (planned)

10. Integrations: durable events, signed webhooks, OAuth apps, and deployment verification.
11. Routes: sandboxed automation runners, workflow definitions, logs, artifacts, cancellation, and check integration.
12. Extended ecosystem: wikis, Showcase hosting, Town Hall, forks (Remixes), cross-repository Unite, billing, and high availability.

The naming contract is defined in [NAMING.md](NAMING.md). Standard Git protocol and commands remain compatible even when GITOWN presents friendlier names in its UI and CLI.
