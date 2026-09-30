-- OAuth applications (standard authorization-code flow) and GITOWN Apps
-- (installable, per-repository identity distinct from a user's own OAuth
-- grant). Both reuse existing machinery rather than inventing parallel
-- authorization paths:
--   * OAuth-issued access tokens live in the existing access_tokens table
--     (a nullable oauth_app_id + refresh_token_hash), so every Bearer-auth
--     check site added in earlier phases works unchanged.
--   * A GITOWN App gets one synthetic "bot" user. Installing the app grants
--     that bot user a normal repository_members row scoped to exactly the
--     installed repository, so the existing decorate()/CanWrite machinery
--     enforces the installation's access with no new authorization logic.

ALTER TABLE users ADD COLUMN is_bot boolean NOT NULL DEFAULT false;

CREATE TABLE oauth_apps (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    homepage_url text NOT NULL DEFAULT '' CHECK (char_length(homepage_url) <= 300),
    redirect_uri text NOT NULL CHECK (char_length(redirect_uri) BETWEEN 1 AND 500),
    client_id text NOT NULL UNIQUE,
    client_secret_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_apps_owner ON oauth_apps(owner_id);

-- One row per issued authorization code; codes are single-use and expire
-- quickly, matching the OAuth spec's short-lived-code expectation.
CREATE TABLE oauth_codes (
    code_hash text PRIMARY KEY,
    oauth_app_id uuid NOT NULL REFERENCES oauth_apps(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('repo:read', 'repo:write', 'package:read', 'package:write')),
    redirect_uri text NOT NULL,
    expires_at timestamptz NOT NULL,
    used boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Tracks that a user has granted an app a scope, independent of any one
-- token, so the user's "authorized applications" list and a future re-auth
-- (which reuses the existing grant instead of prompting again) both have
-- something to read, and revoking here can cascade to every token issued
-- under the grant.
CREATE TABLE oauth_authorizations (
    oauth_app_id uuid NOT NULL REFERENCES oauth_apps(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (scope IN ('repo:read', 'repo:write', 'package:read', 'package:write')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (oauth_app_id, user_id)
);

ALTER TABLE access_tokens ADD COLUMN oauth_app_id uuid REFERENCES oauth_apps(id) ON DELETE CASCADE;
ALTER TABLE access_tokens ADD COLUMN refresh_token_hash text UNIQUE;
CREATE INDEX access_tokens_oauth_app ON access_tokens(oauth_app_id, user_id) WHERE oauth_app_id IS NOT NULL;

CREATE TABLE gitown_apps (
    id uuid PRIMARY KEY,
    owner_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    homepage_url text NOT NULL DEFAULT '' CHECK (char_length(homepage_url) <= 300),
    webhook_url text NOT NULL DEFAULT '' CHECK (char_length(webhook_url) <= 2000),
    requested_scope text NOT NULL CHECK (requested_scope IN ('repo:read', 'repo:write')),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gitown_apps_owner ON gitown_apps(owner_id);
CREATE UNIQUE INDEX gitown_apps_bot_user ON gitown_apps(bot_user_id);

-- Installation is repository-scoped only in this implementation (a bounded
-- simplification from the roadmap's "per-repo/per-district" — see
-- docs/PRODUCT_PHASES.md for why district-wide installation is deferred).
CREATE TABLE gitown_app_installations (
    id uuid PRIMARY KEY,
    gitown_app_id uuid NOT NULL REFERENCES gitown_apps(id) ON DELETE CASCADE,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    granted_scope text NOT NULL CHECK (granted_scope IN ('repo:read', 'repo:write')),
    installed_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gitown_app_id, repository_id)
);
CREATE INDEX gitown_app_installations_repo ON gitown_app_installations(repository_id);

ALTER TABLE access_tokens ADD COLUMN installation_id uuid REFERENCES gitown_app_installations(id) ON DELETE CASCADE;

-- Slack- and Discord-formatted deliveries reuse the webhooks/webhook_deliveries
-- outbox Stream A built; only the payload shape differs per kind at send time.
ALTER TABLE webhooks ADD COLUMN kind text NOT NULL DEFAULT 'generic' CHECK (kind IN ('generic', 'slack', 'discord'));
