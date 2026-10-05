CREATE TABLE auth_rate_limit_buckets (
    bucket_hash text PRIMARY KEY,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    window_started_at timestamptz NOT NULL,
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_rate_limit_cleanup ON auth_rate_limit_buckets(updated_at);

CREATE TABLE auth_abuse_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL,
    scope_hash text NOT NULL,
    ip_hash text NOT NULL,
    route text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX auth_abuse_recent ON auth_abuse_events(created_at DESC);
