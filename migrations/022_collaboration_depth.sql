ALTER TABLE pull_requests ADD COLUMN draft boolean NOT NULL DEFAULT false;
ALTER TABLE pull_requests ADD COLUMN merge_method text CHECK (merge_method IS NULL OR merge_method IN ('merge', 'squash', 'rebase'));

ALTER TABLE pull_reviews ADD COLUMN dismissed_at timestamptz;
ALTER TABLE pull_reviews ADD COLUMN dismissed_by uuid REFERENCES users(id);
ALTER TABLE pull_reviews ADD COLUMN dismissal_reason text NOT NULL DEFAULT '' CHECK (char_length(dismissal_reason) <= 2000);

CREATE TABLE pull_review_threads (
    id uuid PRIMARY KEY,
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    commit_sha text NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    path text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 4096),
    side text NOT NULL CHECK (side IN ('left', 'right')),
    line integer NOT NULL CHECK (line BETWEEN 1 AND 100000),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz,
    resolved_by uuid REFERENCES users(id)
);
CREATE INDEX pull_review_threads_pull ON pull_review_threads(pull_request_id, created_at, id);

CREATE TABLE pull_thread_replies (
    id uuid PRIMARY KEY,
    thread_id uuid NOT NULL REFERENCES pull_review_threads(id) ON DELETE CASCADE,
    author_id uuid NOT NULL REFERENCES users(id),
    body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pull_review_requests (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    reviewer_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, reviewer_id)
);

CREATE TABLE pull_assignees (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_request_id, user_id)
);

CREATE TABLE pull_labels (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (pull_request_id, label_id)
);

CREATE TABLE pull_issue_links (
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    issue_id uuid NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    closes boolean NOT NULL DEFAULT false,
    PRIMARY KEY (pull_request_id, issue_id)
);

CREATE TABLE pull_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    pull_request_id uuid NOT NULL REFERENCES pull_requests(id) ON DELETE CASCADE,
    actor_id uuid REFERENCES users(id),
    kind text NOT NULL,
    body text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pull_events_timeline ON pull_events(pull_request_id, created_at, id);

ALTER TABLE repository_branch_rules ADD COLUMN require_resolved boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN require_up_to_date boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN restrict_push boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN require_signed boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN require_maintainer_approval boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN required_checks text[] NOT NULL DEFAULT '{}';

CREATE TABLE commit_statuses (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    sha text NOT NULL CHECK (sha ~ '^[0-9a-f]{40}$'),
    context text NOT NULL CHECK (char_length(context) BETWEEN 1 AND 40),
    state text NOT NULL CHECK (state IN ('pending', 'success', 'failure', 'error')),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 300),
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, sha, context)
);

CREATE TABLE repository_invitations (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    email text NOT NULL,
    username text,
    role text NOT NULL CHECK (role IN ('read', 'triage', 'write', 'maintain')),
    token_hash text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined', 'expired')),
    invited_by uuid NOT NULL REFERENCES users(id),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX repository_invitations_repo ON repository_invitations(repository_id, created_at DESC);

CREATE TABLE ownership_transfers (
    id uuid PRIMARY KEY,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    from_user_id uuid NOT NULL REFERENCES users(id),
    to_user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'declined', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ownership_transfers_one_pending ON ownership_transfers(repository_id) WHERE status = 'pending';

ALTER TABLE issues ADD COLUMN pinned boolean NOT NULL DEFAULT false;
ALTER TABLE issues ADD COLUMN duplicate_of uuid REFERENCES issues(id) ON DELETE SET NULL;
ALTER TABLE issues ADD COLUMN priority text NOT NULL DEFAULT 'none' CHECK (priority IN ('none', 'low', 'medium', 'high', 'urgent'));
ALTER TABLE issues ADD COLUMN estimate smallint CHECK (estimate IS NULL OR estimate BETWEEN 0 AND 100);
ALTER TABLE issues ADD COLUMN due_date date;
ALTER TABLE issues ADD COLUMN iteration text NOT NULL DEFAULT '' CHECK (char_length(iteration) <= 40);

ALTER TABLE issue_comments ADD COLUMN updated_at timestamptz;
ALTER TABLE pull_comments ADD COLUMN updated_at timestamptz;

CREATE TABLE issue_comment_revisions (
    id uuid PRIMARY KEY,
    comment_id uuid NOT NULL REFERENCES issue_comments(id) ON DELETE CASCADE,
    body text NOT NULL,
    edited_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE pull_comment_revisions (
    id uuid PRIMARY KEY,
    comment_id uuid NOT NULL REFERENCES pull_comments(id) ON DELETE CASCADE,
    body text NOT NULL,
    edited_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE issue_subscriptions ADD COLUMN mode text NOT NULL DEFAULT 'participate' CHECK (mode IN ('watch', 'ignore', 'participate'));
ALTER TABLE pull_subscriptions ADD COLUMN mode text NOT NULL DEFAULT 'participate' CHECK (mode IN ('watch', 'ignore', 'participate'));

ALTER TABLE issue_templates ADD COLUMN kind text NOT NULL DEFAULT 'custom' CHECK (kind IN ('bug', 'feature', 'custom'));

ALTER TABLE users ADD COLUMN skills text NOT NULL DEFAULT '' CHECK (char_length(skills) <= 200);
ALTER TABLE users ADD COLUMN availability text NOT NULL DEFAULT '' CHECK (char_length(availability) <= 200);
ALTER TABLE users ADD COLUMN open_to_collaborators boolean NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN email_notifications text NOT NULL DEFAULT 'immediate' CHECK (email_notifications IN ('immediate', 'digest', 'off'));

ALTER TABLE repositories ADD COLUMN homepage text NOT NULL DEFAULT '' CHECK (char_length(homepage) <= 300);
ALTER TABLE repositories ADD COLUMN stack text NOT NULL DEFAULT '' CHECK (char_length(stack) <= 200);

CREATE TABLE saved_searches (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    query text NOT NULL CHECK (char_length(query) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE TABLE email_messages (
    id uuid PRIMARY KEY,
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    recipient_email text NOT NULL,
    kind text NOT NULL,
    subject text NOT NULL,
    body text NOT NULL,
    unsubscribe_token text NOT NULL UNIQUE,
    digest boolean NOT NULL DEFAULT false,
    dedupe_key text,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz
);
CREATE UNIQUE INDEX email_messages_dedupe ON email_messages(user_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX email_messages_unsent ON email_messages(user_id, created_at) WHERE sent_at IS NULL;

ALTER TABLE notifications ADD COLUMN invitation_id uuid REFERENCES repository_invitations(id) ON DELETE CASCADE;
ALTER TABLE notifications ADD COLUMN transfer_id uuid REFERENCES ownership_transfers(id) ON DELETE CASCADE;
ALTER TABLE notifications ADD COLUMN dedupe_key text;
ALTER TABLE notifications DROP CONSTRAINT notifications_one_subject;
ALTER TABLE notifications ADD CONSTRAINT notifications_one_subject CHECK (num_nonnulls(issue_id, pull_request_id, invitation_id, transfer_id) = 1);
ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check CHECK (kind IN (
    'issue_comment', 'issue_closed', 'issue_reopened',
    'pull_comment', 'pull_review', 'pull_closed', 'pull_reopened', 'pull_merged',
    'mention', 'assignment', 'review_request', 'invitation', 'ownership_transfer',
    'check_success', 'check_failure'
));
CREATE UNIQUE INDEX notifications_dedupe ON notifications(recipient_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
