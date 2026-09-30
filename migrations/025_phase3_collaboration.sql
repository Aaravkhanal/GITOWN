-- Invitations can be revoked, and only one pending invitation may exist per address.
ALTER TABLE repository_invitations DROP CONSTRAINT repository_invitations_status_check;
ALTER TABLE repository_invitations ADD CONSTRAINT repository_invitations_status_check CHECK (status IN ('pending', 'accepted', 'declined', 'expired', 'revoked'));
ALTER TABLE repository_invitations ADD COLUMN responded_by uuid REFERENCES users(id);
ALTER TABLE repository_invitations ADD COLUMN responded_at timestamptz;
UPDATE repository_invitations SET status = 'expired'
WHERE status = 'pending' AND id NOT IN (
    SELECT DISTINCT ON (repository_id, email) id FROM repository_invitations
    WHERE status = 'pending' ORDER BY repository_id, email, created_at DESC
);
CREATE UNIQUE INDEX repository_invitations_one_pending ON repository_invitations(repository_id, email) WHERE status = 'pending';

-- Branch protection: named reviewers or crews, and per-branch force-push and deletion policy.
ALTER TABLE repository_branch_rules ADD COLUMN required_reviewers text[] NOT NULL DEFAULT '{}';
ALTER TABLE repository_branch_rules ADD COLUMN allow_force_push boolean NOT NULL DEFAULT false;
ALTER TABLE repository_branch_rules ADD COLUMN allow_deletion boolean NOT NULL DEFAULT false;

-- CI systems link a status to its run.
ALTER TABLE commit_statuses ADD COLUMN target_url text NOT NULL DEFAULT '' CHECK (char_length(target_url) <= 500);
