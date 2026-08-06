-- +goose Up
-- +goose StatementBegin
-- Admit 'orchestrator_fresh_conversation' to the lifecycle ledger.
--
-- 2B-1 added the kind in Go (domain.LifecycleKindOrchestratorFresh) but not
-- here, so every orchestrator fresh conversation failed at its FIRST ledger
-- insert with "CHECK constraint failed" — before fleet observation, before the
-- source was stopped. The feature was unusable against the real store while
-- every manager test passed, because the in-memory fake accepts any kind.
--
-- SQLite cannot ALTER a CHECK constraint, so the table is rebuilt. Same shape
-- as the 0014/0020/0023 rebuilds of review_run: create with the new
-- constraint, copy, drop, rename, recreate indexes. The copy is a plain
-- INSERT..SELECT of every column, so no row is reinterpreted on the way
-- across; only the constraint changes.
--
-- Ordering note: the new table is created under a temporary name and renamed
-- last, so a failure at any point leaves the original intact. goose runs this
-- in a transaction, which makes that atomic, but the ordering holds without
-- relying on it.
CREATE TABLE lifecycle_ledger_new (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'switch', 'pause', 'resume', 'failover', 'fresh_conversation',
        'orchestrator_fresh_conversation'
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

INSERT INTO lifecycle_ledger_new (
    id, session_id, project_id, kind, phase, generation_id,
    from_harness, to_harness, from_model, to_model, role_id,
    source_native_session_id, target_native_session_id, payload_json, created_at
)
SELECT
    id, session_id, project_id, kind, phase, generation_id,
    from_harness, to_harness, from_model, to_model, role_id,
    source_native_session_id, target_native_session_id, payload_json, created_at
FROM lifecycle_ledger;

DROP TABLE lifecycle_ledger;
ALTER TABLE lifecycle_ledger_new RENAME TO lifecycle_ledger;

CREATE INDEX idx_lifecycle_ledger_session_created
    ON lifecycle_ledger(session_id, created_at DESC);
CREATE INDEX idx_lifecycle_ledger_project_created
    ON lifecycle_ledger(project_id, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Reversible only while no orchestrator_fresh_conversation rows exist; those
-- would violate the narrower constraint. Dev-only, like 0057's Down.
DELETE FROM lifecycle_ledger WHERE kind = 'orchestrator_fresh_conversation';

CREATE TABLE lifecycle_ledger_old (
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
INSERT INTO lifecycle_ledger_old SELECT * FROM lifecycle_ledger;
DROP TABLE lifecycle_ledger;
ALTER TABLE lifecycle_ledger_old RENAME TO lifecycle_ledger;
CREATE INDEX idx_lifecycle_ledger_session_created
    ON lifecycle_ledger(session_id, created_at DESC);
CREATE INDEX idx_lifecycle_ledger_project_created
    ON lifecycle_ledger(project_id, created_at DESC);
-- +goose StatementEnd
