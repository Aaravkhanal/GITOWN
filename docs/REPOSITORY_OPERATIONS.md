# Repository imports, exports, and storage recovery

## Import and export

The workspace has two portability paths:

- **Import a public repository** validates a canonical HTTPS URL for GitHub, GitLab, or Bitbucket and shows the upstream/destination and visibility before the user confirms. It accepts no forge credentials. The import uses a shallow clone of the upstream default branch; other branches and tags are not copied. Imported repositories default to private. Preview checks the URL and public DNS answers but does not prove that a repository exists. Fetching still repeats DNS resolution; treat import as a trusted-pilot feature until the documented DNS-pinning hardening is complete.
- **Restore a GITOWN export package** accepts only a package produced by GITOWN. No arbitrary archive paths are extracted. The server accepts exactly two regular files, manifest.json and repository.bundle, checks the version/schema, bundle size and SHA-256, compares the ref list against the bundle itself, validates the default branch, and enforces repository/account quotas before committing the restored repository. The bundle limit is 100 MiB and manifest limit is 4 MiB. Corrupt or unsupported input is rejected before the repository row is committed.

The repository page's **Export package** download is an uncompressed tar containing a JSON manifest and Git bundle. The v1 manifest has schema_version 1, format gitown.repository-export/v1, source repository display metadata, the default branch, the bundle filename/size/SHA-256, and all refs included in the bundle. The checksum detects corruption; it is not a signature and does not prove who produced the archive. Keep export packages private when the repository is private.

The bundle contains Git refs and objects, not application collaboration state. The manifest explicitly lists omitted areas; the package never includes accounts, credentials, sessions, access permissions, issues, Unite requests, webhook configuration/secrets, or Routes secrets. Import chooses a new repository name and visibility; it does not transfer the source repository's access policy. The original raw .bundle route remains available for users who want to restore it with ordinary Git or inspect it with git bundle list-heads.

The automated portability integration test exports a private repository with multiple branches and a tag, restores it through the upload API, compares every ref/object ID, and checks that a corrupt archive creates no repository. Private/authenticated forge imports are not implemented; they require provider-specific, read-only credentials and the controls in [product scope](PRODUCT_SCOPE.md).

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
