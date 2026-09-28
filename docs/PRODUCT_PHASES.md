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

Shipped foundation: issues, labels and discussions; same-repository Unite requests, diffs, discussions, close/reopen, merge commits, expected-SHA checks, and interrupted-merge recovery.

Remaining gate: formal and inline reviews, assignees, milestones, mentions, subscriptions, notifications, branch rules, required approvals/checks/resolved conversations, squash/rebase, source-branch deletion, a merge queue, forks (Remixes), and cross-repository Unite requests.

## Later phases (planned)

4. Boards and planning: project boards, iterations, dependencies, templates, and saved views.
5. Social and discovery: profiles, follows, Sparks, activity feeds, topics, trending, and advanced search.
6. Districts and Crews: organizations, teams, ownership, policies, and enterprise administration.
7. Drops and Crates: releases, assets, package registries, provenance, retention, and vulnerability data.
8. Routes: sandboxed automation runners, workflow definitions, logs, artifacts, secrets, cancellation, quotas, and check integration.
9. Integrations and operations: durable events, signed webhooks, apps, observability, scheduled maintenance, online backups, restore drills, and deployment verification.
10. Ecosystem and scale: Showcase pages, wikis, Town Hall discussions, billing, compliance exports, high availability, and multi-region recovery.

The naming contract is defined in [NAMING.md](NAMING.md). Standard Git protocol and commands remain compatible even when GITOWN presents friendlier names in its UI and CLI.
