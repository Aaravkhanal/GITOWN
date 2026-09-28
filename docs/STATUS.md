# Implementation status — first working slice

Date: 2026-09-28. This file reports implemented behavior, not aspirational parity.

| Blueprint area     | Implemented now                                                                                                                                                                        | Remaining                                                                                            |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Foundation         | Go/Next.js/PostgreSQL, migrations, launcher, health endpoint, JSON startup/error logs, CI definition                                                                                   | OpenTelemetry, metrics, deployment verification, migration checksums and rollback tooling            |
| Identity           | Register/login/logout, salted Argon2id, hashed sessions/tokens, expiry/revocation, login throttling                                                                                    | Email verification, MFA, password reset, invitations, per-account/device management                  |
| Repository storage | Bare Git repositories, generated IDs, optional README, private/public visibility, owner settings, collaborator roles, safe rename, read-only archive, soft deletion and 30-day restore | Automated permanent purge, total storage quotas, orphan reconciliation, storage sharding             |
| Git transport      | Native smart HTTP, normal Git clients, branches/tags, protocol-v2 forwarding, read/write PAT scopes, 100 MiB request/pack limit, subprocess deadlines/concurrency limit                | SSH, per-user/IP Git rate limits, durable post-receive events, process-level OS quotas, LFS          |
| Developer CLI      | `gitown` wrapper with bring/track/save/send/sync/unite/move/look commands, Git escape hatch, transparent credentials and exit codes                                                    | Installers, signed release binaries, shell completion, automatic server/token configuration          |
| Code browser       | Trees, text files up to 512 KiB, branch selection, latest 30 commits, README as escaped plain text                                                                                     | Markdown rendering/syntax highlighting, tags UI, blame, downloads, full pagination                   |
| Pull requests      | Same-repository open/list/detail, real diffs, mergeability, merge commit, expected head/base checks, atomic base-ref compare-and-swap, recoverable merge intent                        | Reviews/comments/checks, squash/rebase, merge queue, branch permissions, close/reopen PR, pagination |
| Issues             | Owners and triage-or-higher collaborators create and close/reopen issues; authenticated readers can discuss issues with a chronological 200-comment timeline                           | Comment editing, labels, assignees, milestones, notifications                                        |
| Audit              | Account, repository, token, issue, PR and receive-transport activity                                                                                                                   | Database-level immutability, durable exact ref-change events, IP/user-agent fields, retention/export |
| Operations         | Offline backup/restore scripts, tested PostgreSQL dump/restore and Git snapshot, local Compose definitions                                                                             | Online consistent backups, scheduled jobs, restore objectives, independent security review           |

The development alpha is **not ready for untrusted public hosting**. In particular, it has no email verification/MFA, fine-grained branch protections, total disk quotas, hardened Git process sandbox, or durable receive-event queue. Repository owners can push directly to `main`; PR-only policy is not implemented yet.

Current limits: 100 repositories per account, 20 active tokens per account, 30-day token life, 7-day session life, 30-day soft-deletion recovery, 100 returned repositories/issues/PRs, 200 comments per issue, 30 commits per history view, 4 MiB command output, 512 KiB displayed blobs, 100 MiB incoming Git request/pack, 20-second browse/merge subprocess deadline, 2-minute transport deadline, and four concurrent Git transports per API process.

## Merge consistency

Each merge request carries the head/base SHAs shown in the UI. Changed branches cause a 409. A database advisory lock serializes API merge operations for a repository. Git creates the merge objects before a durable `merging` record stores the intended result and expected base. `git update-ref` advances the base only if it still equals the expected SHA. Database finalization then marks the PR merged and records activity in one transaction.

If the process stops between Git and database finalization, retrying the merge reconciles the persisted intent. If the merge is already an ancestor of the base, the database can finalize safely. If the base still equals the expected old SHA, the saved ref update can resume. An unrelated base movement leaves the intent pending for manual inspection instead of guessing and overwriting history. The UI exposes a recovery action. Full automated reconciliation is a later milestone.

Head-branch changes after the final SHA check do not change the captured commit included in the merge. This alpha has no approval/check policy to invalidate; such policy must introduce stronger coordination before protected merges are enabled.

## Storage consistency

Repository creation writes storage before committing metadata. A failure can leave an unreferenced directory, which is inaccessible through repository routes. Do not automatically delete those directories; retain them for reconciliation. Backups in this release require writes to be stopped. Database and Git storage must be restored from the same backup.

## Verification performed

- Go race-enabled tests against PostgreSQL and real Git commands.
- End-to-end account → repository → settings/collaborators → role-gated clone/push/triage → PR diff → merge → pull.
- Unauthorized/private access, wrong credentials, read-only token push, revoked tokens, traversal/invalid refs, blocked force pushes and deletion, CSRF origin rejection.
- PostgreSQL migration reapplication and metadata/Git snapshot restore.
- Production Next.js compilation and TypeScript validation.
- Browser and responsive-layout validation recorded in the committed Playwright test.

Do not treat local checks as evidence that Docker images, GitHub-hosted CI, Windows clients, or public production operation have been verified.
