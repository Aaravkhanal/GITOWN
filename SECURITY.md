# Security and deployment boundary

GITOWN is a development alpha. Do not expose it as a public multi-tenant service yet. This document distinguishes controls present in the application from deployment work and release blockers; it is not a security certification. Report vulnerabilities privately to the repository owner and avoid publishing credentials or private source code in an issue.

## Implemented controls

The application includes Argon2id password hashing; random hashed credentials; expiring and revocable sessions/tokens; HTTP-only same-site cookies and exact-origin checks for JSON mutations; private-repository authorization across API and Git access; collaborator roles; MFA with single-use recovery codes and rotation; step-up authentication for sensitive actions; password recovery and optional verified-email enforcement; shared database-backed account/IP authentication throttles; and redacted operator visibility into abuse decisions.

Git and repository controls include scoped personal access tokens, branch protections and commit-bound reviews, bounded Git request sizes/deadlines/concurrency, receive checks, disabled ambient Git configuration and hooks, argument-array subprocess invocation, atomic ref comparison for merges, repository/account quotas with over-quota push rollback, and audited operator dry-run/quarantine/restore for orphaned storage. SSH uses a forced-command gateway with user/deploy keys; it is not an embedded SSH server. Routes is planning/control-plane functionality only: repository-authored commands are not executed. Public HTTPS import is limited to supported public forges; it does not accept forge credentials.

These are application controls, not proof against all attacks. Git remains a large native parser of untrusted data. Process-level time/concurrency limits do not provide host-enforced CPU, memory, disk, or process isolation. A compromised host or database administrator can access application data.

## Deployment requirements and release blockers

Before any shared deployment, an operator must configure HTTPS/TLS termination, secure cookies, trusted proxy CIDRs, a stable sufficiently strong `GITOWN_SECRET_KEY`, PostgreSQL credentials and network isolation, durable repository storage, operator accounts, SMTP delivery, backup policy, log retention, and alert receivers. Email verification is optional in application configuration; production identity policy must explicitly decide whether to require it and verify actual SMTP delivery. Tune abuse thresholds against the expected deployment and keep operator access restricted.

Before public beta, all of the following remain release gates (see [release gates](docs/RELEASE_GATES.md)):

- An independent security professional must review the current threat model and implementation, document findings, and verify critical/high remediations or explicitly accepted mitigations.
- Run and retain a restore drill in the target deployment environment, including coordinated database and Git storage recovery, off-host backup protections, and measured RPO/RTO.
- Perform representative multi-instance load, soak, and failure testing; establish abuse budgets and service SLOs. The API limiter is process-local, and the current CI smoke test is bounded and not a capacity claim.
- Complete production TLS/proxy, SMTP, alert delivery, storage/backup, quota/retention, and incident-response configuration and verification.
- Review Git process isolation and enforce host-level resource limits before accepting untrusted public workloads. Existing application quotas and subprocess limits are not an OS sandbox.

The frontend's current content-security policy permits inline/eval scripts for development compatibility; review and tighten the production policy before public exposure. Alert dashboard assets and CI checks require deployment-specific receivers, monitoring, and actual CI execution; checked-in configuration alone is not operational evidence.

## Explicitly unsupported or deferred

No repository-authored workflow execution or hosted runners are available; no custom Git hooks or HTML previews are supported. Routes declarations remain inert. Embedded sshd, private-forge import/authentication, web push delivery, high availability, and card billing are not implemented. Public HTTPS import and Git bundle export do exist; imports are limited to supported public forge URLs and do not carry credentials. Browser file edits support bounded text, not binary editing; LFS does not support multipart transfer.

## Credential handling

Never commit `.env`, tokens, database dumps, or `.data/`. Tokens are shown once and expire according to their configured lifetime. Never place a token in a Git remote URL; use the operating system's Git credential helper when persistent local credentials are needed. Do not include secrets, recovery codes, webhook payloads, or private repository content in logs, support messages, or public issues.

## Development database warning

The local development PostgreSQL configuration may use loopback-only trust authentication. It is not appropriate for a shared or production host. Use deployment-managed credentials, network restrictions, encrypted backups, and least-privilege access for any shared installation.
