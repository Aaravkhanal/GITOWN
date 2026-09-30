-- Signed webhook subscriptions, scoped to either a repository or a district
-- (never both), and a durable delivery outbox modeled on the Phase 5 email
-- outbox: enqueue inside the triggering transaction, deliver and retry from
-- a background worker.
CREATE TABLE webhooks (
    id uuid PRIMARY KEY,
    repository_id uuid REFERENCES repositories(id) ON DELETE CASCADE,
    district_id uuid REFERENCES districts(id) ON DELETE CASCADE,
    url text NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2000),
    secret_ciphertext bytea NOT NULL,
    secret_nonce bytea NOT NULL,
    events text[] NOT NULL CHECK (array_length(events, 1) BETWEEN 1 AND 20),
    active boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((repository_id IS NULL) <> (district_id IS NULL))
);
CREATE INDEX webhooks_repository ON webhooks(repository_id) WHERE repository_id IS NOT NULL;
CREATE INDEX webhooks_district ON webhooks(district_id) WHERE district_id IS NOT NULL;

CREATE TABLE webhook_deliveries (
    id uuid PRIMARY KEY,
    webhook_id uuid NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event text NOT NULL,
    payload jsonb NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'success', 'failed')),
    attempts int NOT NULL DEFAULT 0,
    response_status int,
    response_body text,
    last_error text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz
);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries(next_attempt_at) WHERE status = 'pending';
CREATE INDEX webhook_deliveries_webhook ON webhook_deliveries(webhook_id, created_at DESC);
