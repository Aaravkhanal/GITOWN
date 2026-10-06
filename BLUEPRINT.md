# GITOWN — Product and Engineering Blueprint

> Status: development alpha; the [product scope decisions](docs/PRODUCT_SCOPE.md), [phase ledger](docs/PRODUCT_PHASES.md), and [release gates](docs/RELEASE_GATES.md) define current commitments and verified scope.
> Repository: `https://github.com/Aaravkhanal/GITOWN`  
> Ownership: an original, independent project owned by Aarav Khanal  
> Working principle: build a dependable Git collaboration platform in deliberate stages instead of attempting every GitHub feature at once.

## 1. Vision

GITOWN will be a web-based Git hosting and collaboration platform where a user can:

- create public or private repositories;
- clone, fetch, pull, and push with normal Git clients;
- browse files, commits, branches, and tags in a browser;
- create branches and commits;
- open, review, approve, and merge pull requests;
- protect important branches with rules and required checks;
- manage issues, labels, milestones, releases, notifications, and repository settings;
- automate tests and deployments in a later phase.

The product should feel familiar to GitHub users, but its code, visual identity, naming, and implementation must be original. We can study public specifications and product behavior, but we should not copy proprietary GitHub code, branding, text, or assets.

## 2. Scope and Product Strategy

“Everything GitHub can do” is a multi-year product containing Git hosting, social collaboration, CI/CD, package registries, security scanning, project management, search, billing, and enterprise administration. GITOWN is not committing to feature-count parity. It prioritizes portable Git, safe collaboration/review, approachable browser/CLI workflows, and privacy-conscious project discovery. The [product-scope decision record](docs/PRODUCT_SCOPE.md) identifies what is core, staged, deferred, or explicitly out of scope. The practical strategy is:

1. Build a secure, single-node Git hosting MVP.
2. Add collaboration and repository governance.
3. Keep automation in planning mode until reviewed isolation is proven; external CI remains supported through integrations.
4. Scale storage, search, background work, and availability only after usage requires it.

### MVP: the first useful release

The MVP is complete when a user can:

- register, sign in, sign out, reset a password, and enable a passkey or TOTP;
- add an SSH key and create a personal access token;
- create, rename, archive, and delete a public or private repository;
- clone/fetch over HTTPS and SSH;
- push branches and tags over HTTPS and SSH;
- browse a repository tree and view files, rendered Markdown, commit history, diffs, branches, and tags;
- open a pull request between branches in the same repository;
- review a pull request, comment on lines, approve it, and merge using merge commit, squash, or rebase;
- create and discuss issues;
- control owner, maintainer, write, triage, and read permissions;
- receive in-app notifications;
- inspect an immutable audit record for security-sensitive actions.

### Explicitly postponed until after MVP

- public forks and cross-repository pull requests;
- organizations and teams;
- hosted CI/CD runners;
- package/container registries;
- code spaces or cloud development environments;
- wikis, discussions, project boards, pages hosting, sponsorships, and marketplaces;
- advanced code search, secret scanning, dependency analysis, and AI features;
- billing, quotas, enterprise federation, and multi-region operation.

Postponing these keeps the first release achievable without designing them out of the future architecture.

## 3. Recommended Technology Stack

Use a **modular monolith** initially: one deployable backend with strict internal modules and background workers. This minimizes operational complexity while preserving a path to split high-load modules later.

| Area                 | Initial choice                                                                                 | Reason                                                                                                                         |
| -------------------- | ---------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Web application      | Next.js + TypeScript                                                                           | Strong server-rendered UI, routing, accessibility tooling, and a mature component ecosystem                                    |
| Core backend         | Go                                                                                             | Good fit for streaming Git traffic, subprocess control, concurrency, and a small production footprint                          |
| API style            | REST/JSON plus OpenAPI                                                                         | Simple for browser and CLI clients; generates typed clients and testable contracts                                             |
| Primary database     | PostgreSQL                                                                                     | Transactions, constraints, JSON support, full-text search for the first release, and reliable migrations                       |
| Cache/queues         | Redis initially                                                                                | Rate limits, short-lived cache, sessions, and a simple job queue; jobs remain durable in PostgreSQL where loss is unacceptable |
| Git object storage   | Bare repositories on encrypted local/block storage for MVP                                     | Git itself owns packfiles and refs; simplest reliable single-node starting point                                               |
| Attachments          | S3-compatible object storage                                                                   | Appropriate for avatars, issue attachments, release assets, logs, and artifacts                                                |
| Git transport        | Native `git-http-backend`/`upload-pack`/`receive-pack` integration and OpenSSH forced commands | Reuse the audited Git implementation instead of reimplementing the wire protocol                                               |
| Reverse proxy        | Caddy or Nginx                                                                                 | TLS termination, upload limits, timeouts, buffering rules, and request routing                                                 |
| Observability        | OpenTelemetry + Prometheus + structured logs                                                   | Traces, metrics, and searchable request/audit context without vendor lock-in                                                   |
| Local environment    | Docker Compose                                                                                 | Repeatable PostgreSQL, Redis, object storage, mail catcher, backend, and frontend setup                                        |
| Production packaging | OCI containers                                                                                 | Portable deployments; Kubernetes is unnecessary for the first release                                                          |

Do not build Git object parsing or the Git wire protocol from scratch. Git already supplies server-side plumbing for smart HTTP and SSH. GITOWN’s responsibility is authentication, authorization, repository routing, policy checks, hooks, metadata, and presentation.

## 4. System Architecture

```text
Git CLI / Browser / Future CLI
           |
       TLS proxy
           |
  +--------+-------------------------+
  |                                  |
Web/API application             Git transport gateway
  |                                  |
  |                           HTTPS smart Git / SSH
  |                                  |
  +------------ authorization -------+
           |
  Modular Go application
  | identity | repositories | code browser | pull requests |
  | issues   | reviews      | notifications | audit         |
           |
  +--------+---------+-------------+----------------+
  |                  |             |                |
PostgreSQL         Redis      Bare Git repos   Object storage
  |                  |             |                |
  +---------------- background workers -------------+
```

### Core rule: Git data and application metadata are different

- Git commits, trees, blobs, tags, packfiles, and refs live in bare Git repositories.
- Users, permissions, repository settings, pull requests, reviews, issues, checks, notifications, and audit events live in PostgreSQL.
- Avatars, attachments, release assets, CI logs, and artifacts live in object storage.
- Derived data such as rendered Markdown, commit metadata, diff summaries, and search documents may be cached or indexed but must be reproducible from the source data.

## 5. Backend Modules

Each module owns its schema, service interface, authorization rules, and tests. Modules communicate through explicit service calls and durable domain events, not direct writes into each other’s tables.

### Identity and access

- accounts, verified email addresses, sessions, password reset, passkeys/TOTP;
- SSH public keys, personal access tokens, token scopes, expiry, rotation, and revocation;
- repository roles and explicit grants;
- organization/team permissions in a later phase;
- centralized policy evaluation: `actor + action + resource -> allow/deny + reason`.

### Repository management

- repository lifecycle, visibility, default branch, features, merge methods, and archival;
- on-disk repository locator using opaque repository IDs rather than untrusted names;
- atomic repository creation and rename;
- maintenance jobs: `git gc`, repack, commit-graph generation, integrity checks, and backup snapshots;
- quotas for repository size, LFS, attachments, and request rates.

### Git transport gateway

- authenticate HTTPS using a personal access token and SSH using a registered public key;
- authorize read or write access before exposing a repository path;
- invoke `git-upload-pack` for fetch/clone and `git-receive-pack` for push;
- stream request/response bodies without loading packfiles into memory;
- pass protocol v2 headers correctly;
- enforce request size, duration, concurrency, and repository quotas;
- execute pre-receive policy checks and emit post-receive indexing/events;
- never accept arbitrary command, environment, or filesystem paths from users.

### Code browsing

- list refs, commits, trees, blobs, blame data, and history;
- render Markdown through a strict sanitizer;
- syntax-highlight code with size/time limits;
- serve raw blobs safely with correct content types and download headers;
- compute diffs asynchronously when expensive and cache by immutable commit IDs.

### Pull requests and reviews

- immutable base/head repository IDs plus branch names and captured commit SHAs;
- title, description, state, draft status, reviewers, labels, timeline, and participants;
- merge-base calculation and changed-file/diff views;
- review comments anchored to file, side, line, and commit; preserve outdated comments;
- approvals, requested changes, conversation resolution, checks, and mergeability;
- merge commit, squash, and rebase methods;
- optimistic locking so a stale head SHA cannot be merged accidentally;
- atomic ref update after policy evaluation, followed by a durable event/outbox record.

### Issues and planning

- issues, comments, assignees, labels, milestones, references, and close/reopen actions;
- Markdown sanitization, edit history, reactions, and notification subscriptions;
- cross-link commits, pull requests, and issues through structured references.

### Events, jobs, and notifications

- transactional outbox table written in the same transaction as state changes;
- idempotent workers with bounded retries and a dead-letter state;
- in-app and email notifications based on subscriptions and mention rules;
- signed, retried webhooks after MVP;
- every job includes correlation ID, actor ID, resource ID, attempt count, and trace context.

### Audit

- append-only records for login changes, credentials, permissions, repository lifecycle, protected refs, merges, webhook changes, and administrative actions;
- record actor, action, target, result, IP, user agent, correlation ID, and timestamp;
- redact secrets and sensitive payloads from both audit records and application logs.

## 6. Critical Git Workflows

### Repository creation

1. Validate the owner/name and reserve it with a unique database constraint.
2. Generate an opaque repository ID and resolve an internal storage path.
3. Initialize a bare repository in a temporary sibling directory.
4. Apply safe Git configuration and install controlled hooks.
5. Atomically rename it into place.
6. Commit repository state in PostgreSQL and emit an audit/outbox event.
7. A reconciliation worker repairs or reports any storage/database mismatch.

### Clone and fetch

1. Resolve the URL to a repository ID without using the URL text as a raw path.
2. Authenticate when the repository is private.
3. Authorize read access.
4. Invoke `git-upload-pack` through smart HTTP or the forced SSH command.
5. Stream bytes with cancellation, rate limiting, and observability.

### Push

1. Authenticate and authorize repository write access.
2. Invoke `git-receive-pack` in an isolated, tightly configured process.
3. In `pre-receive`, parse proposed ref updates and evaluate branch/tag policies.
4. Reject the entire push with a useful message if any required rule fails.
5. On success, append a compact post-receive event to a durable spool/outbox.
6. Workers update derived commit/ref data, notifications, webhooks, and search indexes.

The pre-receive path must be fast and deterministic. Network calls and slow indexing belong after the ref update, not inside the critical push lock.

### Pull-request merge

1. Lock or compare-and-swap the pull request using its expected version and head SHA.
2. Recompute mergeability against the current base ref.
3. Re-evaluate permissions, reviews, required checks, unresolved conversations, and branch rules.
4. Create the requested merge result in a temporary work area.
5. Run a final atomic ref update only if the base ref still matches the expected old SHA.
6. Mark the pull request merged and write the timeline/audit/outbox records.
7. Optionally delete the source branch in a separate authorized operation.

## 7. Branch Rules and Merge Policy

Rules should be composable and evaluated by one policy engine. Support, in order:

- deny force pushes and branch deletion;
- require a pull request;
- require a minimum number of approvals;
- dismiss stale approvals after new commits;
- require review from configured code owners;
- require all conversations to be resolved;
- require named status checks;
- require the branch to be up to date;
- require signed commits;
- restrict who may push or merge;
- require linear history;
- apply rules to administrators too.

A merge queue is a post-MVP feature. It should test speculative merge groups against the latest target branch and only advance the target ref when required checks succeed.

## 8. Initial Data Model

The exact schema will evolve, but these are the primary entities:

| Domain        | Core tables                                                                                       |
| ------------- | ------------------------------------------------------------------------------------------------- |
| Identity      | `users`, `emails`, `sessions`, `password_credentials`, `mfa_methods`, `ssh_keys`, `access_tokens` |
| Ownership     | `namespaces`, later `organizations`, `teams`, `team_members`                                      |
| Repositories  | `repositories`, `repository_members`, `repository_settings`, `branch_rules`, `storage_locations`  |
| Pull requests | `pull_requests`, `pull_request_commits`, `reviews`, `review_comments`, `merge_attempts`           |
| Issues        | `issues`, `issue_comments`, `labels`, `issue_labels`, `milestones`, `assignees`                   |
| Checks        | `check_suites`, `check_runs`, `commit_statuses`                                                   |
| Activity      | `timeline_events`, `notifications`, `subscriptions`, `reactions`                                  |
| Integration   | `webhooks`, `webhook_deliveries`, `outbox_events`, `background_jobs`                              |
| Governance    | `audit_events`, `abuse_reports`, `reserved_names`                                                 |

Database rules:

- use UUIDv7 or equivalent sortable opaque IDs;
- use case-normalized unique keys for usernames and repository names;
- keep immutable actor snapshots where historical display matters;
- use foreign keys and database constraints for invariants, not only application checks;
- use soft deletion only where recovery, legal retention, or references require it;
- use explicit schema migrations with forward and rollback procedures;
- avoid storing access tokens, reset tokens, or webhook secrets in plaintext.

## 9. API Surface

Version the public API from the start: `/api/v1`.

Initial resource families:

```text
/auth/*
/users/{username}
/user/keys
/user/tokens
/repos/{owner}/{repo}
/repos/{owner}/{repo}/git/refs
/repos/{owner}/{repo}/commits
/repos/{owner}/{repo}/contents/{path}
/repos/{owner}/{repo}/branches
/repos/{owner}/{repo}/tags
/repos/{owner}/{repo}/pulls
/repos/{owner}/{repo}/pulls/{number}/reviews
/repos/{owner}/{repo}/issues
/repos/{owner}/{repo}/rules
/repos/{owner}/{repo}/hooks
/notifications
```

API requirements:

- publish an OpenAPI specification and generate clients/types from it;
- cursor pagination for growing collections;
- stable machine-readable error codes plus safe human messages;
- idempotency keys for retryable create/merge operations;
- strong ETags or version fields for concurrent edits;
- scoped authorization on every endpoint, including nested resources;
- rate-limit headers and correlation IDs;
- no secrets in URLs, logs, analytics, or error responses.

Git smart-HTTP endpoints are protocol endpoints, not JSON APIs, and must preserve Git content types, status behavior, streaming, and `Git-Protocol` headers.

## 10. Authentication and Permission Model

### Browser authentication

- Argon2id password hashing with unique salts and tuned parameters;
- secure, HTTP-only, same-site cookies and server-side session revocation;
- CSRF defense for cookie-authenticated mutations;
- verified email before sensitive actions;
- passkeys/WebAuthn preferred, TOTP as an additional MFA option;
- recovery codes stored as hashes and shown once.

### Git authentication

- HTTPS: username plus scoped personal access token; never accept account passwords;
- SSH: public-key lookup followed by a forced command that accepts only a validated Git operation and repository identifier;
- token and key usage timestamps, expiry, revocation, and audit history;
- least-privilege scopes such as `repo:read`, `repo:write`, `user:keys`, and `admin:repo`.

### Repository roles

| Role        | Intended access                                                      |
| ----------- | -------------------------------------------------------------------- |
| Owner/Admin | Settings, access, rules, deletion, and all repository operations     |
| Maintainer  | Repository management excluding ownership/destructive controls       |
| Write       | Push to allowed refs; create and manage normal collaboration content |
| Triage      | Manage issues and pull requests without code write access            |
| Read        | View/clone and participate where allowed                             |

Every operation must be authorized on the server. Hiding a UI control is never an authorization boundary.

## 11. Security Baseline

This platform stores source code and executes Git operations, so security is part of the architecture rather than a final checklist.

- threat-model authentication, Git transports, repository path resolution, hooks, Markdown, uploads, webhooks, runners, and administrative actions;
- run services as unprivileged users with read/write access only to required directories;
- use opaque internal paths and prevent traversal, symlink escape, and argument injection;
- isolate Git subprocesses with time, CPU, memory, file-count, and process limits;
- keep platform-controlled hooks non-writable by repository users;
- sanitize all rendered user HTML and block dangerous URL schemes;
- apply CSP, HSTS, secure cookies, clickjacking defense, and strict MIME handling;
- encrypt transport everywhere and encrypt backups/secrets at rest;
- store application secrets in a secret manager, not the repository or container image;
- scan first-party dependencies and container images, pin versions, and publish an update policy;
- rate-limit login, token, repository creation, Git, search, and expensive diff endpoints;
- require re-authentication for credential changes and destructive operations;
- make repository deletion a recoverable, delayed purge with clear retention;
- back up PostgreSQL, repository storage, and object storage consistently; regularly test full restores;
- never run user CI workloads on the application host.

### CI runner rule

Hosted automation is remote code execution by design. When implemented, every job must run in a disposable VM or equally strong isolation boundary with:

- no access to control-plane networks, storage, metadata endpoints, or other jobs;
- short-lived credentials and per-job secret delivery;
- outbound network policy, resource/time quotas, artifact limits, and log redaction;
- destroyed compute and workspace after every job;
- separately patched and monitored runner infrastructure.

Containers alone are not an adequate trust boundary for arbitrary public code.

## 12. Repository Layout for the Build Phase

Create this structure only when implementation begins; it is documented here now to avoid adding empty scaffolding prematurely.

```text
GITOWN/
├── BLUEPRINT.md
├── README.md
├── LICENSE
├── SECURITY.md
├── CONTRIBUTING.md
├── .editorconfig
├── .gitignore
├── .env.example
├── compose.yaml
├── Makefile
├── api/
│   └── openapi.yaml
├── apps/
│   ├── web/                  # Next.js application
│   └── server/               # Go entrypoint and composition root
├── internal/
│   ├── auth/
│   ├── policy/
│   ├── repository/
│   ├── gittransport/
│   ├── codebrowser/
│   ├── pullrequest/
│   ├── issue/
│   ├── notification/
│   ├── job/
│   └── audit/
├── migrations/
├── deploy/
│   ├── local/
│   └── production/
├── docs/
│   ├── adr/
│   ├── operations/
│   └── threat-models/
└── tests/
    ├── integration/
    ├── protocol/
    └── end-to-end/
```

Keep the frontend and backend in one repository initially. This makes atomic changes, local development, shared API generation, and release tracking easier.

## 13. Delivery Roadmap

Estimates assume one focused developer using automation responsibly. They are planning ranges, not promises; security review and production hardening can extend them.

### Phase 0 — foundations (1–2 weeks)

- record architecture decisions and define the threat model;
- establish Go/TypeScript workspaces, linting, formatting, tests, and local Compose services;
- implement configuration validation, migrations, health checks, logging, metrics, and CI for GITOWN itself;
- define API conventions and the initial OpenAPI contract.

Exit criteria: a new developer can run the stack from documented commands; CI tests every change; no secrets are committed.

### Phase 1 — identity and repositories (2–4 weeks)

- account/session flows, verified email, MFA foundation, SSH keys, and tokens;
- create/list/view/archive repositories and implement roles;
- safe storage locator, bare repository lifecycle, audit trail, and backup proof of concept.

Exit criteria: private repository metadata and access changes are correctly authorized and audited.

### Phase 2 — real Git transport (3–5 weeks)

- HTTPS clone/fetch/push using smart Git protocol;
- SSH clone/fetch/push with forced commands;
- push policy hooks, ref events, quotas, concurrency controls, and maintenance jobs;
- compatibility tests with command-line Git on macOS, Linux, and Windows.

Exit criteria: two independent users can securely clone and collaborate on public/private repositories with standard Git clients.

### Phase 3 — browser experience (2–4 weeks)

- repository home, tree/blob/raw views, Markdown, syntax highlighting, commits, diffs, branches, and tags;
- responsive and keyboard-accessible UI;
- pagination, caching, large-file safeguards, and clear empty/error states.

Exit criteria: common repository inspection tasks work without the command line and pass accessibility checks.

### Phase 4 — pull requests and reviews (4–7 weeks)

- pull-request lifecycle, diffs, timelines, review comments, approvals, and requested changes;
- mergeability, conflict reporting, three merge methods, branch rules, and required checks data model;
- race-safe merging and end-to-end tests for protected branches.

Exit criteria: protected pull requests cannot merge unless all current rules pass, including under concurrent pushes.

### Phase 5 — issues and notifications (2–4 weeks)

- issues, comments, labels, assignees, milestones, mentions, references, and subscriptions;
- in-app notification inbox and transactional email;
- abuse controls, edit history, moderation foundation, and user preferences.

Exit criteria: collaboration events reliably reach the right subscribers once and do not expose private repository data.

### Phase 6 — integrations and operations (3–6 weeks)

- signed webhooks, delivery history, retries, status/check APIs, and deploy keys;
- production dashboards, alerting, runbooks, backup/restore drills, retention, and quota enforcement;
- load, soak, chaos, and security tests; incident response and disclosure process.

Exit criteria: documented recovery objectives are demonstrated in a restore drill and critical alerts have tested runbooks.

### Phase 7 — automation platform (6–12+ weeks)

- workflow syntax and validation, scheduler, isolated runner protocol, logs, artifacts, secrets, and status integration;
- runner autoscaling, cleanup, network policy, quotas, cancellation, and supply-chain hardening;
- begin with trusted private repositories before considering untrusted public jobs.

Exit criteria: hostile test workloads cannot access the control plane, other jobs, host credentials, or persisted secrets.

### Later expansion

- organizations/teams, forks, cross-repository pull requests, merge queues;
- release assets, LFS, packages, projects, wikis, discussions, pages;
- dedicated search/indexing service and scalable object-backed Git storage;
- billing, enterprise identity, compliance exports, high availability, and multi-region disaster recovery.

## 14. Testing Strategy

### Unit tests

- policy decisions, validation, state machines, merge rules, URL/path handling, token scopes, and event idempotency.

### Integration tests

- PostgreSQL constraints and migrations;
- real bare Git repositories and actual Git binaries;
- HTTPS and SSH authentication/authorization;
- hooks, push rejection, branch rules, object storage, Redis failure, and worker retries.

### Protocol compatibility tests

- clone/fetch/pull/push, tags, shallow clone, protocol v2, large packfiles, interrupted transfers, non-fast-forward updates, and Unicode names;
- multiple supported Git client versions and operating systems.

### End-to-end tests

- user registration through first push;
- create branch -> push -> open pull request -> review -> protected merge;
- private repository access denial and credential revocation;
- issue mention and notification flow;
- repository archive, restore, and delayed deletion.

### Security and reliability tests

- path traversal, command/argument injection, stored XSS, CSRF, SSRF, broken access control, token leakage, and malicious archives/uploads;
- concurrent merges and pushes, worker duplication, database failover behavior, disk pressure, backup consistency, and restore drills;
- fuzz parsers and all untrusted path/ref/URL inputs;
- load tests using realistic packfiles and repository shapes, not only tiny fixtures.

No feature is complete until authorization-denial, audit, observability, migration, and failure-path tests exist.

## 15. Deployment and Operations

### MVP deployment

- one hardened Linux host or small private cluster;
- reverse proxy, web app, API, worker, PostgreSQL, Redis, object store, SSH gateway, and encrypted repository volume;
- separate OS identities and filesystem permissions for services;
- managed PostgreSQL/object storage preferred when budget permits;
- off-host encrypted backups and automated restore verification;
- staging environment with production-like Git transports and migrations.

### Scaling path

1. Scale stateless web/API instances horizontally.
2. Separate background workers by queue and resource profile.
3. Add PostgreSQL replicas for safe read workloads and improve indexes.
4. Shard repositories across storage nodes using stable repository IDs and a location service.
5. Move cold/large Git data to purpose-built object-backed storage only after measurement and careful protocol testing.
6. Introduce dedicated search and notification services when the monolith’s measured load justifies them.

Never share a writable bare repository through a filesystem that cannot provide Git’s required locking and atomic rename semantics.

### Essential service objectives

- availability and latency targets for web/API and Git transports;
- push/fetch success rate and duration by repository size;
- queue age, retry count, and dead-letter count;
- storage capacity, inode usage, repository corruption signals, and backup age;
- database saturation, lock waits, connection pool, and slow queries;
- authentication failures, rate-limit events, and denied policy actions;
- restore point objective (RPO) and restore time objective (RTO), tested rather than assumed.

## 16. Decisions to Make Before Coding

These choices should become short Architecture Decision Records (ADRs):

1. Product name availability and original visual identity.
2. Open-source license or proprietary license for GITOWN.
3. Public SaaS, self-hosted product, or both.
4. Go backend and Next.js frontend confirmation.
5. Initial deployment provider, region, storage type, and budget.
6. Email provider and account-verification policy.
7. Whether repository deletion is recoverable for 7, 30, or 90 days.
8. Supported Git client versions and maximum repository/file/pack sizes.
9. Username/repository naming and reserved-name policy.
10. Public signup versus invitation-only alpha.

Recommended alpha defaults: invitation-only, one production region, no billing, private/public repositories, conservative quotas, recoverable 30-day deletion, and no execution of user-provided workflows.

## 17. First Implementation Slice

When implementation is approved, build one vertical slice before a broad UI:

1. local environment and migrations;
2. one user account and secure session;
3. one private repository record with owner permission;
4. safe creation of one bare repository;
5. one scoped personal access token;
6. HTTPS clone, fetch, and push using standard Git;
7. minimal browser tree and commit view;
8. audit events and integration tests for allowed and denied access;
9. backup and successful restore of both metadata and the repository.

This slice proves the hardest foundation—Git transport, authorization, storage, and consistency—before investing in the full collaboration interface.

## 18. Definition of Done for the MVP

The MVP is ready for a private alpha only when:

- every MVP workflow has end-to-end tests;
- public/private access and every repository role have negative authorization tests;
- clone/fetch/push work through both HTTPS and SSH with supported Git clients;
- merge operations are safe during concurrent pushes;
- branch rules cannot be bypassed by alternate endpoints or Git transports;
- user-rendered content is sanitized and uploads are constrained;
- rate limits, quotas, audit records, dashboards, and alerts are active;
- database, Git repositories, and object storage can be restored together from backup;
- migrations and rollbacks have been exercised in staging;
- secrets can be rotated without data loss;
- a security review has no unresolved critical/high findings;
- operational, incident-response, and account/repository recovery runbooks exist.

## 19. Reference Standards and Documentation

Implementation should use primary specifications and official documentation:

- [Git smart HTTP backend](https://git-scm.com/docs/git-http-backend)
- [Git wire protocol v2](https://git-scm.com/docs/protocol-v2)
- [Git hooks](https://git-scm.com/docs/githooks)
- [Git receive-pack](https://git-scm.com/docs/git-receive-pack)
- [Git update-ref and atomic ref transactions](https://git-scm.com/docs/git-update-ref)
- [GitHub documentation: protected branches](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches) as behavioral research, not code to copy
- [OWASP Application Security Verification Standard](https://owasp.org/www-project-application-security-verification-standard/)
- [OWASP Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
- [WebAuthn Level 3 specification](https://www.w3.org/TR/webauthn-3/)
- [OpenTelemetry documentation](https://opentelemetry.io/docs/)

## 20. Project Guardrails

- GITOWN stays in its own repository and must never share Git history or source files with VIBE-AI or another project.
- Do not copy another forge’s source code unless a future decision explicitly adopts a compatible open-source dependency and records its license and notices.
- Do not use GitHub trademarks or reproduce its interface pixel-for-pixel.
- Keep changes small, reviewed, tested, and reversible.
- Prefer boring, proven infrastructure over premature microservices.
- Treat authorization, repository integrity, and backup restoration as core product behavior.
- Measure before scaling and document each major architectural decision.
- Do not claim feature parity until behavior, failure modes, security, and operations are all verified.

---

**Current direction:** the first vertical slice has been implemented. Continue with the next milestones in [README.md](README.md) and use [implementation status](docs/STATUS.md) to distinguish delivered features from the remaining blueprint.
