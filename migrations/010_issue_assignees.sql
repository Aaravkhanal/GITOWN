CREATE TABLE issue_assignees (
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issue_id, user_id)
);

CREATE INDEX issue_assignees_user_idx ON issue_assignees(user_id, created_at DESC);
