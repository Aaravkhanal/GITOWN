CREATE TABLE issue_templates (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    title text NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    body text NOT NULL DEFAULT '' CHECK (char_length(body) <= 10000),
    position integer NOT NULL CHECK (position BETWEEN 1 AND 10),
    PRIMARY KEY (repository_id, name),
    UNIQUE (repository_id, position)
);

CREATE UNIQUE INDEX issue_templates_name_folded ON issue_templates(repository_id, lower(name));
