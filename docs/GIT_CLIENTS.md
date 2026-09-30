# Git clients exercised

Phase 8 client coverage is the system `git` used by the PostgreSQL integration tests, plus the forced-command SSH gateway called in-process. No other Git client was executed.

The system Git tests performed:

- `git clone` and `git push` of a branch over smart HTTP with a personal access token
- `git fetch origin` after that push
- `git push` of a lightweight tag
- `git clone --depth 1`, confirmed with `git rev-parse --is-shallow-repository`
- a Git LFS batch upload and download over HTTP

A separate HTTP test creates an LFS path lock and expects the second lock on the same path to conflict.

The SSH tests called `git-upload-pack` and `git-receive-pack` through `SSHSession`. The advertisement test checks that stdout contains `refs/heads/main`. Receive tests check a read-only deploy key and a restricted-push denial before Git runs. They do not open a live sshd.

The gateway trusts the fingerprint argument supplied by `ForceCommand`. Install the printed `authorized_keys` line on a dedicated sshd user. GITOWN does not embed an SSH server.
