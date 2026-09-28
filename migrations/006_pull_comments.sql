CREATE TABLE pull_comments (
    id uuid PRIMARY KEY,
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pull_comments_timeline ON pull_comments(pull_request_id, created_at, id);
