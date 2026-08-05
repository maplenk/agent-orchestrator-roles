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

-- Restore markers first, while the losers are still identifiable as active.
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
-- prior workspace claims are gone), so Down only removes the constraint.
DROP INDEX IF EXISTS idx_sessions_one_active_orchestrator;
-- +goose StatementEnd
