CREATE TABLE labels (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
    color text NOT NULL CHECK (color ~ '^[0-9a-f]{6}$'),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 200),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX labels_repository_name ON labels(repository_id, lower(name));

CREATE TABLE issue_labels (
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (issue_id, label_id)
);
