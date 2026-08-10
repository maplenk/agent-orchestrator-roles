package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ---- sessions ----

// CreateSession assigns the per-project identity ("{project}-{num}") and inserts
// the record, returning it with ID populated. The next-num read and the insert
// run on the writer connection under writeMu, so two concurrent creates in the
// same project can't collide on num.
func (s *Store) CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	num, err := s.qw.NextSessionNum(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("next session num for %s: %w", rec.ProjectID, err)
	}
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, num))
	params, err := recordToInsert(rec, num)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("insert session %s: %w", rec.ID, err)
	}
	if err := s.qw.InsertSession(ctx, params); err != nil {
		if isActiveOrchestratorConflict(err) {
			return domain.SessionRecord{}, fmt.Errorf("insert session for project %s: %w",
				rec.ProjectID, domain.ErrActiveOrchestratorExists)
		}
		return domain.SessionRecord{}, fmt.Errorf("insert session %s: %w", rec.ID, err)
	}
	return rec, nil
}

// UpdateSession writes the full mutable state of an existing session. The
// id/project/num/created_at are immutable and not touched here.
//
// Clearing is_terminated on an orchestrator re-enters migration 0057's partial
// unique index, so a restore/resume that races another active orchestrator
// fails here. That is surfaced as domain.ErrActiveOrchestratorExists rather
// than a raw driver error: the caller has usually just created a runtime and
// must know to reap it (see session_manager.relaunchSession).
func (s *Store) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdateSession(ctx, recordToUpdate(rec)); err != nil {
		if isActiveOrchestratorConflict(err) {
			return fmt.Errorf("update session %s: %w", rec.ID, domain.ErrActiveOrchestratorExists)
		}
		return err
	}
	return nil
}

// isActiveOrchestratorConflict reports whether err is migration 0057's
// one-active-orchestrator index rejecting a write.
//
// SQLite names the COLUMNS, not the index — "UNIQUE constraint failed:
// sessions.project_id (2067)" — so the index name is not available to match on.
// The match must be on the EXACT column list: sessions has carried
// UNIQUE(project_id, num) since migration 0001, and that distinct collision
// reports "sessions.project_id, sessions.num", which a substring test would
// misread as an orchestrator conflict.
func isActiveOrchestratorConflict(err error) bool {
	return uniqueConstraintColumns(err) == "sessions.project_id"
}

// uniqueConstraintColumns returns the exact column list SQLite reported for a
// UNIQUE violation, or "" when err is not one (or is not parseable). Returning
// "" for an unrecognized shape fails toward NOT classifying, which surfaces an
// unmapped error rather than a confidently wrong one.
func uniqueConstraintColumns(err error) string {
	if !isSQLiteUnique(err) {
		return ""
	}
	const marker = "UNIQUE constraint failed: "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	cols := msg[i+len(marker):]
	// The driver appends the result code as " (2067)". A column list cannot
	// contain " (", so trimming from the last one is unambiguous.
	if j := strings.LastIndex(cols, " ("); j >= 0 {
		cols = cols[:j]
	}
	return strings.TrimSpace(cols)
}

// UpdateSessionFromActivitySignal projects activity-derived session metadata
// only when the signal still belongs to the session's active harness launch.
func (s *Store) UpdateSessionFromActivitySignal(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	resultState, resultSummary, resultAt, resultGeneration, resultCurrent := roleResultColumns(rec.RoleResult)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateSessionFromActivitySignal(ctx, gen.UpdateSessionFromActivitySignalParams{
		ActivityState:                activity.State,
		ActivityLastAt:               activity.LastActivityAt,
		FirstSignalAt:                timeToNullTime(rec.FirstSignalAt),
		AgentSessionID:               rec.Metadata.AgentSessionID,
		LatestUserPrompt:             rec.Metadata.LatestUserPrompt,
		LatestAssistantUpdate:        rec.Metadata.LatestAssistantUpdate,
		RoleResultState:              resultState,
		RoleResultSummary:            resultSummary,
		RoleResultReportedAt:         resultAt,
		RoleResultGenerationID:       resultGeneration,
		RoleResultCurrent:            resultCurrent,
		NativeTranscriptPath:         rec.Metadata.NativeTranscriptPath,
		UpdatedAt:                    rec.UpdatedAt,
		ID:                           rec.ID,
		ExpectedHarness:              rec.Harness,
		ExpectedSessionMode:          domain.NormalizeSessionMode(rec.Mode),
		ExpectedRuntimeLaunchID:      rec.Metadata.RuntimeLaunchID,
		ExpectedControllerGeneration: rec.Metadata.ControllerGeneration,
	})
	if err != nil {
		return false, fmt.Errorf("update session %s from activity signal: %w", rec.ID, err)
	}
	return rows > 0, nil
}

// RecordSessionLatestUserPrompt persists the latest real user direction and
// supersedes any prior semantic result after delivery. It does not rewrite
// lifecycle ownership/state that another goroutine may have advanced.
func (s *Store) RecordSessionLatestUserPrompt(ctx context.Context, id domain.SessionID, prompt string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RecordSessionLatestUserPrompt(ctx, gen.RecordSessionLatestUserPromptParams{
		LatestUserPrompt: prompt,
		UpdatedAt:        updatedAt,
		ID:               id,
	})
	if err != nil {
		return false, fmt.Errorf("record latest user prompt for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// ClaimChatControllerGeneration makes generation the only Chat controller that
// may project provider events for this session. The narrow update avoids writing
// a stale full SessionRecord over lifecycle facts changed by another goroutine.
func (s *Store) ClaimChatControllerGeneration(
	ctx context.Context,
	id domain.SessionID,
	generation string,
	updatedAt time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ClaimChatControllerGeneration(ctx, gen.ClaimChatControllerGenerationParams{
		ControllerGeneration: generation,
		UpdatedAt:            updatedAt,
		ID:                   id,
	})
	if err != nil {
		return fmt.Errorf("claim chat controller generation for %s: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("claim chat controller generation for %s: chat session not found", id)
	}
	return nil
}

// RenameSession updates only the user-facing display name for an existing
// session. It returns ok=false when the session id does not exist. The
// sessions_cdc_update trigger fans out a session_updated CDC event when the
// display name actually changes.
func (s *Store) RenameSession(ctx context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RenameSession(ctx, gen.RenameSessionParams{
		ID:          id,
		DisplayName: displayName,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("rename session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionPinned updates the pinned status of a session.
func (s *Store) SetSessionPinned(ctx context.Context, id domain.SessionID, isPinned bool, pinnedAt *time.Time, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionPinned(ctx, gen.SetSessionPinnedParams{
		ID:        id,
		IsPinned:  isPinned,
		PinnedAt:  timePtrToNullTime(pinnedAt),
		UpdatedAt: updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set session pinned %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionPreviewURL updates only the browser preview URL for an existing
// session. It returns ok=false when the session id does not exist. The
// sessions_cdc_update trigger fans out a session_updated CDC event when the
// preview URL actually changes.
func (s *Store) SetSessionPreviewURL(ctx context.Context, id domain.SessionID, previewURL string, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionPreviewURL(ctx, gen.SetSessionPreviewURLParams{
		ID:         id,
		PreviewURL: previewURL,
		UpdatedAt:  updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set preview url for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionPauseIfAbsent writes the durable pause pin, and ONLY that column.
//
// Conditional on the pin still being absent and the session still live, so the
// write is a compare-and-set rather than a read-modify-write. ok=false means
// the precondition failed — already paused, terminated, or gone — and the
// caller must re-read to say which; it is not an error, because a detector
// re-reporting the same limit is the normal case.
//
// This exists because the generic UpdateSession carries a whole session record
// that its caller read at some earlier moment. A lifecycle or switch writer
// holding a pre-pause snapshot would clear a pin it never saw, silently
// re-opening every automatic write path.
func (s *Store) SetSessionPauseIfAbsent(ctx context.Context, id domain.SessionID, pause *domain.SessionPause, guard domain.PauseGuard, updatedAt time.Time) (bool, error) {
	encoded, err := encodePause(pause)
	if err != nil {
		return false, err
	}
	if encoded == "" {
		return false, fmt.Errorf("set pause for %s: refusing to write an empty pin", id)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Raw, not sqlc: the literals in this WHERE clause trigger the parser bug
	// documented in queries/sessions.sql, which silently truncates the
	// statement (and leaks its tail into the next generated const).
	// Every condition is in the STATEMENT, not read-then-checked in Go: the
	// point is that ownership is verified atomically with the write, so a
	// switch or relaunch landing between a check and the write cannot slip
	// through.
	q := `UPDATE sessions SET pause_json = ?, updated_at = ?
		 WHERE id = ? AND pause_json = '' AND is_terminated = 0`
	args := []any{encoded, updatedAt, id}
	if h := strings.TrimSpace(string(guard.ExpectHarness)); h != "" {
		q += ` AND harness = ?`
		args = append(args, h)
	}
	if g := strings.TrimSpace(guard.ExpectRuntimeLaunchID); g != "" {
		q += ` AND runtime_launch_id = ?`
		args = append(args, g)
	}
	if guard.RequireNoSwitchPending {
		q += ` AND switch_pending_json = ''`
	}
	res, err := s.writeDB.ExecContext(ctx, q, args...)
	if err != nil {
		return false, fmt.Errorf("set pause for session %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set pause for session %s: rows: %w", id, err)
	}
	return rows > 0, nil
}

// ClearSessionPauseIfIncident lifts the pause pin, and ONLY that column, when
// the stored pin still names the incident the caller is answering. ok=false
// means it did not: the session was resumed already, or a different incident
// now holds it, and lifting that one would resume a session on evidence the
// caller never saw.
func (s *Store) ClearSessionPauseIfIncident(ctx context.Context, id domain.SessionID, incidentID string, updatedAt time.Time) (bool, error) {
	if strings.TrimSpace(incidentID) == "" {
		return false, fmt.Errorf("clear pause for %s: incident required", id)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Raw, not sqlc: see SetSessionPauseIfAbsent.
	res, err := s.writeDB.ExecContext(ctx,
		// pause_json <> '' comes FIRST: json_extract raises "malformed JSON" on
		// an empty string, so clearing an unpaused session would error instead
		// of answering "nothing to clear". SQLite short-circuits AND.
		`UPDATE sessions SET pause_json = '', updated_at = ?
		 WHERE id = ? AND pause_json <> '' AND json_extract(pause_json, '$.incidentId') = ?`,
		updatedAt, id, incidentID)
	if err != nil {
		return false, fmt.Errorf("clear pause for session %s: %w", id, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("clear pause for session %s: rows: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionTerminateOnPRMerge updates the user's merge-completion lifecycle
// policy. It returns ok=false when the session id does not exist.
func (s *Store) SetSessionTerminateOnPRMerge(ctx context.Context, id domain.SessionID, terminate bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionTerminateOnPRMerge(ctx, gen.SetSessionTerminateOnPRMergeParams{
		ID:                 id,
		TerminateOnPRMerge: terminate,
		UpdatedAt:          updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set terminate-on-pr-merge for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionAutoInjectReview persists a session's automatic review-injection policy.
func (s *Store) SetSessionAutoInjectReview(ctx context.Context, id domain.SessionID, autoInject bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAutoInjectReview(ctx, gen.SetSessionAutoInjectReviewParams{
		ID:               id,
		AutoInjectReview: autoInject,
		UpdatedAt:        updatedAt,
	})
	if err != nil {
		return false, fmt.Errorf("set auto-inject review for session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionReviewerHarness persists the reviewer preference for one session.
func (s *Store) SetSessionReviewerHarness(ctx context.Context, id domain.SessionID, harness domain.ReviewerHarness, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionReviewerHarness(ctx, gen.SetSessionReviewerHarnessParams{
		ReviewerHarness: harness,
		UpdatedAt:       updatedAt,
		ID:              id,
	})
	if err != nil {
		return false, fmt.Errorf("set reviewer harness for %s: %w", id, err)
	}
	return rows > 0, nil
}

// DeleteSession removes a session row, but only if it is still in seed state
// (no workspace, no runtime handle, no agent session id, no prompt, no handoff
// metadata, and not already terminated). Rows that have observable spawn output are immutable
// to preserve the no-resurrection guarantee — for those, callers fall back to
// MarkTerminated (lifecycle.Manager) instead.
//
// The deletion runs in a transaction. It first probes seed state with
// SessionIsSeed; only if that returns true does it clear the session's
// change_log rows (required because change_log FKs sessions(id) without
// ON DELETE CASCADE) and then delete the session row. For live or absent
// sessions the transaction commits with no rows touched — critically, the
// session_created / session_updated CDC events for live sessions are NOT
// destroyed when callers (e.g. RollbackSpawn's delete-then-kill fallback)
// invoke DeleteSession on a fully-spawned row.
//
// Returns deleted=true when a seed row was removed; deleted=false when the
// session id did not match a seed row (either it never existed, or it had
// already progressed past seed state). The latter case is benign — the caller
// should fall back to MarkTerminated.
func (s *Store) DeleteSession(ctx context.Context, id domain.SessionID) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete seed session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)

	isSeed, err := q.SessionIsSeed(ctx, id)
	if err != nil {
		return false, fmt.Errorf("delete seed session: probe seed state for %s: %w", id, err)
	}
	if !isSeed {
		// Commit the empty tx so we don't leak a transaction. Critically, do
		// NOT touch change_log here — for a live session that contains real
		// session_created / session_updated CDC events.
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("delete seed session: commit no-op: %w", err)
		}
		return false, nil
	}

	// Drop change_log rows for this session id first so the FK doesn't reject
	// the session DELETE. We do not touch project-level events (session_id IS
	// NULL) — those belong to the project, not this session. Both this DELETE
	// and the session DELETE below run via raw ExecContext to sidestep sqlc
	// 1.31's SQLite-parser bug, which strips trailing `?` placeholders and
	// string literals from DELETE statements (see queries/changelog.sql and
	// queries/sessions.sql for the documented workaround context).
	if _, err := tx.ExecContext(ctx, `DELETE FROM change_log WHERE session_id = ?`, id); err != nil {
		return false, fmt.Errorf("delete seed session: clear change log for %s: %w", id, err)
	}
	res, err := tx.ExecContext(ctx, `
DELETE FROM sessions
WHERE id = ?
  AND is_terminated = 0
  AND workspace_path = ''
  AND runtime_handle_id = ''
  AND agent_session_id = ''
  AND prompt = ''
  AND latest_user_prompt = ''
  AND latest_assistant_update = ''
  AND native_transcript_path = ''`, id)
	if err != nil {
		return false, fmt.Errorf("delete seed session %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete seed session %s: rows affected: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("delete seed session: commit: %w", err)
	}
	return n > 0, nil
}

// GetSession returns the full record for a session, or ok=false if absent.
func (s *Store) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	row, err := s.qr.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session %s: %w", id, err)
	}
	rec, err := rowToRecord(row.Session)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session %s: %w", id, err)
	}
	return rec, true, nil
}

// GetSessionByRuntimeHandleID finds a session whose live runtime_handle_id matches
// the terminal mux key (tmux session name). ok=false when no row matches.
func (s *Store) GetSessionByRuntimeHandleID(ctx context.Context, handleID string) (domain.SessionRecord, bool, error) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return domain.SessionRecord{}, false, nil
	}
	row, err := s.qr.GetSessionByRuntimeHandleID(ctx, handleID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session by runtime handle %q: %w", handleID, err)
	}
	rec, err := rowToRecord(row.Session)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session by runtime handle %q: %w", handleID, err)
	}
	return rec, true, nil
}

// GetSessionByPendingSourceHandle finds a session whose SwitchPending still
// records the pre-stop runtime handle (handle cleared after source destroy).
func (s *Store) GetSessionByPendingSourceHandle(ctx context.Context, handleID string) (domain.SessionRecord, bool, error) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return domain.SessionRecord{}, false, nil
	}
	row, err := s.qr.GetSessionByPendingSourceHandle(ctx, handleID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session by pending source handle %q: %w", handleID, err)
	}
	rec, err := rowToRecord(row.Session)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session by pending source handle %q: %w", handleID, err)
	}
	return rec, true, nil
}

// ListSessions returns every session in a project, ordered by num.
func (s *Store) ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListSessionsByProject(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := rowToRecord(r.Session)
		if err != nil {
			return nil, fmt.Errorf("list sessions for %s: %w", project, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// ListAllSessions returns every session across all projects.
func (s *Store) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		rec, err := rowToRecord(r.Session)
		if err != nil {
			return nil, fmt.Errorf("list all sessions: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

func rowToRecord(row gen.Session) (domain.SessionRecord, error) {
	// Hydrate Role whenever any role-specific column is populated — not only when
	// role_id is set. Dropping partial pins (e.g. template_artifact_id without
	// role_id) would make restore treat corrupt rows as legacy.
	role := roleFromSessionRow(row)
	pending, err := decodeSwitchPending(row.SwitchPendingJson)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	pause, err := decodePause(row.PauseJson)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	roleResult, err := roleResultFromSessionRow(row, role)
	if err != nil {
		return domain.SessionRecord{}, err
	}

	return domain.SessionRecord{
		ID:              row.ID,
		ProjectID:       row.ProjectID,
		IssueID:         row.IssueID,
		Kind:            row.Kind,
		Harness:         row.Harness,
		ReviewerHarness: row.ReviewerHarness,
		DisplayName:     row.DisplayName,
		Mode:            domain.NormalizeSessionMode(row.SessionMode),
		Activity: domain.Activity{
			State:          row.ActivityState,
			LastActivityAt: row.ActivityLastAt,
		},
		FirstSignalAt:      nullTimeToTime(row.FirstSignalAt),
		IsTerminated:       row.IsTerminated,
		IsPinned:           row.IsPinned,
		PinnedAt:           nullTimeToTimePtr(row.PinnedAt),
		TerminateOnPRMerge: row.TerminateOnPRMerge,
		AutoInjectReview:   row.AutoInjectReview,
		RoleResult:         roleResult,
		Metadata: domain.SessionMetadata{
			Branch:                    row.Branch,
			WorkspacePath:             row.WorkspacePath,
			WorkspaceRepoPath:         row.WorkspaceRepoPath,
			DiffBaseSHA:               row.DiffBaseSha,
			DiffBaseRef:               row.DiffBaseRef,
			RuntimeHandleID:           row.RuntimeHandleID,
			RuntimeLaunchID:           row.RuntimeLaunchID,
			AgentSessionID:            row.AgentSessionID,
			Prompt:                    row.Prompt,
			LatestUserPrompt:          row.LatestUserPrompt,
			LatestAssistantUpdate:     row.LatestAssistantUpdate,
			NativeTranscriptPath:      row.NativeTranscriptPath,
			PreviewURL:                row.PreviewURL,
			PreviewRevision:           row.PreviewRevision,
			Role:                      role,
			SwitchPending:             pending,
			Pause:                     pause,
			SpawnCapabilityHash:       row.SpawnCapabilityHash,
			BrowserCapabilityVerifier: row.BrowserCapabilityVerifier,
			ProviderConversationID:    row.ProviderConversationID,
			ControllerGeneration:      row.ControllerGeneration,
		},
		CleanupGeneration: row.CleanupGeneration,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}, nil
}

func encodeSwitchPending(p *domain.SwitchPending) string {
	if p == nil || strings.TrimSpace(p.GenerationID) == "" {
		return ""
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// encodePause serialises the durable pause pin. Unlike encodeSwitchPending it
// cannot swallow a marshal failure: dropping the pin silently would un-pause a
// session, re-opening every automatic write path this pin exists to close. A
// pause that cannot be encoded is a programming error, so it is reported.
func encodePause(p *domain.SessionPause) (string, error) {
	if p == nil {
		return "", nil
	}
	if err := p.Validate(); err != nil {
		return "", fmt.Errorf("encode pause: %w", err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("encode pause: %w", err)
	}
	return string(b), nil
}

// decodePause returns (nil, nil) for empty, a pause pointer for valid JSON, or
// an error for malformed durable state. Corrupt JSON must NOT read as "not
// paused": that is the one failure mode that turns unreadable state back into
// automatic sends. Callers fail closed on the error instead.
func decodePause(raw string) (*domain.SessionPause, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}
	var p domain.SessionPause
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("corrupt pause_json: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("corrupt pause_json: %w", err)
	}
	return &p, nil
}

// decodeSwitchPending returns (nil, nil) for empty, a pending pointer for valid
// JSON, or an error for malformed non-empty durable state. Callers must fail
// closed on error so corrupt ownership state cannot open input paths.
func decodeSwitchPending(raw string) (*domain.SwitchPending, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil, nil
	}
	var p domain.SwitchPending
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("corrupt switch_pending_json: %w", err)
	}
	if strings.TrimSpace(p.GenerationID) == "" {
		return nil, fmt.Errorf("corrupt switch_pending_json: missing generationId")
	}
	return &p, nil
}

func recordToInsert(rec domain.SessionRecord, num int64) (gen.InsertSessionParams, error) {
	pause, err := encodePause(rec.Metadata.Pause)
	if err != nil {
		return gen.InsertSessionParams{}, err
	}
	activity := normalActivity(rec.Activity, rec.CreatedAt)
	role := rec.Metadata.Role
	writes, spawn := roleWriteFlags(role.ResolvedPermissions)
	resultState, resultSummary, resultAt, resultGeneration, resultCurrent := roleResultColumns(rec.RoleResult)
	return gen.InsertSessionParams{
		ID:                        rec.ID,
		ProjectID:                 rec.ProjectID,
		Num:                       num,
		IssueID:                   rec.IssueID,
		Kind:                      rec.Kind,
		Harness:                   rec.Harness,
		ReviewerHarness:           rec.ReviewerHarness,
		RoleID:                    role.RoleID,
		RoleMapSchemaVersion:      int64(role.RoleMapSchemaVersion),
		RoleMapSha256:             role.RoleMapSHA256,
		RoleConfigRevision:        role.RoleConfigRevision,
		TemplateArtifactID:        role.TemplateArtifactID,
		TemplateSha256:            role.TemplateSHA256,
		ResolvedModel:             role.ResolvedModel,
		ResolvedWorkspaceWrites:   writes,
		ResolvedCanSpawn:          spawn,
		SpawnCapabilityHash:       rec.Metadata.SpawnCapabilityHash,
		DisplayName:               rec.DisplayName,
		ActivityState:             activity.State,
		ActivityLastAt:            activity.LastActivityAt,
		FirstSignalAt:             timeToNullTime(rec.FirstSignalAt),
		IsTerminated:              rec.IsTerminated,
		IsPinned:                  rec.IsPinned,
		PinnedAt:                  timePtrToNullTime(rec.PinnedAt),
		Branch:                    rec.Metadata.Branch,
		WorkspacePath:             rec.Metadata.WorkspacePath,
		WorkspaceRepoPath:         rec.Metadata.WorkspaceRepoPath,
		DiffBaseSha:               rec.Metadata.DiffBaseSHA,
		DiffBaseRef:               rec.Metadata.DiffBaseRef,
		RuntimeHandleID:           rec.Metadata.RuntimeHandleID,
		RuntimeLaunchID:           rec.Metadata.RuntimeLaunchID,
		AgentSessionID:            rec.Metadata.AgentSessionID,
		Prompt:                    rec.Metadata.Prompt,
		SwitchPendingJson:         encodeSwitchPending(rec.Metadata.SwitchPending),
		PauseJson:                 pause,
		LatestUserPrompt:          rec.Metadata.LatestUserPrompt,
		LatestAssistantUpdate:     rec.Metadata.LatestAssistantUpdate,
		NativeTranscriptPath:      rec.Metadata.NativeTranscriptPath,
		PreviewURL:                rec.Metadata.PreviewURL,
		PreviewRevision:           rec.Metadata.PreviewRevision,
		TerminateOnPRMerge:        rec.TerminateOnPRMerge,
		AutoInjectReview:          rec.AutoInjectReview,
		CleanupGeneration:         rec.CleanupGeneration,
		BrowserCapabilityVerifier: rec.Metadata.BrowserCapabilityVerifier,
		CreatedAt:                 rec.CreatedAt,
		UpdatedAt:                 rec.UpdatedAt,
		SessionMode:               domain.NormalizeSessionMode(rec.Mode),
		ProviderConversationID:    rec.Metadata.ProviderConversationID,
		ControllerGeneration:      rec.Metadata.ControllerGeneration,
		RoleResultState:           resultState,
		RoleResultSummary:         resultSummary,
		RoleResultReportedAt:      resultAt,
		RoleResultGenerationID:    resultGeneration,
		RoleResultCurrent:         resultCurrent,
	}, nil
}

// recordToUpdate deliberately omits pause_json. The pause pin is column-owned
// (SetPauseIfAbsent / ClearPauseIfIncident) because callers of the generic
// update hold a whole session record read at some earlier point: a lifecycle or
// switch writer whose snapshot predates a pause would otherwise clear a pin it
// never saw, and a stale resume would overwrite newer runtime state.
func recordToUpdate(rec domain.SessionRecord) gen.UpdateSessionParams {
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	role := rec.Metadata.Role
	writes, spawn := roleWriteFlags(role.ResolvedPermissions)
	resultState, resultSummary, resultAt, resultGeneration, resultCurrent := roleResultColumns(rec.RoleResult)
	return gen.UpdateSessionParams{
		ID:                        rec.ID,
		IssueID:                   rec.IssueID,
		Kind:                      rec.Kind,
		Harness:                   rec.Harness,
		ReviewerHarness:           rec.ReviewerHarness,
		RoleID:                    role.RoleID,
		RoleMapSchemaVersion:      int64(role.RoleMapSchemaVersion),
		RoleMapSha256:             role.RoleMapSHA256,
		RoleConfigRevision:        role.RoleConfigRevision,
		TemplateArtifactID:        role.TemplateArtifactID,
		TemplateSha256:            role.TemplateSHA256,
		ResolvedModel:             role.ResolvedModel,
		ResolvedWorkspaceWrites:   writes,
		ResolvedCanSpawn:          spawn,
		SpawnCapabilityHash:       rec.Metadata.SpawnCapabilityHash,
		DisplayName:               rec.DisplayName,
		ActivityState:             activity.State,
		ActivityLastAt:            activity.LastActivityAt,
		FirstSignalAt:             timeToNullTime(rec.FirstSignalAt),
		IsTerminated:              rec.IsTerminated,
		IsPinned:                  rec.IsPinned,
		PinnedAt:                  timePtrToNullTime(rec.PinnedAt),
		Branch:                    rec.Metadata.Branch,
		WorkspacePath:             rec.Metadata.WorkspacePath,
		WorkspaceRepoPath:         rec.Metadata.WorkspaceRepoPath,
		DiffBaseSha:               rec.Metadata.DiffBaseSHA,
		DiffBaseRef:               rec.Metadata.DiffBaseRef,
		RuntimeHandleID:           rec.Metadata.RuntimeHandleID,
		RuntimeLaunchID:           rec.Metadata.RuntimeLaunchID,
		AgentSessionID:            rec.Metadata.AgentSessionID,
		Prompt:                    rec.Metadata.Prompt,
		SwitchPendingJson:         encodeSwitchPending(rec.Metadata.SwitchPending),
		LatestUserPrompt:          rec.Metadata.LatestUserPrompt,
		LatestAssistantUpdate:     rec.Metadata.LatestAssistantUpdate,
		NativeTranscriptPath:      rec.Metadata.NativeTranscriptPath,
		PreviewURL:                rec.Metadata.PreviewURL,
		PreviewRevision:           rec.Metadata.PreviewRevision,
		TerminateOnPRMerge:        rec.TerminateOnPRMerge,
		AutoInjectReview:          rec.AutoInjectReview,
		CleanupGeneration:         rec.CleanupGeneration,
		BrowserCapabilityVerifier: rec.Metadata.BrowserCapabilityVerifier,
		UpdatedAt:                 rec.UpdatedAt,
		ProviderConversationID:    rec.Metadata.ProviderConversationID,
		ControllerGeneration:      rec.Metadata.ControllerGeneration,
		RoleResultState:           resultState,
		RoleResultSummary:         resultSummary,
		RoleResultReportedAt:      resultAt,
		RoleResultGenerationID:    resultGeneration,
		RoleResultCurrent:         resultCurrent,
	}
}

func roleResultColumns(result *domain.SessionRoleResult) (string, string, sql.NullTime, string, int64) {
	if result == nil {
		return "", "", sql.NullTime{}, "", 0
	}
	current := int64(0)
	if result.Current {
		current = 1
	}
	return string(result.State), result.Summary, timeToNullTime(result.ReportedAt), result.GenerationID, current
}

func roleResultFromSessionRow(row gen.Session, role domain.SessionRoleBinding) (*domain.SessionRoleResult, error) {
	if row.RoleResultState == "" && row.RoleResultSummary == "" && !row.RoleResultReportedAt.Valid &&
		row.RoleResultGenerationID == "" && row.RoleResultCurrent == 0 {
		return nil, nil
	}
	report, err := domain.NormalizeRoleResultReport(domain.RoleResultReport{
		SchemaVersion: domain.RoleResultSchemaVersion,
		State:         domain.RoleResultState(row.RoleResultState),
		Summary:       row.RoleResultSummary,
	})
	if err != nil || !row.RoleResultReportedAt.Valid || strings.TrimSpace(row.RoleResultGenerationID) == "" || strings.TrimSpace(role.RoleID) == "" {
		return nil, fmt.Errorf("corrupt role result for session %s", row.ID)
	}
	activeGeneration := row.RuntimeLaunchID
	if domain.NormalizeSessionMode(row.SessionMode) == domain.SessionModeChat {
		activeGeneration = row.ControllerGeneration
	}
	return &domain.SessionRoleResult{
		RoleID:       role.RoleID,
		State:        report.State,
		Summary:      report.Summary,
		ReportedAt:   row.RoleResultReportedAt.Time,
		GenerationID: row.RoleResultGenerationID,
		Current:      row.RoleResultCurrent != 0 && activeGeneration != "" && row.RoleResultGenerationID == activeGeneration,
	}, nil
}

// roleFromSessionRow maps sessions role columns to domain.SessionRoleBinding.
// Returns zero only when every role-specific column is empty/default. sessions.harness
// alone does not count — it is shared with non-role sessions.
func roleFromSessionRow(row gen.Session) domain.SessionRoleBinding {
	if !sessionRowHasRoleColumns(row) {
		return domain.SessionRoleBinding{}
	}
	role := domain.SessionRoleBinding{
		RoleID:               row.RoleID,
		RoleMapSchemaVersion: int(row.RoleMapSchemaVersion),
		RoleMapSHA256:        row.RoleMapSha256,
		RoleConfigRevision:   row.RoleConfigRevision,
		TemplateArtifactID:   row.TemplateArtifactID,
		TemplateSHA256:       row.TemplateSha256,
		ResolvedModel:        row.ResolvedModel,
		ResolvedPermissions: domain.RoleExecutionPolicy{
			WorkspaceWrites: row.ResolvedWorkspaceWrites != 0,
			CanSpawn:        row.ResolvedCanSpawn != 0,
		},
	}
	// Effective harness is sessions.harness; surface it on the pin when any role
	// column is present so incomplete pins remain fully visible to restore.
	if row.Harness != "" {
		role.ResolvedHarness = row.Harness
	}
	return role
}

func sessionRowHasRoleColumns(row gen.Session) bool {
	return row.RoleID != "" ||
		row.TemplateArtifactID != "" ||
		row.TemplateSha256 != "" ||
		row.RoleMapSha256 != "" ||
		row.ResolvedModel != "" ||
		row.RoleMapSchemaVersion != 0 ||
		row.RoleConfigRevision != 0 ||
		row.ResolvedWorkspaceWrites != 0 ||
		row.ResolvedCanSpawn != 0
}

func roleWriteFlags(p domain.RoleExecutionPolicy) (writes, spawn int64) {
	if p.WorkspaceWrites {
		writes = 1
	}
	if p.CanSpawn {
		spawn = 1
	}
	return
}

// nullTimeToTime / timeToNullTime bridge the nullable first_signal_at column
// to the domain's zero-time convention (zero = no signal received yet).
func nullTimeToTime(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func timeToNullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func nullTimeToTimePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func timePtrToNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func normalActivity(a domain.Activity, fallback time.Time) domain.Activity {
	if a.State == "" {
		a.State = domain.ActivityIdle
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = fallback
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = time.Now().UTC()
	}
	return a
}
