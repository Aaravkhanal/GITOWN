CREATE TABLE users (
    id uuid PRIMARY KEY,
    username text NOT NULL UNIQUE CHECK (username = lower(username)),
    email text NOT NULL UNIQUE CHECK (email = lower(email)),
    display_name text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE access_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    scope text NOT NULL CHECK (scope IN ('repo:read', 'repo:write')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE TABLE repositories (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (name = lower(name)),
    description text NOT NULL DEFAULT '',
    visibility text NOT NULL CHECK (visibility IN ('public', 'private')),
    default_branch text NOT NULL DEFAULT 'main',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(owner_id, name)
);
CREATE TABLE issues (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id),
    number integer NOT NULL,
    author_id uuid NOT NULL REFERENCES users(id),
    title text NOT NULL,
    body text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, number)
);
CREATE TABLE pull_requests (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id),
    number integer NOT NULL,
    author_id uuid NOT NULL REFERENCES users(id),
    title text NOT NULL,
    body text NOT NULL DEFAULT '',
    base_branch text NOT NULL,
    head_branch text NOT NULL,
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed', 'merging', 'merged')),
    merge_sha text,
    expected_base_sha text,
    created_at timestamptz NOT NULL DEFAULT now(),
    merged_at timestamptz,
    UNIQUE(repository_id, number)
);
CREATE UNIQUE INDEX one_open_pull_per_pair ON pull_requests(repository_id, base_branch, head_branch) WHERE state IN ('open', 'merging');
CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_id uuid REFERENCES users(id),
    action text NOT NULL,
    target text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_actor ON audit_events(actor_id, id DESC);
