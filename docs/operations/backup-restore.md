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

Start an isolated API against the restored targets. Verify account login, repository privacy, a clone/fetch, and PR/issue history before sending any real traffic to it. Keep the old installation intact until restoration is accepted. Session/token hashes are restored too; invalidate them manually if the recovery follows a credential compromise.

Integration tests perform a PostgreSQL dump/restore drill in a randomly generated test schema and verify the snapshot's merged Git history. The standalone script should also be exercised in your deployment environment before relying on it for recovery. Scheduled/online backups, storage reconciliation, retention, encryption/key management, RPO/RTO, and restore automation remain future work.
