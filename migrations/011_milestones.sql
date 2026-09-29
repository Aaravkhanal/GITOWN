CREATE TABLE milestones (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 10000),
    state text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    due_date date,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX milestones_repository_title ON milestones(repository_id, lower(title));
CREATE INDEX milestones_repository_state ON milestones(repository_id, state, created_at);

ALTER TABLE issues ADD COLUMN milestone_id uuid REFERENCES milestones(id) ON DELETE SET NULL;
CREATE INDEX issues_milestone_id ON issues(milestone_id);
