CREATE TABLE pull_subscriptions (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, user_id)
);

ALTER TABLE notifications ALTER COLUMN issue_id DROP NOT NULL;
ALTER TABLE notifications ADD COLUMN pull_request_id uuid REFERENCES pull_requests(id) ON DELETE CASCADE;
ALTER TABLE notifications ADD CONSTRAINT notifications_one_subject CHECK ((issue_id IS NULL) <> (pull_request_id IS NULL));
ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check CHECK (kind IN (
    'issue_comment', 'issue_closed', 'issue_reopened',
    'pull_comment', 'pull_review', 'pull_closed', 'pull_reopened', 'pull_merged'
));
