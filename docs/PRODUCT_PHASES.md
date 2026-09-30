# GITOWN product phases

This file is the product-parity plan discussed for GITOWN. It complements the dependency-ordered engineering roadmap. A phase is complete only when its listed capabilities, authorization rules, audit trail, UI, API contract, documentation, and automated tests are shipped.

Status legend: **partial** means usable capabilities exist but the phase gate has not been met; **planned** means implementation has not reached the gate.

## Phase 1 — Safe public accounts (partial)

Shipped: registration and login, Argon2id passwords, authenticated password changes that revoke other sessions and can revoke every access token, signed-in device visibility, scoped personal access tokens, CSRF/origin checks, and basic login throttling.

Remaining gate: verified email, password recovery, TOTP MFA and recovery codes, step-up authentication for destructive actions, security-event notifications, invitation controls, stronger account/IP abuse throttles, and operator-visible abuse decisions.

## Phase 2 — Everyday repository work (partial)

Shipped: public/private repository creation, collaborators and roles, real bare Git storage, smart HTTP clone/fetch/push, branch/tag transport, tree and text browsing, raw downloads, repository and file-specific history, lifecycle controls, friendly CLI commands, and browser file creation/editing/deletion with atomic stale-head protection.

Remaining gate: file rename/upload, sanitized Markdown, syntax highlighting, blame, compare UI, import/export, orphan reconciliation, and an embedded SSH server. Repository and account quotas, `git gc`, Git LFS batch transfer with path locks, and a forced-command SSH gateway are implemented. A push that unpacks over the quota has its new refs rolled back before the client is told the push succeeded. LFS has no multipart transfer. SSH is not an embedded sshd.

## Phase 3 — Collaboration and protected delivery (complete)

Shipped: issues, assignees, labels, milestones with due dates and progress, discussions, issue following, Unite following, and an in-app inbox for issue and Unite events; same-repository Unite requests, diffs, discussions, close/reopen, draft Unite requests, formal approve/request-changes reviews with stale-head visibility and dismissal (with a required reason) that keeps an audit trail, an approval count based on each reviewer's latest non-dismissed review at the current head, requested reviewers, and crew review requests with a UI.

Collaborator access requires an invitation — pending, accepted, declined, expired, or revoked — with no path left to add a collaborator directly; a district's outside-collaborator policy is enforced both when an invitation is sent and when it is accepted; an email-only invitation carries an accept link and works by token even before the invitee has an account; ownership transfer requires the sender to type the repository's full name to confirm, the recipient to accept, and a stale-transfer check that rejects the accept if ownership already moved; a transfer can be viewed and cancelled while pending; and a role-capability endpoint and UI show a collaborator exactly what their role permits.

Inline comments are anchored to an exact commit, file, side, and line, validated against the current diff, startable by clicking a diff line, with replies, resolution (including by the thread's own author), and outdated marking; a deduplicated timeline covers pushes (including browser edits), comments, reviews, inline threads, replies, and check results; large diffs and histories page with a "load more" control; squash/rebase/merge with optional source-branch deletion that respects branch protection; Unite assignees, labels, and linked issues; and closing keywords in a Unite body or its commits close the linked issue once no blocker is open.

Branch rules can require resolved conversations, an up-to-date branch, a minimum approval count, approval from named reviewers or crews, successful check contexts, signed commits verified against a registered signing key, and restrict who can push, force-push, or delete a branch — enforced identically for browser edits, API merges, HTTPS pushes, and SSH pushes through one shared policy function and a pre-receive hook. Unprotected branches can be force-pushed and deleted; only rules an owner turns on apply. SSH push and fetch stream both directions correctly. A bearer-scoped personal access token can post commit statuses without a browser session, for CI systems.

Boundaries: a merge queue, forks (Remixes), and cross-repository Unite requests stay later. Maintainer approval still means the owner or a member with the maintain role, or a district owner/admin on a district repository. A required-crew rule names a crew in the repository's own district; cross-district rules stay later.

## Phase 4 — Boards and planning (complete)

Shipped: a repository issue board with configurable status columns (2–10, Done always last), synchronized with issue close/reopen and merge/close/reopen of Unite requests placed on the board, with a small set of automation toggles; owner-managed issue templates with validated custom form fields, plus one-click default bug-report and feature-request templates; same-repository sub-issues with a progress bar and cycle/depth limits, alongside the existing blocked-by/blocks dependency model that rejects cycles and blocks completion while a prerequisite stays open.

Issues filter server-side by state, up to five labels, assignee, author, milestone, and free text, with pagination; comments can be edited with a visible "edited" marker and history; duplicate marking closes the issue and rejects cycles; pinning; issue transfer works between any two repositories the actor can access, carrying over numbering, labels, dependencies, board placement, and duplicate pointers; `#42` and cross-repository `owner/repo#42` references are recorded and rendered as links with a "referenced by" list; saved searches; custom board fields (text, number, date, single select); priority, estimate, due date, and iteration; board, table, and roadmap views (the roadmap is a real due-date timeline) with drag-and-drop; and closing keywords now close an issue from an ordinary push to the default branch as well as from a Unite request.

District-wide boards aggregate issues across the repositories a viewer can read, alongside the existing per-repository boards.

## Phase 5 — Social and discovery (complete)

Shipped: public builder profiles with multiple labelled links and skill tags, configurable repository showcases with screenshots and a generated public showcase page per repository, one-click repository Sparks with visible counts, builder follows with counts, follower/following lists, and self-follow protection, a public following feed for repository creation/Sparks/issues/Unite requests, owner-managed repository topics, public repository text/topic search with Spark sorting and 30-day trending, and paged public builder search by username, name, bio, skill, location, and availability.

Notification email is delivered through a durable outbox: a background worker claims queued messages and sends them outside any open database transaction, with retry and backoff, and digest mode assembles and actually sends one email per interval instead of only marking messages sent. Every comment, review, or other event sends its own email — earlier deliveries no longer silently stop after the first email per thread — with real subject and body content and a direct link to the item. Unsubscribing uses a hashed, expiring, one-click (RFC 8058) link with a confirmation page, not a bare token pasted into the email body.

Watch, participate, and ignore subscription modes now behave differently from each other; owners and maintainers watch their own repositories by default and are notified of new issues and Unite requests; mentions are recognized in issue, Unite request, and review bodies as well as comments, including on edits; check-result notifications always reach the Unite request's author on failure; and invitation and ownership-transfer notifications for a private repository are visible to the invitee. The inbox is paginated and filterable with an unread count and a sidebar badge. Contribution history counts pushes, reviews, merged Unite requests, and issues — not Sparks — and contribution badges are tiered.

Boundaries: custom project domains stay later.

## Phase 6 — Discovery (complete)

Shipped: language and beginner-friendly filters, recently updated sorting, ranked search of a capped public text index with a `git grep` fallback, topic catalog and topic pages, help-wanted and good-first-task discovery, topic-overlap recommendations with a trending fallback, an hourly Spark cap counted from audit events, user-owned community collections of public repositories, operator-featured collections through `GITOWN_OPERATORS`, and a 180-day public contribution graph of issues, comments, Sparks, and merged Unite requests.

Boundaries: the index stores a limited number of text lines from public default branches. It is not an external search cluster. Recommendations are topic overlap, not a trained model. The contribution graph does not count private commits. Custom project domains stay later.

## Phase 7 — Districts and crews (complete)

Shipped: districts (organizations) with public or private visibility, owner and admin or member roles, repository creation policy, base permission on district repositories, internal repository visibility, crews (teams), crew review requests that notify members, encrypted district secrets when `GITOWN_SECRET_KEY` is set, a district audit JSON view and CSV download, usage counts, an operator invoice ledger, and two enforced policies: whether the district allows public repositories and whether it allows collaborators from outside the district.

Boundaries: invoices record an amount an operator enters and can be marked paid. Responses include `charges: false`. GITOWN does not charge a card. SAML/OIDC and IP restrictions stay later. District-wide boards stay in phase 4. A crew request does not count as a review by itself.

## Phase 8 — Git transport depth (complete)

Shipped: user SSH keys, repository deploy keys, a forced-command `ssh-shell` gateway using the same branch protections as HTTPS, Git LFS batch upload and download plus path locks, shallow clone, fetch, and push through the system Git client, repository and account quotas from `GITOWN_REPO_QUOTA_BYTES` and `GITOWN_USER_QUOTA_BYTES`, ref-change events with old and new SHAs, maintenance via `git gc`, tag listings that report whether a signature block is present, and SSH signing keys used to verify drop provenance.

Boundaries: the gateway trusts the fingerprint argument because sshd must bind it with `ForceCommand`. Run that binary as a dedicated user. GITOWN does not embed sshd. LFS locks are single-request path locks, not multipart transfers. A pack is measured again after unpack; refs created or moved by that push are rolled back and the client is not told the push succeeded when the unpacked size exceeds the quota. Tag “signed” still means a signature block is present. Trust for drop provenance is `ssh-keygen -Y verify` against a key the publisher registered, not a global keyring. Client coverage is the system `git` plus the in-process gateway checks in [GIT_CLIENTS.md](GIT_CLIENTS.md).

## Phase 9 — Drops and crates (complete)

Shipped: drops (releases) for an existing tag or for an annotated tag created at publish time, draft and prerelease flags, notes, a generated changelog from `git log`, binary assets with SHA-256 checksums, public download counts, and provenance that is marked verified only after an SSH signature matches a signing key the publisher registered. Crates store package name, version, metadata, a file up to 512 KB, retention, and deletion. Package-scoped tokens authorize crate routes. Unscoped npm publish, packument, and tarball routes are served at `/npm`. OCI blob upload and manifest routes are served at `/v2`. Publishing rejects a fixed list of private-key headers, token prefixes, and dangerous command strings.

Boundaries: this is not Sigstore. An empty signature stays unverified. The npm routes accept one unscoped name, not a full npm registry. The OCI routes store one blob and one manifest reference, not a full container registry. The pattern list is not a malware engine. Additional package ecosystems stay later.

## Later phases (planned)

10. Integrations: durable events, signed webhooks, OAuth apps, and deployment verification.
11. Routes: sandboxed automation runners, workflow definitions, logs, artifacts, cancellation, and check integration.
12. Extended ecosystem: wikis, Showcase hosting, Town Hall, forks (Remixes), cross-repository Unite, card billing, and high availability. SAML/OIDC and IP restrictions stay later as well.

The naming contract is defined in [NAMING.md](NAMING.md). Standard Git protocol and commands remain compatible even when GITOWN presents friendlier names in its UI and CLI.
