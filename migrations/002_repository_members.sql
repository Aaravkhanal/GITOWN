CREATE TABLE repository_members (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('read', 'triage', 'write', 'maintain')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(repository_id, user_id)
);
CREATE INDEX repository_members_user ON repository_members(user_id, repository_id);
