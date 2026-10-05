ALTER TABLE sessions ADD COLUMN step_up_at timestamptz;

CREATE INDEX sessions_step_up_expiry ON sessions(step_up_at) WHERE step_up_at IS NOT NULL;
