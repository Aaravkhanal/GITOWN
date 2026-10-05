# Extended ecosystem (partial)

Phase 12 is a set of bounded features. Each one below is real for the limit it states. None of them start a container, execute repository commands, bill a card, or accept a credential for GitHub, GitLab, or Bitbucket.

## Remixes

`POST /api/v1/repos/{owner}/{repo}/remix` clones a repository you can read into a new repository you own. The clone uses the local Git directory. It does not fetch a remote URL. A private or internal parent becomes a private remix. The account repository cap of 100 still applies. `GET .../remixes` lists public remixes and your own.

## Cross-project Unite requests

A Unite request may name `head_owner` and `head_repository` when that repository is a remix of the base, or they share a remix root. The caller must be able to read the base and write the head. GITOWN copies the head commit into the base object store under a hidden ref, `refs/gitown/pulls/<sha>`, and hides that namespace from upload-pack. Opening the request does not publish the remix's other objects as a branch. Reviews and merges resolve the head on the remix and copy the commit again. A route trigger still runs only when the head branch is on the base repository.

## Wiki and Town Hall

Wiki pages remain Git files. Search is `GET .../wiki-search?q=`. Supported PNG, JPEG, GIF, WebP, and PDF attachments are committed under `.gitown/wiki/attachments/` with a 512 KiB per-blob ceiling, matching the Git object limit. The API verifies bytes and serves only the allowlisted types with restrictive headers. Town Hall is `GET` and `POST .../discussions`, with comments and an open or closed state. Commenting requires a signed-in user who can see the repository.

## Showcase

`GET /sites/{owner}/{repo}` and `GET /sites/{owner}/{repo}/{path}` serve files from a public, unarchived repository. A `showcase` branch is preferred and is read from its root. Otherwise files come from `.gitown/showcase/` on the default branch. HTML, CSS, images, and plain text are served with this policy:

`default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox`

There is no `allow-scripts`. JavaScript in a page is delivered as text and is not granted permission to run. This is separate from the older project showcase page at `/repos/{owner}/{name}/showcase`.

## Snippets

`GET` and `POST /api/v1/snippets` store one file, public or private, up to 64 KiB. A private snippet is visible only to its owner.

## Development environments

`POST .../dev-environments` reads `.gitown/dev.yml` and records `pending_sandbox_review`. The file may contain `name`, `notes`, and `image`. Any other field, including `run`, is rejected. `execution_enabled` is false. Cancel moves the row to `cancelled`. No container is created.

## Supply chain

A scan reads the default branch locally:

- `go.mod` require lines
- `package.json` dependencies and devDependencies
- `requirements.txt` lines with `==`

Secret findings match a fixed marker list, the same family used for package publishes. The stored record is the path, line number, marker name, and commit SHA. The line itself is not stored. Repository write permission is required to list findings.

A maintainer can publish a `GOWN-` advisory for a package in the `go`, `npm`, or `pypi` ecosystem. A scan opens at most five issues when a published advisory's package matches an edge and the patched version differs. The issue is a note. It does not change a file. Optional OSV lookups are disabled unless `GITOWN_OSV_ENABLED=true`; when enabled they send exact package names and versions to api.osv.dev and upsert linked advisory snapshots and vulnerability alerts. Range-style versions are skipped, and the status reports whether lookups were disabled or unavailable. Review [Phase 12 ecosystem decisions](PHASE12_ECOSYSTEM.md) before enabling it on private repositories. Alerts can be dismissed.

`.gitown/CODEOWNERS`, or `CODEOWNERS` at the repository root, requests reviewers on a new Unite request. The last matching pattern wins. Patterns are `*`, a directory prefix, `/**`, or an exact path. Reviewers must be the owner or a write or maintain member, and the author is excluded.

## Merge queue

`POST .../merge-queue` adds an open, non-draft Unite request. While any entry is `waiting`, only the request at the front can merge. Other merge attempts return 409 `merge_queue`. A successful merge, or a retry that finds the request already merged, marks that entry `merged`. An empty queue leaves the previous merge behavior unchanged.

## Mobile notifications and pledges

`POST /api/v1/user/devices` returns a `mob_` token once. `GET /api/v1/mobile/feed` with that token returns unread inbox rows. Delivery is pull. These tokens are not APNs, FCM, or Web Push subscriptions; actual provider delivery is not yet implemented. The provider and credentials decision is scoped in [Phase 12 ecosystem decisions](PHASE12_ECOSYSTEM.md).

`POST /api/v1/users/{username}/sponsorships` records a pledge of 0 to 100,000,000 cents. The response includes `charges: false`. There is no card charge. A builder cannot pledge to their own account.

## Import

`POST /api/v1/imports` accepts `source` of `github`, `gitlab`, or `bitbucket` and a public `https://` URL on `github.com`, `gitlab.com`, or `bitbucket.org`. The URL must have two path segments, no user information, no query, no fragment, and no port. The host must resolve to a public address. The clone is shallow, redirects are disabled, and the file protocol is rejected. No personal access token is accepted. A hostile DNS answer that changes between the check and the clone is still a residual risk; the clone itself still refuses non-HTTPS URLs and redirects.
