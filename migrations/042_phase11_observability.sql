-- Durable claim leases let another process recover work after a worker exits
-- while a row is marked sending. Delivery remains at-least-once.
ALTER TABLE email_messages ADD COLUMN claimed_at timestamptz;
ALTER TABLE webhook_deliveries ADD COLUMN claimed_at timestamptz;

-- Pre-lease sending rows cannot be distinguished from active claims.
UPDATE email_messages SET status='pending',last_error='recovered during worker lease migration' WHERE status='sending';
UPDATE webhook_deliveries SET status='pending',last_error='recovered during worker lease migration' WHERE status='sending';

CREATE INDEX email_messages_claim_lease ON email_messages(claimed_at) WHERE status='sending';
CREATE INDEX webhook_deliveries_claim_lease ON webhook_deliveries(claimed_at) WHERE status='sending';
