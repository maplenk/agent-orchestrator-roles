-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN role_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN role_map_schema_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN role_map_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN role_config_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN template_artifact_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN template_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN resolved_model TEXT NOT NULL DEFAULT '';
-- Policy flags are 0/1 only (integer, not JSON). CHECK catches corruption on write.
ALTER TABLE sessions ADD COLUMN resolved_workspace_writes INTEGER NOT NULL DEFAULT 0
    CHECK (resolved_workspace_writes IN (0, 1));
ALTER TABLE sessions ADD COLUMN resolved_can_spawn INTEGER NOT NULL DEFAULT 0
    CHECK (resolved_can_spawn IN (0, 1));

CREATE TABLE template_artifacts (
    id TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    content BLOB NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_template_artifacts_sha256 ON template_artifacts(sha256);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_template_artifacts_sha256;
DROP TABLE template_artifacts;

ALTER TABLE sessions DROP COLUMN resolved_can_spawn;
ALTER TABLE sessions DROP COLUMN resolved_workspace_writes;
ALTER TABLE sessions DROP COLUMN resolved_model;
ALTER TABLE sessions DROP COLUMN template_sha256;
ALTER TABLE sessions DROP COLUMN template_artifact_id;
ALTER TABLE sessions DROP COLUMN role_config_revision;
ALTER TABLE sessions DROP COLUMN role_map_sha256;
ALTER TABLE sessions DROP COLUMN role_map_schema_version;
ALTER TABLE sessions DROP COLUMN role_id;
-- +goose StatementEnd
