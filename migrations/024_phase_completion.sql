CREATE TABLE code_documents (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    path text NOT NULL,
    line int NOT NULL CHECK (line > 0),
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 200),
    PRIMARY KEY (repository_id, path, line)
);
CREATE INDEX code_documents_search ON code_documents USING gin (to_tsvector('simple', content));

ALTER TABLE collections ADD COLUMN featured boolean NOT NULL DEFAULT false;

ALTER TABLE districts ADD COLUMN allow_public boolean NOT NULL DEFAULT true;
ALTER TABLE districts ADD COLUMN allow_outside_collaborators boolean NOT NULL DEFAULT true;

CREATE TABLE district_invoices (
    id uuid PRIMARY KEY,
    district_id uuid NOT NULL REFERENCES districts(id) ON DELETE CASCADE,
    period text NOT NULL CHECK (period ~ '^[0-9]{4}-[0-9]{2}$'),
    amount_cents int NOT NULL CHECK (amount_cents >= 0 AND amount_cents <= 100000000),
    status text NOT NULL CHECK (status IN ('open', 'paid', 'void')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (district_id, period)
);

CREATE TABLE signing_keys (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    public_key text NOT NULL CHECK (char_length(public_key) <= 16384),
    fingerprint text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX signing_keys_user ON signing_keys(user_id, created_at DESC);

ALTER TABLE drops ADD COLUMN signature text NOT NULL DEFAULT '' CHECK (char_length(signature) <= 8000);
ALTER TABLE drops ADD COLUMN provenance_verified boolean NOT NULL DEFAULT false;

CREATE TABLE lfs_locks (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    path text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 200),
    owner_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (repository_id, path)
);

CREATE TABLE oci_blobs (
    name text NOT NULL,
    digest text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0 AND size_bytes <= 4194304),
    owner_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (name, digest)
);

CREATE TABLE oci_manifests (
    name text NOT NULL,
    reference text NOT NULL,
    digest text NOT NULL,
    media_type text NOT NULL,
    owner_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (name, reference)
);
