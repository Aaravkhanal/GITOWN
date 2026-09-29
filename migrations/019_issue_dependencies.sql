ALTER TABLE issues ADD CONSTRAINT issues_id_repository_unique UNIQUE (id, repository_id);

CREATE TABLE issue_dependencies (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    issue_id uuid NOT NULL,
    blocker_id uuid NOT NULL,
    PRIMARY KEY (issue_id, blocker_id),
    CHECK (issue_id <> blocker_id),
    FOREIGN KEY (issue_id, repository_id) REFERENCES issues(id, repository_id) ON DELETE CASCADE,
    FOREIGN KEY (blocker_id, repository_id) REFERENCES issues(id, repository_id) ON DELETE CASCADE
);

CREATE INDEX issue_dependencies_blocker ON issue_dependencies(blocker_id);
