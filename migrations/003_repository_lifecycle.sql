ALTER TABLE repositories
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN deleted_at timestamptz,
    ADD COLUMN purge_after timestamptz;
CREATE INDEX repositories_deleted ON repositories(owner_id, deleted_at) WHERE deleted_at IS NOT NULL;
