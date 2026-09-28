ALTER TABLE sessions ADD COLUMN id text;
UPDATE sessions SET id=left(token_hash, 16);
ALTER TABLE sessions ALTER COLUMN id SET NOT NULL;
ALTER TABLE sessions ADD CONSTRAINT sessions_id_unique UNIQUE (id);
ALTER TABLE sessions ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE sessions ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE sessions ADD COLUMN ip_address text NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN user_agent text NOT NULL DEFAULT '';

CREATE INDEX sessions_user_created ON sessions(user_id, created_at DESC);
