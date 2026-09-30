-- OCI image namespaces now share the crates table (ecosystem='oci') so every
-- push/pull goes through the same ownership and visibility checks a crate
-- already gets, instead of the OCI routes having no access model at all.
ALTER TABLE crates DROP CONSTRAINT crates_ecosystem_check;
ALTER TABLE crates ADD CONSTRAINT crates_ecosystem_check CHECK (ecosystem IN ('npm', 'oci'));

-- Real MIME type recorded from the uploaded bytes, instead of every asset
-- being served back as application/octet-stream regardless of its content.
ALTER TABLE drop_assets ADD COLUMN content_type text NOT NULL DEFAULT 'application/octet-stream';

-- Download-count dedupe: one increment per (asset, identity, hour) instead
-- of every single GET inflating the counter unconditionally.
CREATE TABLE drop_asset_downloads (
    asset_id uuid NOT NULL REFERENCES drop_assets(id) ON DELETE CASCADE,
    identity text NOT NULL CHECK (char_length(identity) <= 200),
    bucket timestamptz NOT NULL,
    PRIMARY KEY (asset_id, identity, bucket)
);
