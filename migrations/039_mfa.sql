CREATE TABLE user_mfa (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret_cipher bytea,
    secret_nonce bytea,
    pending_secret_cipher bytea,
    pending_secret_nonce bytea,
    pending_expires_at timestamptz,
    enabled_at timestamptz,
    last_totp_step bigint NOT NULL DEFAULT -1,
    CHECK ((enabled_at IS NULL) = (secret_cipher IS NULL)),
    CHECK ((secret_cipher IS NULL) = (secret_nonce IS NULL)),
    CHECK ((pending_secret_cipher IS NULL) = (pending_secret_nonce IS NULL))
);

CREATE TABLE mfa_recovery_codes (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE mfa_login_challenges (
    token_hash text PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);
CREATE INDEX mfa_login_challenge_user ON mfa_login_challenges(user_id, created_at DESC);
