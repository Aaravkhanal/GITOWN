# GITOWN

An independent home for your code. Built and owned by Aarav Khanal.

GITOWN now has a working first implementation of the [blueprint](BLUEPRINT.md): a Next.js interface, Go API, PostgreSQL metadata, and native Git repository storage. You can create an account and repository, push a branch with standard Git, open a pull request, view its diff, merge it, and pull the result back to your computer.

This is a **local development alpha**, not the finished GitHub-equivalent MVP. See [implementation status](docs/STATUS.md) for the exact feature boundary.

## What works

- Accounts, login/logout, Argon2id password hashes, expiring browser sessions, and request-origin checks.
- Public/private repositories with optional initial README, editable descriptions/visibility, collaborator roles, safe rename/archive, 30-day deletion recovery, and real bare Git storage.
- Personal access tokens with read or write scopes, 30-day expiration, and revocation.
- Standard Git clone, fetch, pull, branch/tag push over smart HTTP (HTTPS when behind TLS).
- Role-gated read/triage/write/maintain access, private-repository enforcement, force-push/deletion rejection, and bounded Git operations.
- Branch selection, file/directory browsing, text previews, README text, and commit history.
- Same-repository pull requests, actual Git diffs, conflict detection, merge commits, stale-SHA rejection, and interrupted-merge recovery.
- Issue creation, descriptions, close/reopen, chronological discussions, reusable colored labels, and activity history.
- Responsive dashboard, repository filtering, repository settings, access-token settings, and empty/error states.
- A compatible `gitown` CLI with friendly commands such as `bring`, `track`, `save`, `send`, `sync`, and `unite`.

No other forge was cloned or copied. Standard dependencies keep their own licenses and attribution; they do not become contributors to GITOWN's Git history.

## Run locally

Requirements: Node.js 22+, Go 1.26+, Git 2.39+, PostgreSQL 15+ tools (`postgres`, `initdb`, `psql`, `createdb`, `pg_isready`). A project-local Go installation at `.tools/go/bin/go` is also supported.

```sh
npm ci
npm run dev
```

Open [localhost:3000](http://localhost:3000), create your account, then create a repository. `npm run dev`:

1. Starts GITOWN's own PostgreSQL cluster at `127.0.0.1:55432`, stored under `.data/postgres`.
2. Builds the Go API, applies versioned migrations, and starts it at `127.0.0.1:8080`.
3. Starts the Next.js frontend at `localhost:3000`.

`Ctrl+C` stops these services. Your local data remains in ignored `.data/`. The local database uses trust authentication and binds only to loopback; it is intended for a trusted development machine.

If ports are occupied, choose alternate ports:

```sh
GITOWN_WEB_PORT=3100 GITOWN_API_PORT=8180 npm run dev
```

If you already have a dedicated database, export `DATABASE_URL` first. The launcher will use it without starting PostgreSQL. It does **not** automatically load `.env` into your shell. `.env` is used by Docker Compose.

## Push and merge your first change

Create a repository with a README. In **Access tokens**, generate a read-and-write token. Then copy the clone command from the repository's **Clone repository** button.

```sh
git clone http://localhost:8080/git/YOUR_USERNAME/YOUR_REPOSITORY.git
cd YOUR_REPOSITORY
git switch -c feature/first-change
# Edit a file in your editor.
git add .
git commit -m "My first change"
git push -u origin feature/first-change
```

When Git asks, enter your GITOWN username and use the token as the password. Do not paste the token into the remote URL. Your account password is not accepted for Git operations.

You can also use GITOWN's own command names for this workflow. Build the CLI with `npm run build:cli`, then see the [CLI guide](docs/CLI.md).

Open **Pull requests → New pull request**, choose `main` as the base and your feature branch as the comparison, and create it. Inspect the diff, then click **Merge pull request**. Finally:

```sh
git switch main
git pull --ff-only origin main
```

This release uses merge commits. Squash/rebase methods, required reviews, permanent purge automation, and protected-branch policies beyond force-push/deletion prevention are planned.

## Docker Compose

```sh
cp .env.example .env
# Edit POSTGRES_PASSWORD in .env; use a URL-safe value for this local compose setup.
docker compose up --build
```

The frontend and API are published to loopback on ports 3000 and 8080. PostgreSQL uses 55432. The Compose volumes persist data across restarts. Stop an existing native GITOWN instance before starting Compose on the same ports.

Compose is provided for local development. Docker was unavailable in the initial development environment, so container builds remain to be verified. Public deployment requires TLS and the additional controls in [SECURITY.md](SECURITY.md).

## Checks

```sh
make check               # Go vet + TypeScript
make test                # Go unit tests; DB integration tests skip without TEST_DATABASE_URL
make build              # Go executable + production frontend
make cli                # Friendly GITOWN command-line client

# With the local development database running:
TEST_DATABASE_URL='postgres://gitown@127.0.0.1:55432/gitown?sslmode=disable' make test

# Against a running, disposable GITOWN test instance:
npx playwright install chromium
PLAYWRIGHT_BASE_URL=http://localhost:3100 npm run test:e2e
```

Database integration tests create and remove a randomly named schema and use temporary Git storage. They verify real Git transports, authorization failures, scoped/revoked tokens, merge conflicts/races, and a database/repository restore drill. Browser tests create test accounts and repositories in the selected instance, so use a disposable instance, not your everyday workspace.

GitHub Actions runs the Go checks, production frontend build, and browser workflow. Its configured credentials are only for an ephemeral CI database.

## Project map

```text
apps/server/       Go process and shutdown lifecycle
apps/gitown/       Friendly GITOWN command-line client
apps/web/          Next.js interface and typed API client
internal/app/      HTTP API, access checks, Git gateway, issues, pull requests
internal/auth/     Argon2id hashes and random credentials
internal/config/   Environment validation
internal/gitstore/ Bounded Git subprocesses and safe storage paths
internal/owncli/   GITOWN-to-Git command vocabulary
migrations/        Embedded, ordered PostgreSQL migrations
api/               OpenAPI contract
deploy/            Container build definitions
scripts/           Local startup and offline backup/restore
tests/e2e/         Browser workflow and responsive-layout checks
docs/              Decisions, status, security and operational details
```

The initial API modules share one application package for transactional workflows. Splitting into separate services is not required to expand this implementation.

## Next milestones

1. Add email verification, password recovery, MFA, and invitation-based signup.
2. Add collaborators and role-based access, SSH transport, and branch policies enforced consistently on push and merge.
3. Expand pull requests with comments, reviews, checks, squash/rebase, pagination, and deterministic conflict resolution.
4. Add durable Git events/outbox processing, backups under load, repository lifecycle/reconciliation, observability, and operational hardening.

The complete longer-term direction stays in [BLUEPRINT.md](BLUEPRINT.md).
