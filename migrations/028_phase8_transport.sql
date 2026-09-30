-- Tracks when a repository was last garbage-collected, so the periodic
-- maintenance sweep can find repositories that are overdue and the UI can
-- show when maintenance last ran. NULL means never maintained.
ALTER TABLE repositories ADD COLUMN last_maintained_at timestamptz;
