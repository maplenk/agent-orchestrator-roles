-- +goose Up
-- +goose StatementBegin
-- Durable manual-failover attempt record (Phase 3B, PHASE3B_MVP_CONTRACT §5).
-- One row per continuation attempt for one incident.
--
-- Deliberately a table of its own rather than more lifecycle_ledger rows. The
-- ledger is an append-only audit of saga phases; this is the authoritative
-- answer to "which rungs has this incident already spent, and is one in flight
-- right now?" — a question Continue must answer with a single indexed read
-- before it touches anything, and which must survive a crash between the ledger
-- write and the launch. Deriving it by folding the ledger would make the answer
-- depend on replaying phases, which is precisely the state a crash truncates.
--
-- The lifecycle ledger itself needs no migration: 'failover' has been in the
-- kind CHECK since 9002 (re-stated in 9006).
CREATE TABLE session_failover_attempts (
    -- '<sessionId>:<incidentId>:<seq>'. The same composition rule the ledger
    -- primary key uses, which is why domain.ValidateIncidentID excludes ':'.
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    incident_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    role_id TEXT NOT NULL DEFAULT '',
    from_harness TEXT NOT NULL DEFAULT '',
    from_model TEXT NOT NULL DEFAULT '',
    to_harness TEXT NOT NULL DEFAULT '',
    to_model TEXT NOT NULL DEFAULT '',
    -- Position in roleMap.failover.roles[role_id] that was selected, kept
    -- durable so an audit can still name the spent ladder entry after the role
    -- map is edited. -1 means "not resolved from a ladder position".
    rung_index INTEGER NOT NULL DEFAULT -1,
    -- The switch saga's generation (== sessions.runtime_launch_id). NEVER
    -- empty: Continue mints it before this row is written and pins it into the
    -- saga via SwitchRequest.ForceGenerationID (contract §6b), so crash
    -- recovery matches an attempt to its runtime by identity rather than by
    -- guessing from (to_harness, to_model, role_id). The DEFAULT '' exists only
    -- so the column is well-defined for a future backfill; no writer uses it.
    generation_id TEXT NOT NULL DEFAULT '',
    -- Mirrors domain.FailoverAttemptState.Valid(). A state Go accepts but
    -- SQLite rejects is a feature that cannot run.
    --
    -- 'post_stop' is NOT terminal (contract §6a): the source is stopped and the
    -- handoff retained, so the attempt is completed on the SAME generation by
    -- whichever reaches it first — an operator Continue that adopts it, or
    -- boot's existing post_stop recovery. 'failed' is a PRE-STOP failure only.
    state TEXT NOT NULL CHECK (state IN ('requested', 'post_stop', 'acked', 'failed')),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    -- One row per sequence number per incident. This is what makes the attempt
    -- append idempotent under retry: a duplicate Continue that got as far as
    -- computing the same seq collides here instead of spending a second rung.
    UNIQUE (session_id, incident_id, seq)
);
-- The read Continue and FailoverPreview both make, every time.
CREATE INDEX idx_session_failover_attempts_incident
    ON session_failover_attempts(session_id, incident_id, seq);
-- Reconciliation matches a crash-interrupted attempt to its switch generation.
CREATE INDEX idx_session_failover_attempts_generation
    ON session_failover_attempts(session_id, generation_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_session_failover_attempts_generation;
DROP INDEX IF EXISTS idx_session_failover_attempts_incident;
DROP TABLE IF EXISTS session_failover_attempts;
-- +goose StatementEnd
