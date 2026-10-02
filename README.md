# GITOWN

An independent home for your code. Built and owned by Aarav Khanal.

GITOWN now has a working first implementation of the [blueprint](BLUEPRINT.md): a Next.js interface, Go API, PostgreSQL metadata, and native Git repository storage. You can create an account and repository, push a branch with standard Git, open a pull request, view its diff, merge it, and pull the result back to your computer.

This is a **local development alpha**, not the finished GitHub-equivalent MVP. See [implementation status](docs/STATUS.md) for the exact feature boundary.
The dependency-ordered remaining work is tracked in the [execution roadmap](docs/ROADMAP.md).
The numbered parity plan and honest phase gates are tracked in [product phases](docs/PRODUCT_PHASES.md).
The early workflow-planning preview is documented in [Routes](docs/ROUTES.md); it does not execute repository-authored commands.
The Phase 12 [Wiki preview](docs/WIKI.md) stores pages as Markdown in each repository's Git history.
User-facing terminology follows the compatibility-first [GITOWN naming system](docs/NAMING.md).

## What works

- Accounts, login/logout, Argon2id password hashes, secure password changes with optional token revocation, expiring/revocable browser sessions with device visibility, and request-origin checks.
- Public/private repositories with optional initial README, editable descriptions/visibility, collaborator roles, safe rename/archive, 30-day deletion recovery, and real bare Git storage.
- Personal access tokens with repository or package scopes, 30-day expiration, and revocation. Package tokens cannot push Git.
- Standard Git clone, fetch, pull, branch/tag push over smart HTTP (HTTPS when behind TLS), including a shallow clone. User SSH keys and deploy keys work through a forced-command gateway when sshd is configured separately. The gateway trusts the fingerprint that command supplies. Git LFS batch upload, download, and path locks are included. A pack that unpacks over the repository or account quota is rolled back before the client is told it succeeded.
- Role-gated read/triage/write/maintain access, private-repository enforcement, force-push/deletion rejection, and bounded Git operations.
- Branch selection, file/directory browsing, raw downloads, file-specific history, browser file creation/editing/deletion with race-safe Git commits, text previews, README text, and commit history.
- Same-repository Unite requests with drafts, close/reopen, discussions, inline line comments, requested reviewers, formal approve/request-changes reviews tied to exact head commits, review dismissal, owner-configured Merge Guards, squash/rebase/merge, optional source-branch deletion, actual Git diffs, conflict detection, stale-SHA rejection, and interrupted-merge recovery.
- Repository invitations, ownership-transfer confirmation, and a durable email outbox: a background worker sends each notification with retries and backoff, bundles digest readers' updates into one email per interval, adds per-message unsubscribe links with one-click support, and records mail as suppressed when SMTP is not configured.
- Issue planning with priority, iteration, estimates, due dates, duplicates, comment editing, and board, table, and roadmap views.
- Explore search across public issues and Unite requests, and builder search by skills, location, and availability.
- Notifications: repository watching (owners and maintainers watch by default), per-thread watch/participate/ignore, mentions in bodies, comments, reviews, and edits, assignment and review-request notices, check results for Unite authors, follower notices, and a paged inbox with unread counts and filters.
- Issue creation, descriptions, close/reopen, assignees, milestones with due dates and progress, chronological discussions, reusable colored labels, and activity history.
- Public builder profiles with bio, location, skills, up to five links, follower and following lists, contribution history from pushes, reviews, issues, and merged Unite requests, tiered badges earned by shipped work, and a six-repository showcase with stack, Sparks, and Drop downloads.
- Generated project showcase pages (`/repos/<owner>/<name>/showcase`) that combine the README, setup section, screenshots, demo link, tech stack, contributors, latest Drop, roadmap, and help-wanted issues.
- Repository Sparks: signed-in users can appreciate visible repositories; everyone with access can see the count.
- Builder follows: discover who follows a public profile and follow or unfollow other builders.
- A following feed for visible repository creation, Sparks, issues, and Unite requests from followed builders.
- Owner-managed repository topics for describing and organizing projects.
- Public repository discovery with server-backed text/topic search, language and beginner-friendly filters, recent/updated/name/Spark/30-day-trending sorting, ranked code search over a capped public text index, help-wanted tasks, topic-overlap recommendations, topic pages, a public contribution graph, and community collections that an operator can feature.
- Districts (organizations), crews (teams), internal repositories, encrypted district secrets, public-repository and outside-collaborator policies, usage counts, an invoice ledger that does not charge a card, and a district audit CSV.
- Drops (releases) with notes, assets, checksums, download counts, optional annotated tag creation, and SSH-signature provenance. Crates can be published through the JSON API, an unscoped npm wire subset, or an OCI blob and manifest subset. Publishing checks a fixed pattern list. This is not a full npm registry, a container registry, or a malware engine.
- Public builder discovery with username/name/bio search, follower and public-repository counts, and 25-result pages.
- Repository issue boards with To do, In progress, and Done columns; moving to Done closes the issue.
- Owner-managed issue templates that prefill titles and descriptions for recurring work.
- Same-repository issue dependencies with cycle detection; open blockers prevent closing or marking an issue Done.
- Issue following with an in-app inbox for comments and close/reopen updates.
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

To send email, export `GITOWN_SMTP_ADDR` (host:port) and `GITOWN_SMTP_FROM`, plus `GITOWN_SMTP_USER` and `GITOWN_SMTP_PASSWORD` if your server requires authentication. STARTTLS is used whenever the server offers it. `GITOWN_DIGEST_INTERVAL` (default `60m`) controls how often digest emails are sent. Without SMTP, GITOWN keeps an honest delivery record and marks messages as suppressed.

The release version comes from the root `package.json`. `npm run dev`, `make build`, `make cli`, and the API Dockerfile inject it into the Go binaries; `/healthz`, `gitown version`, and the web sidebar report it. Set `NEXT_PUBLIC_GITOWN_CHANNEL=stable` to hide the release-channel badge.

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
On a new branch, `gitown send` automatically publishes it to `origin` and records the upstream.

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
2. Run the forced-command SSH gateway behind a separately configured sshd. The gateway trusts the fingerprint argument that sshd binds. Multipart LFS transfer, per-user Git rate limits, and a durable receive queue are still absent.
3. Add a merge queue, forks, cross-repository Unite requests, and deterministic conflict resolution.
4. Add durable Git event delivery, backups under load, repository reconciliation, observability, and operational hardening.

The complete longer-term direction stays in [BLUEPRINT.md](BLUEPRINT.md).
