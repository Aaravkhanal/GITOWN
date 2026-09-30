# Git clients exercised

Phase 8 client coverage is the system `git` used by the PostgreSQL integration tests, plus the forced-command SSH gateway called in-process. No other Git client was executed.

The system Git tests performed:

- `git clone` and `git push` of a branch over smart HTTP with a personal access token
- `git fetch origin` after that push
- `git push` of a lightweight tag
- `git clone --depth 1`, confirmed with `git rev-parse --is-shallow-repository`
- a Git LFS batch upload and download over HTTP

A separate HTTP test creates an LFS path lock and expects the second lock on the same path to conflict.

The SSH tests drive a real `git clone` and `git push` end to end: the test binary re-execs itself as `GIT_SSH_COMMAND` (`GIT_SSH_VARIANT=simple`) so the system `git` client talks to `SSHSession` exactly as it would to a live sshd, including the initial ref advertisement, `require_unite` rejection, and a signed-commit branch rule. Earlier receive-only tests that fed `SSHSession` pre-built commands (checking a read-only deploy key and a restricted-push denial before Git runs) still exist alongside this, but the client-driven test is the one that proves a real `git` binary can clone and push over this path without deadlocking. Neither opens a live sshd process — `SSHSession` is called in-process — but the Git protocol traffic between client and server is genuine.

Only the system `git` client (see `git --version` in the CI image) has been exercised, once, over one protocol version. No other Git client implementation — libgit2, JGit, isomorphic-git, GitHub Desktop's bundled Git, or similar — and no matrix of `git` versions have been tested. This is a known, disclosed gap, not a claim of broad client compatibility.

The gateway trusts the fingerprint argument supplied by `ForceCommand`. Install the printed `authorized_keys` line on a dedicated sshd user. GITOWN does not embed an SSH server.
