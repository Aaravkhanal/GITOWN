# GITOWN product phases

This file is the product-parity plan discussed for GITOWN. It complements the dependency-ordered engineering roadmap. A phase is complete only when its listed capabilities, authorization rules, audit trail, UI, API contract, documentation, and automated tests are shipped.

Status legend: **partial** means usable capabilities exist but the phase gate has not been met; **planned** means implementation has not reached the gate.

## Phase 1 — Safe public accounts (partial)

Shipped: registration and login, Argon2id passwords, authenticated password changes that revoke other sessions and can revoke every access token, signed-in device visibility, scoped personal access tokens, CSRF/origin checks, and basic login throttling.

Remaining gate: verified email, password recovery, TOTP MFA and recovery codes, step-up authentication for destructive actions, security-event notifications, invitation controls, stronger account/IP abuse throttles, and operator-visible abuse decisions.

## Phase 2 — Everyday repository work (partial)

Shipped: public/private repository creation, collaborators and roles, real bare Git storage, smart HTTP clone/fetch/push, branch/tag transport, tree and text browsing, raw downloads, repository and file-specific history, lifecycle controls, friendly CLI commands, and browser file creation/editing/deletion with atomic stale-head protection.

Remaining gate: file rename/upload, sanitized Markdown, syntax highlighting, blame, tags and compare UI, repository code search, import/export, storage accounting and quotas, maintenance, orphan reconciliation, SSH transport, and LFS.

## Phase 3 — Collaboration and protected delivery (partial)

Shipped foundation: issues, assignees, labels, milestones with due dates and progress, discussions, issue following, Unite following, and an in-app inbox for issue and Unite events; same-repository Unite requests, diffs, discussions, close/reopen, formal approve/request-changes reviews with stale-head visibility, per-branch Merge Guards requiring fresh approvals and blocking current change requests, optional direct-push/browser-edit protection that requires Unite for a branch, merge commits, expected-SHA checks, and interrupted-merge recovery.

Shipped in this slice: draft Unite requests, inline comments on an exact commit/file/side/line with replies, resolution, and outdated marking, requested reviewers, review dismissal with an audit event, squash/rebase/merge plus optional source-branch deletion, Unite assignees/labels/linked issues, auto-close from the Unite body, linked issues, and commit messages when no blocker is open, a timeline, diff pagination, invitations with a durable email outbox, and ownership-transfer confirmation. Branch rules can require resolved conversations, an up-to-date branch, owner or maintainer approval, successful check contexts, signed commits, and owner/maintainer-only pushes. The same rules apply to browser edits, API merges, and HTTPS pushes. Require Unite stays off unless an owner enables it. Force pushes and branch deletions stay rejected globally.

Remaining gate: a merge queue, forks (Remixes), cross-repository Unite requests, and SSH. Crews do not exist, so maintainer approval means the owner or a member with the maintain role. Email is stored in an outbox and is handed to SMTP only when GITOWN_SMTP_ADDR and GITOWN_SMTP_FROM are set; without SMTP, immediate messages are recorded as sent and are not delivered to an inbox.

## Phase 4 — Boards and planning (partial)

Shipped foundation: a repository issue board with To do, In progress, and Done columns, synchronized with issue close/reopen actions; owner-managed issue templates prefill new issues; same-repository issue dependencies reject cycles and block completion while prerequisites remain open.

Shipped in this slice: comment editing with history, label and text filtering, bug/feature/custom template kinds, duplicate marking, pinning, issue transfer between repositories the same owner controls, priority, estimate, due date, and iteration, saved searches, and board, table, and roadmap views with drag-and-drop on the three columns. Dependencies remain the sub-issue model. `#42` references are listed from the issue body and comments. Closing keywords in a Unite request or its commits can close an issue.

Remaining gate: district-wide boards. Districts are not implemented, so boards stay inside one repository.

## Phase 5 — Social and discovery (partial)

Shipped foundation: public builder profiles, configurable repository showcases, one-click repository Sparks with visible counts, builder follows with counts and self-follow protection, a public following feed for repository creation/Sparks/issues/Unite requests, owner-managed repository topics, public repository text/topic search with Spark sorting and 30-day trending, and paged public builder search by username/name/bio.

Shipped in this slice: watch, participate, and ignore subscription modes; mentions; assignment, review-request, merge, check, invitation, and ownership notifications; mark-all-read; configurable email preference with unsubscribe tokens; profile skills, availability, and open-to-collaborators; contribution counts and badges computed from public merged Unite requests, approvals, closed issues, and public repositories; repository homepage and tech stack; Explore search for public issues and Unite requests; and an open-to-collaborators builder filter.

Remaining gate: code search, custom domains, release download counts, and district boards. Badges are not commit counts.

## Later phases (planned)

6. Districts and Crews: organizations, teams, ownership, policies, and enterprise administration.
7. Drops and Crates: releases, assets, package registries, provenance, retention, and vulnerability data.
8. Routes: sandboxed automation runners, workflow definitions, logs, artifacts, secrets, cancellation, quotas, and check integration.
9. Integrations and operations: durable events, signed webhooks, apps, observability, scheduled maintenance, online backups, restore drills, and deployment verification.
10. Ecosystem and scale: Showcase pages, wikis, Town Hall discussions, billing, compliance exports, high availability, and multi-region recovery.

The naming contract is defined in [NAMING.md](NAMING.md). Standard Git protocol and commands remain compatible even when GITOWN presents friendlier names in its UI and CLI.
