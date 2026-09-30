-- Durable email outbox. Transactions only enqueue; a background worker
-- claims due rows, sends them, and records the real outcome.
ALTER TABLE email_messages ALTER COLUMN unsubscribe_token DROP NOT NULL;
ALTER TABLE email_messages ADD COLUMN status text NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'sending', 'sent', 'failed', 'suppressed', 'bundled'));
ALTER TABLE email_messages ADD COLUMN attempts smallint NOT NULL DEFAULT 0;
ALTER TABLE email_messages ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE email_messages ADD COLUMN last_error text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 300);
ALTER TABLE email_messages ADD COLUMN link_path text NOT NULL DEFAULT '' CHECK (char_length(link_path) <= 300);
ALTER TABLE email_messages ADD COLUMN unsubscribe_expires_at timestamptz;
ALTER TABLE email_messages ADD COLUMN digest_id uuid REFERENCES email_messages(id) ON DELETE SET NULL;
-- Older rows were stamped sent_at without any delivery attempt; keep them
-- out of the queue instead of mailing stale updates.
UPDATE email_messages SET status = CASE WHEN sent_at IS NULL THEN 'suppressed' ELSE 'sent' END,
    last_error = CASE WHEN sent_at IS NULL THEN 'queued before durable delivery' ELSE '' END;
-- Plaintext unsubscribe tokens were previously embedded in stored bodies.
UPDATE email_messages SET body = split_part(body, E'\n\nUnsubscribe by opening GITOWN with this token: ', 1);
UPDATE email_messages SET body = split_part(body, ' Token: inv_', 1) WHERE kind = 'invitation';
DROP INDEX email_messages_unsent;
CREATE INDEX email_messages_due ON email_messages(next_attempt_at) WHERE status = 'pending' AND NOT digest;
CREATE INDEX email_messages_digest_due ON email_messages(user_id, created_at) WHERE status = 'pending' AND digest;
CREATE UNIQUE INDEX email_messages_address_dedupe ON email_messages(recipient_email, dedupe_key)
    WHERE user_id IS NULL AND dedupe_key IS NOT NULL;

-- Notifications: follow notices have no repository, other kinds keep exactly
-- one subject. Kinds are validated in Go so later migrations can add kinds
-- without rewriting this constraint.
ALTER TABLE notifications ALTER COLUMN repository_id DROP NOT NULL;
ALTER TABLE notifications ADD COLUMN excerpt text NOT NULL DEFAULT '' CHECK (char_length(excerpt) <= 280);
ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check CHECK (kind ~ '^[a-z_]{3,40}$');
ALTER TABLE notifications DROP CONSTRAINT notifications_one_subject;
ALTER TABLE notifications ADD CONSTRAINT notifications_one_subject CHECK (
    (kind = 'follow' AND repository_id IS NULL AND num_nonnulls(issue_id, pull_request_id, invitation_id, transfer_id) = 0)
    OR (kind <> 'follow' AND repository_id IS NOT NULL AND num_nonnulls(issue_id, pull_request_id, invitation_id, transfer_id) = 1)
);
CREATE INDEX notifications_recipient_unread ON notifications(recipient_id, id DESC) WHERE read_at IS NULL;

CREATE TABLE repository_subscriptions (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode text NOT NULL CHECK (mode IN ('watching', 'participating', 'ignoring')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, user_id)
);
CREATE INDEX repository_subscriptions_watching ON repository_subscriptions(repository_id) WHERE mode = 'watching';

CREATE TABLE user_links (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position smallint NOT NULL CHECK (position BETWEEN 1 AND 5),
    label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 40),
    url text NOT NULL CHECK (char_length(url) BETWEEN 9 AND 300),
    PRIMARY KEY (user_id, position)
);

CREATE TABLE repository_screenshots (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    position smallint NOT NULL CHECK (position BETWEEN 1 AND 6),
    url text NOT NULL CHECK (char_length(url) BETWEEN 9 AND 300),
    caption text NOT NULL DEFAULT '' CHECK (char_length(caption) <= 140),
    PRIMARY KEY (repository_id, position)
);

CREATE INDEX ref_events_actor ON ref_events(actor_id, created_at DESC) WHERE actor_id IS NOT NULL;
