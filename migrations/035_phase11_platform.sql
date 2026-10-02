-- Phase 11 control plane. These tables store secrets, runner registrations,
-- caches, and artifact bytes. They do not start a workload. Untrusted
-- workflow execution stays disabled until sandboxing has been reviewed.

ALTER TABLE route_environments
    ADD COLUMN network text NOT NULL DEFAULT 'restricted' CHECK (network IN ('none', 'restricted')),
    ADD COLUMN cpu_millis int NOT NULL DEFAULT 1000 CHECK (cpu_millis BETWEEN 100 AND 8000),
    ADD COLUMN memory_mb int NOT NULL DEFAULT 512 CHECK (memory_mb BETWEEN 128 AND 8192),
    ADD COLUMN disk_mb int NOT NULL DEFAULT 1024 CHECK (disk_mb BETWEEN 128 AND 10240),
    ADD COLUMN isolation text NOT NULL DEFAULT 'untrusted_execution_disabled';

CREATE TABLE route_secrets (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[A-Z][A-Z0-9_]{0,63}$'),
    ciphertext bytea NOT NULL,
    nonce bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, name)
);

CREATE TABLE route_runners (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
    kind text NOT NULL CHECK (kind = 'self_hosted'),
    labels text[] NOT NULL DEFAULT '{}',
    token_hash text NOT NULL UNIQUE,
    cpu_millis int NOT NULL CHECK (cpu_millis BETWEEN 100 AND 8000),
    memory_mb int NOT NULL CHECK (memory_mb BETWEEN 128 AND 8192),
    disk_mb int NOT NULL CHECK (disk_mb BETWEEN 128 AND 10240),
    network text NOT NULL CHECK (network IN ('none', 'restricted')),
    last_seen_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, name)
);

CREATE TABLE route_caches (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    cache_key text NOT NULL CHECK (cache_key ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,119}$'),
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    storage_path text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(repository_id, cache_key)
);
