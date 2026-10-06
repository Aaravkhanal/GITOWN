# Independent security review brief

This document prepares a review request; it is **not** a review, certification, or public-beta approval. GITOWN must remain a development alpha until an independent reviewer has completed this work and the owner has resolved the findings under [release gates](RELEASE_GATES.md).

## Reviewer and environment

Use an external security professional with no authorship or implementation responsibility for the reviewed code. Give the reviewer a pinned commit SHA, the [threat model](THREAT_MODEL.md), this scope, the [closeout template](SECURITY_REVIEW_FINDINGS.md), a private test deployment with synthetic accounts/data, and a safe disclosure channel. Do not give production credentials or ask them to test against production users or third-party infrastructure without explicit authorization.

## Review scope

- Account registration, password reset, email verification, MFA/recovery codes, step-up checks, sessions, access tokens, OAuth flows, and abuse throttles.
- Repository/review authorization, private-repository isolation, branch protections, Git smart HTTP/SSH gateway parsing, concurrent ref updates, object limits, hooks, and filesystem/process boundaries.
- Import URL validation, DNS rebinding/SSRF boundaries, webhooks, redirects, proxy trust, uploads, LFS, package/OCI routes, wiki assets, static showcase, and rendered Markdown.
- Database migrations, transaction/queue behavior, worker leases/retries, secret encryption/key rotation, audit records, recovery/restore, and operator APIs.
- Routes parsing/control plane, explicitly verifying that no repository-authored command can execute and that future execution remains disabled.
- Deployment defaults, container permissions, TLS/proxy assumptions, CI/release permissions, dependency supply chain, logs/metrics privacy, and documented operational failure modes.

## Required deliverable and closeout

The report should identify the exact reviewed commit, methods and test environment, findings with severity and reproducible impact, affected components, recommended mitigations, and any scope exclusions. The owner tracks each finding to a fix or documented risk acceptance. Critical/high findings block public beta; fixed findings require reviewer retest where practical. Record the final report URL and closeout decision in protected deployment configuration as `GITOWN_INDEPENDENT_SECURITY_REVIEW` only after the independent work is complete.

## Review readiness checklist

- [ ] Select an independent reviewer and agree on scope, timeline, disclosure, and a safe test environment.
- [ ] Pin the commit SHA and deployment image/build identity; freeze code changes during the review or explicitly record changes that invalidate review coverage.
- [ ] Provide synthetic test accounts covering anonymous, owner, collaborator, read-only token, operator, MFA, and private-repository cases.
- [ ] Provide architecture, threat model, API docs, security/deployment boundary, release gates, and this findings template.
- [ ] Remove production secrets and real user data from the test environment and reviewer materials.
- [ ] Record every finding, owner, remediation commit, regression test, and reviewer retest/disposition.
- [ ] Keep public beta disabled until critical/high findings are closed and the owner signs the final risk decision.
