# GITOWN product phases

This file is the product-parity plan discussed for GITOWN. It complements the dependency-ordered engineering roadmap. A phase is complete only when its listed capabilities, authorization rules, audit trail, UI, API contract, documentation, and automated tests are shipped.

Status legend: **partial** means usable capabilities exist but the phase gate has not been met; **planned** means implementation has not reached the gate.

## Phase 1 — Safe public accounts (partial)

Shipped: registration and login, Argon2id passwords, authenticated password changes that revoke other sessions and can revoke every access token, signed-in device visibility, scoped personal access tokens, CSRF/origin checks, and basic login throttling.

Remaining gate: verified email, password recovery, TOTP MFA and recovery codes, step-up authentication for destructive actions, security-event notifications, invitation controls, stronger account/IP abuse throttles, and operator-visible abuse decisions.

## Phase 2 — Everyday repository work (partial)

Shipped: public/private repository creation, collaborators and roles, real bare Git storage, smart HTTP clone/fetch/push, branch/tag transport, tree and text browsing, raw downloads, repository and file-specific history, lifecycle controls, friendly CLI commands, and browser file creation/editing/deletion with atomic stale-head protection.

Remaining gate: file rename/upload, sanitized Markdown, syntax highlighting, blame, compare UI, import/export, orphan reconciliation, and an embedded SSH server. Repository and account quotas, `git gc`, Git LFS batch transfer with path locks, and a forced-command SSH gateway are implemented. A push that unpacks over the quota has its new refs rolled back before the client is told the push succeeded. LFS has no multipart transfer. SSH is not an embedded sshd.

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

Remaining gate: custom domains and district boards. Badges are not commit counts. Ranked public code search, topic pages, and the public contribution graph are phase 6.

## Phase 6 — Discovery (complete)

Shipped: language and beginner-friendly filters, recently updated sorting, ranked search of a capped public text index with a `git grep` fallback, topic catalog and topic pages, help-wanted and good-first-task discovery, topic-overlap recommendations with a trending fallback, an hourly Spark cap counted from audit events, user-owned community collections of public repositories, operator-featured collections through `GITOWN_OPERATORS`, and a 180-day public contribution graph of issues, comments, Sparks, and merged Unite requests.

Boundaries: the index stores a limited number of text lines from public default branches. It is not an external search cluster. Recommendations are topic overlap, not a trained model. The contribution graph does not count private commits. Custom project domains stay later.

## Phase 7 — Districts and crews (complete)

Shipped: districts (organizations) with public or private visibility, owner and admin or member roles, repository creation policy, base permission on district repositories, internal repository visibility, crews (teams), crew review requests that notify members, encrypted district secrets when `GITOWN_SECRET_KEY` is set, a district audit JSON view and CSV download, usage counts, an operator invoice ledger, and two enforced policies: whether the district allows public repositories and whether it allows collaborators from outside the district.

Boundaries: invoices record an amount an operator enters and can be marked paid. Responses include `charges: false`. GITOWN does not charge a card. SAML/OIDC and IP restrictions stay later. District-wide boards stay in phase 4. A crew request does not count as a review by itself.

## Phase 8 — Git transport depth (complete)

Shipped: user SSH keys, repository deploy keys, a forced-command `ssh-shell` gateway using the same branch protections as HTTPS, Git LFS batch upload and download plus path locks, shallow clone, fetch, and push through the system Git client, repository and account quotas from `GITOWN_REPO_QUOTA_BYTES` and `GITOWN_USER_QUOTA_BYTES`, ref-change events with old and new SHAs, maintenance via `git gc`, tag listings that report whether a signature block is present, and SSH signing keys used to verify drop provenance.

Boundaries: the gateway trusts the fingerprint argument because sshd must bind it with `ForceCommand`. Run that binary as a dedicated user. GITOWN does not embed sshd. LFS locks are single-request path locks, not multipart transfers. A pack is measured again after unpack; refs created or moved by that push are rolled back and the client is not told the push succeeded when the unpacked size exceeds the quota. Tag “signed” still means a signature block is present. Trust for drop provenance is `ssh-keygen -Y verify` against a key the publisher registered, not a global keyring. Client coverage is the system `git` plus the in-process gateway checks in [GIT_CLIENTS.md](GIT_CLIENTS.md).

## Phase 9 — Drops and crates (complete)

Shipped: drops (releases) for an existing tag or for an annotated tag created at publish time, draft and prerelease flags, notes, a generated changelog from `git log`, binary assets with SHA-256 checksums, public download counts, and provenance that is marked verified only after an SSH signature matches a signing key the publisher registered. Crates store package name, version, metadata, a file up to 512 KB, retention, and deletion. Package-scoped tokens authorize crate routes. Unscoped npm publish, packument, and tarball routes are served at `/npm`. OCI blob upload and manifest routes are served at `/v2`. Publishing rejects a fixed list of private-key headers, token prefixes, and dangerous command strings.

Boundaries: this is not Sigstore. An empty signature stays unverified. The npm routes accept one unscoped name, not a full npm registry. The OCI routes store one blob and one manifest reference, not a full container registry. The pattern list is not a malware engine. Additional package ecosystems stay later.

## Later phases (planned)

10. Integrations: durable events, signed webhooks, OAuth apps, and deployment verification.
11. Routes: sandboxed automation runners, workflow definitions, logs, artifacts, cancellation, and check integration.
12. Extended ecosystem: wikis, Showcase hosting, Town Hall, forks (Remixes), cross-repository Unite, card billing, and high availability. SAML/OIDC and IP restrictions stay later as well.

The naming contract is defined in [NAMING.md](NAMING.md). Standard Git protocol and commands remain compatible even when GITOWN presents friendlier names in its UI and CLI.
