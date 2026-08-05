package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Raw SQL rather than sqlc: orchestrator_reap_queue is read and drained only by
// the boot reaper, and session_store.go already establishes raw ExecContext as
// the local escape hatch for statements sqlc's SQLite parser mishandles.

// ListOrchestratorReapQueue returns every outstanding reap obligation, oldest
// first.
//
// A missing table is NOT reported as an empty queue. The reaper's contract is
// fail-closed, and "the schema is not what I expect" must abort boot rather
// than be read as "nothing is owed" — the error is returned verbatim so the
// caller can surface it.
func (s *Store) ListOrchestratorReapQueue(ctx context.Context) ([]domain.OrchestratorReapEntry, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT session_id, project_id, runtime_handle_id, runtime_launch_id,
       workspace_path, queued_at, last_attempt_at, attempt_count
FROM orchestrator_reap_queue
ORDER BY queued_at ASC, session_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list orchestrator reap queue: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.OrchestratorReapEntry
	for rows.Next() {
		var (
			e           domain.OrchestratorReapEntry
			lastAttempt sql.NullTime
		)
		if err := rows.Scan(
			&e.SessionID, &e.ProjectID, &e.RuntimeHandleID, &e.RuntimeLaunchID,
			&e.WorkspacePath, &e.QueuedAt, &lastAttempt, &e.AttemptCount,
		); err != nil {
			return nil, fmt.Errorf("scan orchestrator reap queue: %w", err)
		}
		if lastAttempt.Valid {
			t := lastAttempt.Time
			e.LastAttemptAt = &t
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orchestrator reap queue: %w", err)
	}
	return out, nil
}

// DeleteOrchestratorReapEntry discharges one obligation. Callers must only
// reach this after death has been authoritatively confirmed.
func (s *Store) DeleteOrchestratorReapEntry(ctx context.Context, id domain.SessionID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM orchestrator_reap_queue WHERE session_id = ?`, id); err != nil {
		return fmt.Errorf("delete orchestrator reap entry %s: %w", id, err)
	}
	return nil
}

// RecordOrchestratorReapAttempt stamps a failed drain so repeated inability to
// confirm death is visible rather than silent. It deliberately does not
// schedule anything: the queue is drained once per boot, not on a timer.
func (s *Store) RecordOrchestratorReapAttempt(ctx context.Context, id domain.SessionID, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
UPDATE orchestrator_reap_queue
SET attempt_count = attempt_count + 1, last_attempt_at = ?
WHERE session_id = ?`, at, id); err != nil {
		return fmt.Errorf("record orchestrator reap attempt %s: %w", id, err)
	}
	return nil
}
