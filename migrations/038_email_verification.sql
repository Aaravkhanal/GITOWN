ALTER TABLE users ADD COLUMN email_verified_at timestamptz;
-- Existing accounts predate verification and remain usable after upgrade.
UPDATE users SET email_verified_at=created_at WHERE email_verified_at IS NULL;

CREATE TABLE email_verification_tokens (
    token_hash text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    CHECK (expires_at > created_at)
);
CREATE INDEX email_verification_user_recent ON email_verification_tokens(user_id, created_at DESC);
