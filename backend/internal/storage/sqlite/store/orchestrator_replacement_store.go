package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Raw SQL rather than sqlc, matching orchestrator_reap_store.go: this table is
// written and drained only by the ownership paths and the boot recovery pass.

// PutOrchestratorReplacementIntent records (or refreshes) a project's
// outstanding replacement. Upsert rather than insert: the project gate
// serializes replacement, so a second write for the same project is a RETRY of
// the same obligation, not a competing one, and must not fail.
//
// AttemptCount and last_error are deliberately preserved across an upsert. A
// project that keeps failing to get an orchestrator should look like one, not
// like a fresh request on every retry.
func (s *Store) PutOrchestratorReplacementIntent(ctx context.Context, intent domain.OrchestratorReplacementIntent) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.writeDB.ExecContext(ctx, `
INSERT INTO orchestrator_replacement_intent (project_id, retired_session_id, requested_at)
VALUES (?, ?, ?)
ON CONFLICT(project_id) DO UPDATE SET
  retired_session_id = excluded.retired_session_id,
  requested_at       = excluded.requested_at`,
		string(intent.ProjectID), string(intent.RetiredSessionID), intent.RequestedAt)
	if err != nil {
		return fmt.Errorf("put orchestrator replacement intent %s: %w", intent.ProjectID, err)
	}
	return nil
}

// ListOrchestratorReplacementIntents returns every outstanding replacement,
// oldest first.
//
// A missing table is NOT reported as an empty list, for the same reason the
// reap queue does not: "the schema is not what I expect" must not be read as
// "nothing is owed".
func (s *Store) ListOrchestratorReplacementIntents(ctx context.Context) ([]domain.OrchestratorReplacementIntent, error) {
	rows, err := s.readDB.QueryContext(ctx, `
SELECT project_id, retired_session_id, requested_at, attempt_count, last_attempt_at, last_error
FROM orchestrator_replacement_intent
ORDER BY requested_at ASC, project_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list orchestrator replacement intents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []domain.OrchestratorReplacementIntent
	for rows.Next() {
		var (
			in          domain.OrchestratorReplacementIntent
			project     string
			retired     string
			lastAttempt sql.NullTime
		)
		if err := rows.Scan(&project, &retired, &in.RequestedAt, &in.AttemptCount, &lastAttempt, &in.LastError); err != nil {
			return nil, fmt.Errorf("scan orchestrator replacement intent: %w", err)
		}
		in.ProjectID = domain.ProjectID(project)
		in.RetiredSessionID = domain.SessionID(retired)
		if lastAttempt.Valid {
			t := lastAttempt.Time
			in.LastAttemptAt = &t
		}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orchestrator replacement intents: %w", err)
	}
	return out, nil
}

// DeleteOrchestratorReplacementIntent discharges a project's obligation. Called
// only once a successor is actually live.
func (s *Store) DeleteOrchestratorReplacementIntent(ctx context.Context, project domain.ProjectID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx,
		`DELETE FROM orchestrator_replacement_intent WHERE project_id = ?`, string(project)); err != nil {
		return fmt.Errorf("delete orchestrator replacement intent %s: %w", project, err)
	}
	return nil
}

// RecordOrchestratorReplacementAttempt stamps a failed recovery so repeated
// inability to give a project an orchestrator is visible rather than silent.
func (s *Store) RecordOrchestratorReplacementAttempt(ctx context.Context, project domain.ProjectID, at time.Time, cause string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.writeDB.ExecContext(ctx, `
UPDATE orchestrator_replacement_intent
SET attempt_count = attempt_count + 1, last_attempt_at = ?, last_error = ?
WHERE project_id = ?`, at, cause, string(project)); err != nil {
		return fmt.Errorf("record orchestrator replacement attempt %s: %w", project, err)
	}
	return nil
}
