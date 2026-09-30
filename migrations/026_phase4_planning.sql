-- Issue tracking depth: sub-issues, close reasons, structured issue forms,
-- stored cross-references, configurable boards, custom fields, and pull
-- requests on boards.

ALTER TABLE issues ADD COLUMN parent_id uuid REFERENCES issues(id) ON DELETE SET NULL;
ALTER TABLE issues ADD CONSTRAINT issues_parent_not_self CHECK (parent_id IS NULL OR parent_id <> id);
CREATE INDEX issues_parent ON issues(parent_id) WHERE parent_id IS NOT NULL;
ALTER TABLE issues ADD COLUMN state_reason text NOT NULL DEFAULT '' CHECK (state_reason IN ('', 'completed', 'not_planned', 'duplicate'));
ALTER TABLE issues ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX issues_listing ON issues(repository_id, state, pinned, number);

ALTER TABLE issue_templates ADD COLUMN fields jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(fields) = 'array');

CREATE TABLE issue_transfers (
    source_repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    source_number integer NOT NULL,
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_repository_id, source_number)
);

-- A reference is recorded when an issue, unite request, or comment body
-- mentions #N or owner/repo#N. Sources are rewritten whenever they are edited.
CREATE TABLE issue_references (
    id uuid PRIMARY KEY,
    target_issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    source_kind text NOT NULL CHECK (source_kind IN ('issue', 'issue_comment', 'pull', 'pull_comment')),
    source_id uuid NOT NULL,
    source_repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    source_issue_id uuid REFERENCES issues(id) ON DELETE CASCADE,
    source_pull_id uuid REFERENCES pull_requests(id) ON DELETE CASCADE,
    actor_id uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((source_issue_id IS NULL) <> (source_pull_id IS NULL)),
    UNIQUE (source_kind, source_id, target_issue_id)
);
CREATE INDEX issue_references_target ON issue_references(target_issue_id, created_at);

-- Boards: columns are ordered {key,name} objects. The "done" column always
-- exists; moving an issue there closes it.
CREATE TABLE boards (
    repository_id uuid PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
    columns jsonb NOT NULL CHECK (jsonb_typeof(columns) = 'array'),
    automation boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE issue_board_status DROP CONSTRAINT IF EXISTS issue_board_status_status_check;
ALTER TABLE issue_board_status ADD CONSTRAINT issue_board_status_key CHECK (status ~ '^[a-z0-9][a-z0-9-]{0,29}$');

CREATE TABLE pull_board_items (
    pull_request_id uuid PRIMARY KEY REFERENCES pull_requests(id) ON DELETE CASCADE,
    status text NOT NULL CHECK (status ~ '^[a-z0-9][a-z0-9-]{0,29}$'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE board_fields (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 40),
    kind text NOT NULL CHECK (kind IN ('text', 'number', 'date', 'single_select')),
    options jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(options) = 'array'),
    position integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX board_fields_name ON board_fields(repository_id, lower(name));

CREATE TABLE board_field_values (
    field_id uuid NOT NULL REFERENCES board_fields(id) ON DELETE CASCADE,
    issue_id uuid REFERENCES issues(id) ON DELETE CASCADE,
    pull_request_id uuid REFERENCES pull_requests(id) ON DELETE CASCADE,
    value text NOT NULL CHECK (char_length(value) BETWEEN 1 AND 200),
    CHECK ((issue_id IS NULL) <> (pull_request_id IS NULL))
);
CREATE UNIQUE INDEX board_field_values_issue ON board_field_values(field_id, issue_id) WHERE issue_id IS NOT NULL;
CREATE UNIQUE INDEX board_field_values_pull ON board_field_values(field_id, pull_request_id) WHERE pull_request_id IS NOT NULL;
