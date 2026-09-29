ALTER TABLE repositories ADD COLUMN pushed_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE repositories ADD COLUMN size_bytes bigint NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);
ALTER TABLE repositories ADD COLUMN language text NOT NULL DEFAULT '' CHECK (char_length(language) <= 40);
ALTER TABLE repositories DROP CONSTRAINT repositories_visibility_check;
ALTER TABLE repositories ADD CONSTRAINT repositories_visibility_check CHECK (visibility IN ('public', 'private', 'internal'));

CREATE TABLE districts (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    repo_creation text NOT NULL DEFAULT 'admin' CHECK (repo_creation IN ('owner', 'admin', 'member')),
    base_permission text NOT NULL DEFAULT 'read' CHECK (base_permission IN ('none', 'read', 'triage', 'write')),
    owner_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE repositories ADD COLUMN district_id uuid REFERENCES districts(id);
ALTER TABLE repositories ADD CONSTRAINT repositories_internal_district CHECK (visibility <> 'internal' OR district_id IS NOT NULL);
CREATE INDEX repositories_district ON repositories(district_id) WHERE district_id IS NOT NULL;
CREATE INDEX repositories_pushed ON repositories(pushed_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE district_members (
    district_id uuid NOT NULL REFERENCES districts(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('admin', 'member')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (district_id, user_id)
);

CREATE TABLE crews (
    id uuid PRIMARY KEY,
    district_id uuid NOT NULL REFERENCES districts(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (district_id, slug)
);

CREATE TABLE crew_members (
    crew_id uuid NOT NULL REFERENCES crews(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (crew_id, user_id)
);

CREATE TABLE district_secrets (
    id uuid PRIMARY KEY,
    district_id uuid NOT NULL REFERENCES districts(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$'),
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (district_id, name)
);

CREATE TABLE collections (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,37}[a-z0-9])?$'),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, slug)
);

CREATE TABLE collection_items (
    collection_id uuid NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    position int NOT NULL CHECK (position >= 0),
    PRIMARY KEY (collection_id, repository_id)
);

CREATE TABLE ssh_keys (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    public_key text NOT NULL CHECK (char_length(public_key) <= 16384),
    fingerprint text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ssh_keys_user ON ssh_keys(user_id, created_at DESC);

CREATE TABLE deploy_keys (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    public_key text NOT NULL CHECK (char_length(public_key) <= 16384),
    fingerprint text NOT NULL,
    write boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repository_id, fingerprint)
);
CREATE INDEX deploy_keys_fingerprint ON deploy_keys(fingerprint);

CREATE TABLE ref_events (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    actor_id uuid REFERENCES users(id),
    ref text NOT NULL,
    old_sha text NOT NULL,
    new_sha text NOT NULL,
    via text NOT NULL CHECK (via IN ('https', 'ssh', 'api')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ref_events_repo ON ref_events(repository_id, created_at DESC);

CREATE TABLE drops (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    tag text NOT NULL CHECK (char_length(tag) BETWEEN 1 AND 80),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 20000),
    provenance text NOT NULL DEFAULT '' CHECK (char_length(provenance) <= 4000),
    draft boolean NOT NULL DEFAULT false,
    prerelease boolean NOT NULL DEFAULT false,
    author_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repository_id, tag)
);

CREATE TABLE drop_assets (
    id uuid PRIMARY KEY,
    drop_id uuid NOT NULL REFERENCES drops(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    sha256 text NOT NULL,
    download_count int NOT NULL DEFAULT 0 CHECK (download_count >= 0),
    UNIQUE (drop_id, name)
);

CREATE TABLE crates (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,60}$'),
    ecosystem text NOT NULL DEFAULT 'npm' CHECK (ecosystem = 'npm'),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    visibility text NOT NULL DEFAULT 'public' CHECK (visibility IN ('public', 'private')),
    retention int NOT NULL DEFAULT 20 CHECK (retention BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ecosystem, name)
);

CREATE TABLE crate_versions (
    id uuid PRIMARY KEY,
    crate_id uuid NOT NULL REFERENCES crates(id) ON DELETE CASCADE,
    version text NOT NULL CHECK (version ~ '^[0-9A-Za-z][0-9A-Za-z.+-]{0,40}$'),
    metadata text NOT NULL DEFAULT '{}' CHECK (char_length(metadata) <= 8000),
    sha256 text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0 AND size_bytes <= 524288),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (crate_id, version)
);

CREATE TABLE pull_crew_requests (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    crew_id uuid NOT NULL REFERENCES crews(id) ON DELETE CASCADE,
    requested_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, crew_id)
);

ALTER TABLE access_tokens DROP CONSTRAINT access_tokens_scope_check;
ALTER TABLE access_tokens ADD CONSTRAINT access_tokens_scope_check CHECK (scope IN ('repo:read', 'repo:write', 'package:read', 'package:write'));
