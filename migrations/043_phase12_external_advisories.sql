-- External advisory snapshots are scoped to the repository that was scanned.
-- The upstream record stays attributable and can be refreshed without
-- producing duplicate alerts on every scan.
ALTER TABLE security_advisories
    ADD COLUMN external_source text NOT NULL DEFAULT '',
    ADD COLUMN external_id text NOT NULL DEFAULT '',
    ADD COLUMN external_url text NOT NULL DEFAULT '',
    ADD COLUMN external_version text NOT NULL DEFAULT '';

ALTER TABLE security_advisories
    DROP CONSTRAINT security_advisories_code_check,
    ADD CONSTRAINT security_advisories_code_check
        CHECK (code ~ '^GOWN-[A-F0-9]{8}$' OR code ~ '^OSV-[A-F0-9]{32}$');

CREATE UNIQUE INDEX security_advisories_external_identity
    ON security_advisories(repository_id, external_source, external_id, package_name, external_version)
    WHERE external_source <> '' AND external_id <> '';

ALTER TABLE security_advisories
    ADD CONSTRAINT security_advisories_external_source_length
        CHECK (char_length(external_source) <= 32),
    ADD CONSTRAINT security_advisories_external_id_length
        CHECK (char_length(external_id) <= 120),
    ADD CONSTRAINT security_advisories_external_url_length
        CHECK (char_length(external_url) <= 500),
    ADD CONSTRAINT security_advisories_external_version_length
        CHECK (char_length(external_version) <= 80);
