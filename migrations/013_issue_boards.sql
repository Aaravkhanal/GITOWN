CREATE TABLE issue_board_status (
    issue_id uuid PRIMARY KEY REFERENCES issues(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status IN ('todo', 'progress', 'done')),
    updated_at timestamptz NOT NULL DEFAULT now()
);
