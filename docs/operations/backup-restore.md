# Offline backup and restoration

This first implementation supports coordinated **offline** snapshots. PostgreSQL remains running, while the API and every writer to repository storage must stop. Copying a database and live Git storage at unrelated times is not a consistent backup.

For the native launcher, Ctrl+C also stops its private PostgreSQL process. Start only that existing cluster again, without the API:

```sh
postgres -D .data/postgres -h 127.0.0.1 -p 55432 -k "$PWD/.data"
```

Leave it running in that terminal. In another terminal set the database and storage variables. Use an entirely new absolute snapshot directory with an existing parent:

```sh
export DATABASE_URL='postgres://gitown@127.0.0.1:55432/gitown?sslmode=disable'
export GITOWN_DATA_DIR="$PWD/.data/repositories"
GITOWN_OFFLINE=true node scripts/backup.mjs create /absolute/backups/gitown-2026-09-27
```

The script creates a custom PostgreSQL dump, copies the bare repositories, and writes a final manifest with the metadata checksum. It refuses to overwrite an existing snapshot. An interrupted backup has no manifest and must not be used. Protect the whole snapshot as sensitive source code and credential metadata; it includes password/session/token hashes. Encrypt it before storing it off-host.

To test a restore, provision an **empty, separate database** and select a repository path that does not yet exist. Set `DATABASE_URL` and `GITOWN_DATA_DIR` to those targets, then:

```sh
GITOWN_OFFLINE=true node scripts/backup.mjs restore /absolute/backups/gitown-2026-09-27
```

The script verifies the dump checksum, refuses existing tables/storage, restores the Git directories, runs `git fsck --full`, and restores metadata without ownership changes. Failures can leave partial data in the new targets; preserve them for diagnosis and use fresh targets for another attempt. The script never removes existing targets.

Start an isolated API against the restored targets. Keep the old installation intact until restoration is accepted. Session/token hashes are restored too; invalidate them manually if the recovery follows a credential compromise.

For a repeatable post-restore application drill, choose a private repository and known issue and Unite request that existed at the backup point. Log in to the isolated recovery deployment with a dedicated drill account, create a short-lived read-only token for Git transport, and retain the new session cookie and token only in your local shell environment. Then run:

```sh
export GITOWN_DRILL_CONFIRM_ISOLATED=true
export GITOWN_DRILL_API_URL='https://restore.example'
export GITOWN_DRILL_GIT_URL='https://restore.example/git'
export GITOWN_DRILL_OWNER='restore-owner'
export GITOWN_DRILL_REPOSITORY='restore-proof'
export GITOWN_DRILL_ISSUE_NUMBER=7
export GITOWN_DRILL_PULL_NUMBER=3
export GITOWN_DRILL_SESSION_COOKIE='SESSION_COOKIE_VALUE'
export GITOWN_DRILL_TOKEN='READ_ONLY_TOKEN_VALUE'
export GITOWN_DRILL_REPORT='/absolute/path/restore-drill-report.json'
node scripts/restore-drill.mjs
```

The verifier refuses HTTP, requires an explicit isolation confirmation, and writes a mode-0600 report only to a new absolute path. It never includes the session cookie or token in the report or normal output. It checks liveness/readiness and baseline headers, confirms anonymous access is denied while the authenticated session can read the private repository, performs authenticated `git ls-remote`, and fetches the selected issue and Unite request by number. The drill credential is passed to Git through a temporary askpass helper which is removed afterward. Use a dedicated drill account and token; do not reuse an operator credential.

Record the source backup timestamp/ID, source and restore build identifiers, start/end times, measured RPO/RTO, test IDs, report path, deviations, and operator acceptance in your deployment's protected incident/change record. The script proves only the selected checks; it does not switch traffic or validate off-host encryption, scheduled backup delivery, SMTP, or production-scale failover.

The weekly and per-change GitHub Actions workflow performs a real PostgreSQL custom-dump/restore drill with a bare Git repository, checks restored metadata and Git content, and proves corrupt metadata is rejected before the recovery directory is touched. Restore validates the archive and bare Git repositories before writing either target. CI verifies the restore tooling against its PostgreSQL version, not your production deployment. Run and retain the isolated application drill above before relying on recovery.
