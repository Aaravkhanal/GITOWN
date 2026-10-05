# Repository imports, exports, and storage recovery

## Import and export

The workspace import form accepts public HTTPS URLs from GitHub, GitLab, and Bitbucket. It does not accept forge credentials; imported repositories default to private. The repository page's **Export bundle** download contains all advertised refs and can be restored with `git clone project.bundle` or inspected with `git bundle list-heads project.bundle`. Large repositories stream from Git storage rather than being buffered in application memory.

## Orphan-storage reconciliation

Repository creation can be interrupted after its bare Git directory is created but before its database transaction commits. Such a directory has no repository route and must not be deleted automatically. Configure operator usernames in `GITOWN_OPERATORS`, sign in as one, and use:

```sh
curl -b session-cookie -H 'Content-Type: application/json' \
  -d '{"action":"dry_run"}' https://gitown.example/api/v1/operator/storage/reconcile
```

Dry-run lists UUID-named Git directories absent from repository metadata and already quarantined directories. To preserve an orphan while removing it from the active storage namespace:

```sh
curl -b session-cookie -H 'Content-Type: application/json' \
  -d '{"action":"quarantine","repository_id":"00000000-0000-4000-8000-000000000000"}' \
  https://gitown.example/api/v1/operator/storage/reconcile
```

Quarantine moves the directory to `<GITOWN_DATA_DIR>/_gitown/orphan-quarantine/`. To recover it to the active storage directory, repeat with `"action":"restore"`. The ID above is illustrative: use an ID returned by dry-run. Registered repositories, symlinks, unknown names, and occupied destinations are rejected. Every move is audited; no endpoint here permanently deletes repository data. Take a consistent database-and-storage backup before any operator reconciliation.

## CLI installation and completion

Linux and macOS releases for amd64 and arm64 are attached to version tags. The release workflow signs the checksum manifest with Sigstore keyless signing. Install `cosign`, then run:

```sh
curl -fsSL https://raw.githubusercontent.com/Aaravkhanal/GITOWN/main/scripts/install-gitown.sh | sh
```

The installer verifies the signed checksum manifest and the selected archive checksum before installing to `~/.local/bin` (override with `GITOWN_INSTALL_DIR`). To enable completion, add the output for your shell to its normal completion setup; for example, `gitown completion bash`, `gitown completion zsh`, or `gitown completion fish`.

## SSH deployment decision

GITOWN intentionally does not embed sshd. Run the supplied forced-command gateway behind a separately managed sshd configured with a dedicated service account, public-key fingerprint binding, and no shell/forwarding/PTY access. This keeps shell execution out of the web process. Embedded SSH remains a future decision if the operational need outweighs its additional host-key, daemon lifecycle, privilege-separation, and network attack-surface responsibilities.
