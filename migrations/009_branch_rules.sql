CREATE TABLE repository_branch_rules (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    branch text NOT NULL,
    required_approvals smallint NOT NULL DEFAULT 0 CHECK (required_approvals BETWEEN 0 AND 10),
    block_changes_requested boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, branch)
);
