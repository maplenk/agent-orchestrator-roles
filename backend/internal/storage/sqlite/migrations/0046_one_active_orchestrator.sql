-- +goose Up
-- +goose StatementBegin
-- One active orchestrator per project, enforced by the database.
--
-- Ownership was previously derived ("newest active orchestrator") rather than
-- owned, and two active orchestrators for one project were reachable: Restore
-- and boot RestoreAll take no ownership gate, and the orchestrator worktree is
-- canonical per project, so gitworktree.Create adopts an existing registration
-- rather than failing. Two live orchestrators would silently share one worktree
-- and branch.
--
-- The data must be reconciled BEFORE the index exists. CREATE UNIQUE INDEX
-- fails outright on a database that already holds duplicates
-- (SQLITE_CONSTRAINT_UNIQUE 2067), and goose records goose_db_version inside
-- the same transaction it rolls back — so a failing migration records nothing,
-- retries on every boot, and wedges startup permanently (sqlite.Open runs
-- migrations, and its error aborts daemon.Run before anything can self-heal).
-- Same shape as 0013 and 0020.

-- Evidence first. Unlike 0013's duplicate rows, these duplicates own EXTERNAL
-- PROCESSES: an agent runtime and session-scoped shells that outlive the
-- database write. Terminating a loser and clearing its claim erases the only
-- authoritative probe target (runtime_handle_id / runtime_launch_id), so the
-- boot reaper could no longer confirm execution death — and deriving a handle
-- from the session id is an adapter-specific heuristic, not the stored handle.
--
-- Capture every loser, with its ORIGINAL handle, before anything is cleared.
-- Semantics are delete-on-authoritative-reap: a row's presence means "this
-- execution is still owed a confirmed death", so the queue is drained only as
-- deaths are confirmed, never marked done optimistically. That polarity is
-- deliberate — pr.last_nudge_signature records "already did this", which cannot
-- express an outstanding obligation.
CREATE TABLE orchestrator_reap_queue (
    session_id        TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    project_id        TEXT NOT NULL,
    -- Exact pre-reconciliation execution identity. Empty handle means the row
    -- had none recorded; the reaper must fall back explicitly rather than
    -- treating "no handle" as "already dead".
    runtime_handle_id TEXT NOT NULL DEFAULT '',
    runtime_launch_id TEXT NOT NULL DEFAULT '',
    -- Informational: the canonical workspace the loser was executing in.
    workspace_path    TEXT NOT NULL DEFAULT '',
    queued_at         TIMESTAMP NOT NULL,
    last_attempt_at   TIMESTAMP,
    attempt_count     INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0)
);

CREATE INDEX idx_orchestrator_reap_queue_project ON orchestrator_reap_queue (project_id);

INSERT INTO orchestrator_reap_queue (
    session_id, project_id, runtime_handle_id, runtime_launch_id, workspace_path, queued_at
)
SELECT id, project_id, runtime_handle_id, runtime_launch_id, workspace_path, CURRENT_TIMESTAMP
FROM sessions
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY project_id
            ORDER BY created_at DESC, updated_at DESC, id DESC
        ) AS rn
        FROM sessions
        WHERE kind = 'orchestrator' AND is_terminated = 0
    )
    WHERE rn > 1
);

-- Restore markers next, while the losers are still identifiable as active.
-- RestoreAll relaunches every marker-carrying session, so a terminated loser
-- that kept its marker would be resurrected onto the canonical worktree on the
-- next boot.
DELETE FROM session_worktrees
WHERE session_id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY project_id
            ORDER BY created_at DESC, updated_at DESC, id DESC
        ) AS rn
        FROM sessions
        WHERE kind = 'orchestrator' AND is_terminated = 0
    )
    WHERE rn > 1
);

-- Losers are TERMINATED, never deleted: sessions is referenced by pr, review,
-- notifications, session_worktrees and shell_terminals, and the CDC triggers
-- fan out on session writes.
--
-- The survivor rule mirrors newestOrchestratorRecord in session_manager exactly
-- (newest created_at, then updated_at, then lexically greatest id) so the
-- database and the Go resolver can never disagree about who owns a project.
--
-- Clearing the workspace claim mirrors releaseRetiredWorkspaceClaim: the
-- orchestrator worktree is canonical per project, so a terminated row that
-- keeps naming it aliases the survivor, and any later path-keyed teardown
-- (Kill, Cleanup) would destroy the live orchestrator's worktree. updated_at is
-- deliberately left alone so the historical ordering that chose the survivor
-- stays auditable.
UPDATE sessions
SET is_terminated = 1,
    workspace_path = '',
    workspace_repo_path = '',
    branch = '',
    runtime_handle_id = '',
    runtime_launch_id = ''
WHERE id IN (
    SELECT id FROM (
        SELECT id, ROW_NUMBER() OVER (
            PARTITION BY project_id
            ORDER BY created_at DESC, updated_at DESC, id DESC
        ) AS rn
        FROM sessions
        WHERE kind = 'orchestrator' AND is_terminated = 0
    )
    WHERE rn > 1
);

CREATE UNIQUE INDEX idx_sessions_one_active_orchestrator
    ON sessions (project_id)
    WHERE kind = 'orchestrator' AND is_terminated = 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Best-effort, dev-only: the reconciliation above is not reversible (the losers'
-- prior workspace claims are gone), so Down only removes what it created.
DROP INDEX IF EXISTS idx_sessions_one_active_orchestrator;
DROP INDEX IF EXISTS idx_orchestrator_reap_queue_project;
DROP TABLE IF EXISTS orchestrator_reap_queue;
-- +goose StatementEnd
