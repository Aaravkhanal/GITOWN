# Release gates

These gates describe evidence required for a public beta. A phase stays **partial** until each shipped capability has its authorization rules, API contract/behavior, user-facing UI where applicable, automated tests (including denial and failure paths), and current documentation. A plan or control-plane record is not evidence that its execution path is safe.

## Automated checks

- `.github/workflows/ci.yml` runs on pushes, pull requests, manual dispatch, and weekly. Its PostgreSQL service runs race-enabled Go tests, vet, frontend checks/build, browser workflows, and `npm run test:operations`.
- The operations suite creates isolated source and restore databases, makes a real PostgreSQL custom dump, copies a bare Git repository, restores both, checks the restored database row and Git ref, and verifies refusal to overwrite. A corruption test asserts checksum failure happens before the selected storage target is touched.
- The deployment preflight checks HTTPS service URLs, PostgreSQL configuration, stable secret-key length, operator visibility, verified-email/SMTP pairing, and proxy allowlists. It does not print secret values. If `GITOWN_PUBLIC_BETA=true`, it also requires an HTTPS reference to the independently produced security-review record. Keep this variable and the review reference in protected deployment configuration; do not set them until an external review has actually completed.
- The deployment health check probes `/livez` and `/readyz`, requires their expected JSON status and `nosniff`, and allows HTTP only for loopback smoke tests. CI exercises it against the running local API before browser tests.
- Rate-limit tests cover denial and retry headers, client identity isolation, expiry, credential redaction, and concurrent bursts under the race detector. SMTP protocol tests use an in-memory pipe so they do not need a loopback listener.
- CI is configured to build both Compose images and run a bounded GET-only load smoke across two API instances sharing the disposable CI database/repository storage. This catches build regressions and basic availability/latency failures; it is not a multi-instance soak or a beta capacity claim.

## Public-beta blockers (not waived by CI)

1. An independent security professional must review the threat model and the implementation, record findings and severity, and verify fixes or accepted mitigations. Use the [review brief](SECURITY_REVIEW_SCOPE.md). The project owner must link that report from protected deployment configuration before enabling the public-beta gate. The automated check only verifies the presence of that reference; it does not validate the reviewer or report contents.
2. Run and retain the deployment-environment restore drill in [backup and restore](operations/backup-restore.md). `scripts/restore-drill.mjs` verifies isolated HTTPS health, a restored account session, anonymous denial/authenticated access for a private repo, Git transport, and known issue/Unite records. CI does not test the production storage backend, off-host encryption, backup schedule, measured RPO/RTO, or real traffic cutover.
3. Establish abuse budgets and service SLOs using representative multi-instance load/soak/failure tests against disposable infrastructure. The in-process API limiter is per process; authentication throttles are database-backed. CI stress coverage proves local decisions and race safety, not multi-instance capacity or a public-hosting limit.
4. Resolve documented deployment gaps (TLS termination, trusted proxy CIDRs, SMTP delivery, operator alert receiver, database/storage backup strategy, quotas, retention, and incident response) for the actual hosting environment.

## Current honest status

Phases 11 and 12 remain partial. Routes still executes no repository-authored command; push delivery, private-forge imports, and HA are not implemented. Billing, enterprise identity, and sandbox execution remain explicit product/security decisions. A green CI run does not make the product approved for public multi-tenant hosting.
