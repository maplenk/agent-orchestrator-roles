-- +goose Up
-- +goose StatementBegin
-- Append-only lifecycle ledger for switch / pause / resume / failover / fresh.
-- Not a full chat transcript. Used for audit and switch-history restore.
CREATE TABLE lifecycle_ledger (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'switch', 'pause', 'resume', 'failover', 'fresh_conversation'
    )),
    phase TEXT NOT NULL DEFAULT '',
    generation_id TEXT NOT NULL DEFAULT '',
    from_harness TEXT NOT NULL DEFAULT '',
    to_harness TEXT NOT NULL DEFAULT '',
    from_model TEXT NOT NULL DEFAULT '',
    to_model TEXT NOT NULL DEFAULT '',
    role_id TEXT NOT NULL DEFAULT '',
    source_native_session_id TEXT NOT NULL DEFAULT '',
    target_native_session_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_lifecycle_ledger_session_created
    ON lifecycle_ledger(session_id, created_at DESC);
CREATE INDEX idx_lifecycle_ledger_project_created
    ON lifecycle_ledger(project_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_lifecycle_ledger_project_created;
DROP INDEX IF EXISTS idx_lifecycle_ledger_session_created;
DROP TABLE IF EXISTS lifecycle_ledger;
-- +goose StatementEnd
