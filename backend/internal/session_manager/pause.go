package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Phase 3A durable pause (MASTER_PLAN §7).
//
// The shape of this file is dictated by rule 2, "durable pause; ZERO automatic
// send/restart". There is deliberately no scheduler, no retry-after timer and
// no auto-resume anywhere in the package: the ONLY thing that clears a pause is
// ResumeSession, called from an explicit request. Anything that resumed on its
// own would be an automatic restart regardless of how it were spelled.
//
// Enforcement does not live here. It lives in sessionguard, at the single
// pane-write choke point, so a new automatic caller is fenced by construction
// instead of by remembering to ask.

var (
	// ErrAlreadyPaused is returned when pausing a session that already carries a
	// pause pin for a DIFFERENT incident. Re-pausing the same incident is
	// idempotent and does not error.
	ErrAlreadyPaused = errors.New("session already paused")
	// ErrNotPaused is returned when resuming a session that is not paused.
	ErrNotPaused = errors.New("session is not paused")
)

// PauseRequest is the input to PauseSession. It carries no free-text reason
// field on purpose: a usage limit may only be recorded from a structured
// envelope (domain.SessionPause.Validate enforces the pairing), so there is no
// channel here through which an agent's prose could become a pause.
type PauseRequest struct {
	// IncidentID groups this pause with the resume that answers it. Empty means
	// "mint one".
	IncidentID string
	Reason     domain.PauseReason
	DetectedBy domain.PauseDetection
	// EvidenceJSON is the structured envelope, required for usage_limit.
	EvidenceJSON string
	// RetryAfter is recorded for audit only and never scheduled against.
	RetryAfter *time.Time
}

// PauseSession durably pins a session as paused and records a pause ledger
// event. It does NOT stop the runtime: the agent process and its pane stay
// exactly as they are, because the point of pause is to stop AO acting, not to
// destroy state the user may want to inspect or resume into.
//
// Idempotent for the same incident: re-pausing writes no second ledger row, so
// a detector that reports the same limit twice cannot inflate the audit trail.
func (m *Manager) PauseSession(ctx context.Context, id domain.SessionID, req PauseRequest) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: read session: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, ErrNotFound)
	}
	if rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, ErrTerminated)
	}

	incident := strings.TrimSpace(req.IncidentID)
	if existing := rec.Metadata.Pause; existing != nil {
		if incident == "" || existing.IncidentID == incident {
			// Same incident (or an unspecified one): already recorded.
			return rec, nil
		}
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w (incident %s)", id, ErrAlreadyPaused, existing.IncidentID)
	}
	if incident == "" {
		// Same generator the switch saga uses, so tests can pin incident ids the
		// same way they pin generations.
		incident = m.newSwitchGeneration()
	}

	pause := &domain.SessionPause{
		IncidentID:   incident,
		Reason:       req.Reason,
		DetectedBy:   req.DetectedBy,
		Harness:      rec.Harness,
		EvidenceJSON: req.EvidenceJSON,
		RetryAfter:   req.RetryAfter,
		PausedAt:     m.clock(),
	}
	if err := pause.Validate(); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, err)
	}

	// Ledger BEFORE the pin, so a crash between the two leaves an event with no
	// pin — an over-reported pause, which is inert — rather than a pin with no
	// audit trail, which is a session stuck for a reason nothing recorded.
	if err := m.appendPauseLedger(ctx, rec, domain.LifecycleKindPause, incident, pause.EvidenceJSON); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, err)
	}

	rec.Metadata.Pause = pause
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: persist: %w", id, err)
	}
	m.logger.Info("session paused", "sessionID", id, "reason", string(pause.Reason),
		"detectedBy", string(pause.DetectedBy), "incident", incident, "harness", string(rec.Harness))
	return rec, nil
}

// ResumeSession clears the pause pin and records a resume ledger event. It is
// the ONLY way a pause is lifted.
//
// It deliberately does not send anything. Resuming restores AO's permission to
// write, not an obligation to: re-sending on resume would reintroduce the
// automatic send that pause exists to prevent, and MASTER_PLAN §7 puts manual
// continue in 3B, on an explicit ladder rung.
func (m *Manager) ResumeSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: read session: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w", id, ErrNotFound)
	}
	paused := rec.Metadata.Pause
	if paused == nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w", id, ErrNotPaused)
	}

	// Ledger first, matching PauseSession: a crash between the two leaves the
	// session paused with a resume event recorded, which a retry makes right.
	// The other order would clear the pin with nothing saying who lifted it.
	if err := m.appendPauseLedger(ctx, rec, domain.LifecycleKindResume, paused.IncidentID, ""); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w", id, err)
	}

	rec.Metadata.Pause = nil
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: persist: %w", id, err)
	}
	m.logger.Info("session resumed", "sessionID", id, "incident", paused.IncidentID)
	return rec, nil
}

// appendPauseLedger writes one pause/resume row, keyed so a retry cannot
// duplicate it. Pause events carry no saga phase — they are points, not
// sagas — so the id is session:incident:kind rather than the switch saga's
// session:generation:phase.
func (m *Manager) appendPauseLedger(
	ctx context.Context,
	rec domain.SessionRecord,
	kind domain.LifecycleLedgerKind,
	incident string,
	payload string,
) error {
	id := fmt.Sprintf("%s:%s:%s", rec.ID, incident, kind)
	if events, err := m.store.ListLifecycleLedger(ctx, rec.ID); err == nil {
		for _, e := range events {
			if e.ID == id {
				return nil
			}
		}
	}
	err := m.store.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
		ID: id, SessionID: rec.ID, ProjectID: rec.ProjectID,
		Kind: kind, GenerationID: incident,
		FromHarness: rec.Harness, ToHarness: rec.Harness,
		RoleID:      rec.Metadata.Role.RoleID,
		PayloadJSON: payload, CreatedAt: m.clock(),
	})
	if err != nil {
		return fmt.Errorf("lifecycle ledger %s: %w", kind, err)
	}
	return nil
}
