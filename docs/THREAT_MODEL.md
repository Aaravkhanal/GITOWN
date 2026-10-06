# GITOWN threat model for independent review

Status: review preparation only; this is not an independent assessment. Scope is the current development-alpha code at the exact commit supplied to the reviewer. Re-check this document against that commit before review.

## System and trust boundaries

GITOWN is a single Go API and worker process with PostgreSQL metadata, filesystem-backed bare Git repositories, and a Next.js web client. Git HTTP and the optional forced-command SSH gateway pass untrusted protocol input to native Git subprocesses. A reverse proxy may terminate TLS and forward client identity. SMTP, webhook receivers, advisory feeds, and public forge hosts are external services. Operators control deployment secrets, backups, repository storage, and the database. Routes currently parses and records workflow plans; it must not execute repository-authored commands.

Trust boundaries to test explicitly:

1. Anonymous/authenticated clients → web/API: cookie/session, bearer token, CSRF/origin, MFA, rate limits, and private-resource authorization.
2. Git clients → smart HTTP/SSH → Go process → Git subprocess/storage: protocol parsing, ref updates, quotas, hooks/config isolation, path handling, concurrency, and process resource exhaustion.
3. API/workers → PostgreSQL and filesystem: transaction boundaries, leases/retries, secret encryption, migration behavior, permissions, crash consistency, and backup/restore.
4. Server → reverse proxy and outbound services: forwarded-IP trust, TLS, SSRF/DNS rebinding, webhook/email retries, and disclosure in errors/logs.
5. CI/release/operator → artifacts and production: dependency/build provenance, credential scope, deployment configuration, backups, access logs, and incident response.

## Assets and security objectives

- Private source, LFS objects, issue/review discussions, and repository membership metadata remain visible only to authorized principals.
- Account passwords, sessions, access tokens, MFA seeds/recovery codes, OAuth credentials, webhook secrets, and district secrets remain resistant to theft and replay.
- Repository refs and collaboration decisions remain consistent under concurrent pushes, merges, retries, and process crashes.
- User-controlled input cannot escape repository storage, invoke arbitrary host commands, bypass branch rules, or access internal services through outbound fetch/webhook features.
- Operators can identify abuse and recover a consistent database + Git-storage state without silently destroying the source installation.
- Availability is bounded: untrusted Git/API inputs cannot exhaust CPU, memory, disk, process slots, or worker capacity without detection and containment.

## Adversaries and assumptions

Consider an unauthenticated Internet client, a malicious registered user or collaborator, a stolen/replayed credential, a malicious repository author, a compromised webhook/import target, and an operator or CI misconfiguration. Assume TLS and host/OS patching are deployment responsibilities; verify these assumptions rather than treating them as controls supplied by the application. A database or host administrator can access application data. A public beta must not rely on secrecy of repository IDs or client-side filtering.

## High-value abuse cases for review

- Read or mutate another user's private repository through API, Git HTTP, SSH, cached content, search, exports, or races in membership/ownership changes.
- Bypass branch protections or stale-head review invalidation through concurrent ref changes, alternate transports, web commits, or merge recovery.
- Steal/replay sessions, recovery links, MFA/recovery codes, PATs, OAuth grants, app tokens, or secret values via logs, browser state, redirects, CSRF, or operator APIs.
- Exploit Git parser/subprocess behavior, oversized/delta-heavy packs, malicious refs/objects, symlinks, hooks/config, or path traversal to execute code or consume host resources.
- Use URL import, webhook delivery, redirects, DNS changes, IPv6/private address encodings, proxy headers, or advisory lookup to reach internal services or disclose private dependency metadata.
- Abuse queue leases/retries, duplicate webhook delivery, worker crash recovery, or partial database/filesystem writes to lose or replay security-sensitive state.
- Restore inconsistent, tampered, or attacker-controlled snapshots; overwrite live recovery targets; or expose sensitive backup contents.
- Treat Routes metadata as executable authority; demonstrate that no code path executes untrusted `run`/`uses` declarations.
- Exhaust shared-process rate limits, database pools, disk/inodes, worker slots, or native Git resources with authenticated or anonymous load.

## Existing controls and explicit gaps

The project documents and tests Argon2id credentials, hashed random tokens, session expiry/revocation, MFA and step-up controls, repository access checks, origin checks, bounded Git operations, hook/config isolation, ref compare-and-swap, request/rate limits, SSRF-checked webhook targets, encrypted secrets, leased outboxes, backup checksums, and restore refusal on occupied targets. These controls require independent verification on the reviewed commit; this list is not a claim that each is sufficient.

Known or deployment-dependent gaps include: no safe executor for untrusted workflows; process-level rather than host-enforced Git resource isolation; API rate limiting is per process; production SMTP/proxy/TLS/alerting/retention configuration is operator-owned; production-scale and multi-instance load have not been established; CI restore does not prove production storage, off-host encryption, RPO/RTO, or failover; and no independent security review has occurred.

## Reviewer evidence requested

For every finding, include severity, impact, prerequisites, affected commit/files, reproducible steps or a safe proof, and recommended mitigation. Explicitly identify areas not tested. Confirm whether the reviewer verified authorization denials across UI/API/Git transports, secrets/logging, adversarial Git inputs, SSRF boundaries, worker recovery, restore integrity, and the disabled workflow-execution boundary. Track remediation and retest separately; do not mark public beta approved while critical/high findings remain unresolved.
