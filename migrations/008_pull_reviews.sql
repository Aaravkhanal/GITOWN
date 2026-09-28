CREATE TABLE pull_reviews (
    id uuid PRIMARY KEY,
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN ('approved', 'changes_requested', 'commented')),
    body text NOT NULL DEFAULT '',
    head_sha text NOT NULL CHECK (length(head_sha) = 40),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX pull_reviews_pull_created ON pull_reviews(pull_request_id, created_at, id);
CREATE INDEX pull_reviews_reviewer ON pull_reviews(reviewer_id, created_at DESC);
