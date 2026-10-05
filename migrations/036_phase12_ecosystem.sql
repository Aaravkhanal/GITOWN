-- Phase 12 extended ecosystem. Remixes are real Git clones. Cross-project
-- Unite requests record the head repository. Nothing here charges a card,
-- starts a development container, or executes repository-authored code.

ALTER TABLE repositories
    ADD COLUMN parent_repository_id uuid REFERENCES repositories(id) ON DELETE SET NULL,
    ADD COLUMN fork_root_id uuid REFERENCES repositories(id) ON DELETE SET NULL,
    ADD COLUMN source_url text NOT NULL DEFAULT '' CHECK (char_length(source_url) <= 300);
CREATE INDEX repositories_parent ON repositories(parent_repository_id);

ALTER TABLE pull_requests
    ADD COLUMN head_repository_id uuid REFERENCES repositories(id) ON DELETE SET NULL;
DROP INDEX one_open_pull_per_pair;
CREATE UNIQUE INDEX one_open_pull_per_pair
    ON pull_requests (repository_id, base_branch, head_branch, (COALESCE(head_repository_id, repository_id)))
    WHERE state IN ('open', 'merging');

CREATE TABLE discussions (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    number integer NOT NULL,
    author_id uuid NOT NULL REFERENCES users(id),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    category text NOT NULL CHECK (category IN ('general', 'ideas', 'announcements', 'q-and-a')),
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, number)
);

CREATE TABLE discussion_comments (
    id uuid PRIMARY KEY,
    discussion_id uuid NOT NULL REFERENCES discussions(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 20000),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE snippets (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    filename text NOT NULL CHECK (filename ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$'),
    content text NOT NULL CHECK (char_length(content) <= 65536),
    visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX snippets_owner ON snippets(owner_id, created_at DESC);

CREATE TABLE dev_environments (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    actor_id uuid NOT NULL REFERENCES users(id),
    ref text NOT NULL,
    sha text NOT NULL CHECK (char_length(sha) = 40),
    image text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'pending_sandbox_review' CHECK (status IN ('pending_sandbox_review', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE dependency_edges (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    manifest text NOT NULL,
    ecosystem text NOT NULL CHECK (ecosystem IN ('go', 'npm', 'pypi')),
    package_name text NOT NULL,
    version text NOT NULL,
    UNIQUE(repository_id, manifest, package_name)
);

CREATE TABLE security_advisories (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    code text NOT NULL UNIQUE CHECK (code ~ '^GOWN-[A-F0-9]{8}$'),
    severity text NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    summary text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 300),
    package_name text NOT NULL CHECK (char_length(package_name) BETWEEN 1 AND 200),
    ecosystem text NOT NULL CHECK (ecosystem IN ('go', 'npm', 'pypi')),
    patched_version text NOT NULL DEFAULT '' CHECK (char_length(patched_version) <= 80),
    state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft', 'published', 'withdrawn')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE secret_findings (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    path text NOT NULL,
    line_number int NOT NULL CHECK (line_number > 0),
    marker text NOT NULL,
    sha text NOT NULL CHECK (char_length(sha) = 40),
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'dismissed')),
    UNIQUE(repository_id, path, line_number, marker)
);

CREATE TABLE vulnerability_alerts (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    advisory_id uuid NOT NULL REFERENCES security_advisories(id) ON DELETE CASCADE,
    manifest text NOT NULL,
    package_name text NOT NULL,
    installed_version text NOT NULL,
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'dismissed')),
    UNIQUE(repository_id, advisory_id, manifest, package_name)
);

CREATE TABLE merge_queue (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    position int NOT NULL CHECK (position > 0),
    status text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'merged', 'dequeued')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX merge_queue_one_waiting ON merge_queue(pull_request_id) WHERE status = 'waiting';
CREATE INDEX merge_queue_order ON merge_queue(repository_id, position) WHERE status = 'waiting';

CREATE TABLE mobile_devices (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    platform text NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz
);

CREATE TABLE sponsorships (
    id uuid PRIMARY KEY,
    sponsor_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount_cents int NOT NULL CHECK (amount_cents >= 0 AND amount_cents <= 100000000),
    message text NOT NULL DEFAULT '' CHECK (char_length(message) <= 280),
    public boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(sponsor_id, target_user_id)
);
