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
// Two enforcement points, neither of them here:
//   - sessionguard fences AO-initiated pane writes at the single write choke
//     point, so a new automatic caller is fenced by construction; and
//   - Reconcile / RestoreAll skip paused sessions, because "no automatic send"
//     is worth nothing if boot relaunches the agent instead.
//
// Persistence is COLUMN-OWNED (SetSessionPauseIfAbsent /
// ClearSessionPauseIfIncident), never a read-modify-write through the generic
// UpdateSession. Every other writer in the manager carries a whole session
// record read at some earlier point; one of those writing back a pre-pause
// snapshot would clear the pin and silently re-open everything above.

var (
	// ErrAlreadyPaused is returned when pausing a session that already carries a
	// pause pin for a DIFFERENT incident. Re-pausing the same incident is
	// idempotent and does not error.
	ErrAlreadyPaused = errors.New("session already paused")
	// ErrNotPaused is returned when resuming a session that is not paused.
	ErrNotPaused = errors.New("session is not paused")
	// ErrIncidentRequired is returned when a pause carries no incident id.
	ErrIncidentRequired = errors.New("pause incident id required")
	// ErrIncidentMismatch is returned when a resume names an incident that is
	// not the one currently holding the session.
	ErrIncidentMismatch = errors.New("pause incident mismatch")
)

// PauseRequest is the input to PauseSession. It carries no free-text reason
// field on purpose: a usage limit may only be recorded from a structured
// envelope (domain.SessionPause.Validate parses one), so there is no channel
// here through which an agent's prose could become a pause.
type PauseRequest struct {
	// IncidentID is REQUIRED and must be stable for the incident being
	// reported. The manager deliberately does not mint one.
	//
	// A minted id is not retry-safe: the ledger row is written before the pin,
	// so if the pin write fails the caller never learns the id that was
	// recorded, and a retry with a blank id would open a second incident and a
	// second ledger row for one real limit. With a caller-supplied stable id a
	// retry re-derives the same value, the ledger append dedupes, and the pin
	// is simply written — which is what makes the whole operation idempotent.
	//
	// Detection (3A-2) derives it from the envelope; an operator pause gets one
	// from its request.
	IncidentID string
	Reason     domain.PauseReason
	DetectedBy domain.PauseDetection
	// EvidenceJSON is the structured envelope, required for usage_limit and
	// parsed by domain.ParseLimitEnvelope.
	EvidenceJSON string
	// RetryAfter is recorded for audit only and never scheduled against.
	RetryAfter *time.Time
}

// PauseSession durably pins a session as paused and records a pause ledger
// event. It does NOT stop the runtime: the agent process and its pane stay
// exactly as they are, because the point of pause is to stop AO acting, not to
// destroy state the user may want to inspect or resume into.
//
// Idempotent for the same incident, including after a partial failure: the
// ledger append dedupes on id and the pin write is a compare-and-set, so a
// retry converges rather than duplicating.
func (m *Manager) PauseSession(ctx context.Context, id domain.SessionID, req PauseRequest) (domain.SessionRecord, error) {
	incident := strings.TrimSpace(req.IncidentID)
	if err := domain.ValidateIncidentID(incident); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w: %w", id, ErrIncidentRequired, err)
	}

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
	if existing := rec.Metadata.Pause; existing != nil {
		if existing.IncidentID == incident {
			return rec, nil // already recorded
		}
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w (incident %s)", id, ErrAlreadyPaused, existing.IncidentID)
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
	// audit trail, which is a session stuck for a reason nothing recorded. The
	// required incident id is what makes that recoverable: the retry writes the
	// same ledger id (a no-op) and then the pin.
	if err := m.appendPauseLedger(ctx, rec, domain.LifecycleKindPause, incident, pause.EvidenceJSON); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, err)
	}

	set, err := m.store.SetSessionPauseIfAbsent(ctx, id, pause, m.clock())
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: persist: %w", id, err)
	}
	if !set {
		// The compare-and-set lost. Re-read to say WHY rather than guess: it is
		// the same incident (converged, fine), a different one, or the session
		// stopped being pausable while we worked.
		return m.explainLostPauseRace(ctx, id, incident)
	}

	rec.Metadata.Pause = pause
	m.logger.Info("session paused", "sessionID", id, "reason", string(pause.Reason),
		"detectedBy", string(pause.DetectedBy), "incident", incident, "harness", string(rec.Harness))
	return rec, nil
}

// explainLostPauseRace turns a failed compare-and-set into a specific answer.
func (m *Manager) explainLostPauseRace(ctx context.Context, id domain.SessionID, incident string) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: re-read after contended write: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, ErrNotFound)
	}
	if rec.Metadata.Pause != nil {
		if rec.Metadata.Pause.IncidentID == incident {
			return rec, nil // a concurrent report of the same incident won; converged
		}
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w (incident %s)",
			id, ErrAlreadyPaused, rec.Metadata.Pause.IncidentID)
	}
	if rec.IsTerminated {
		return domain.SessionRecord{}, fmt.Errorf("pause %s: %w", id, ErrTerminated)
	}
	// Not paused, not terminated, yet the guarded write matched nothing: the
	// row moved under us in a way this code does not model. Fail loudly rather
	// than report a pause that is not there.
	return domain.SessionRecord{}, fmt.Errorf("pause %s: pin write matched no row and the session is not paused", id)
}

// ResumeSession clears the pause pin and records a resume ledger event. It is
// the ONLY way a pause is lifted.
//
// expectedIncident is REQUIRED and is the incident the caller believes it is
// answering. Without it the operation is "lift whatever is currently there",
// which is unsafe across any gap between the human deciding and the request
// arriving: an operator or UI action raised for incident A can land after A was
// resumed and a fresh limit B pinned the session, and would then resume B —
// releasing a session on evidence nobody ever looked at. Reading the pin and
// clearing that same pin makes the read-your-own-write vacuous; the caller's
// expectation has to come from outside.
//
// It deliberately does not send or relaunch anything. Resuming restores AO's
// permission to write, not an obligation to: re-sending on resume would
// reintroduce the automatic send that pause exists to prevent, and
// MASTER_PLAN §7 puts manual continue in 3B, on an explicit ladder rung. For a
// session whose agent died while paused, resuming does not bring it back
// either — that is a separate, explicit restore.
func (m *Manager) ResumeSession(ctx context.Context, id domain.SessionID, expectedIncident string) (domain.SessionRecord, error) {
	expected := strings.TrimSpace(expectedIncident)
	// Bounded here too: resume never builds a SessionPause, so Validate's own
	// check would not run on this path.
	if err := domain.ValidateIncidentID(expected); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w: %w", id, ErrIncidentRequired, err)
	}

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
	// Checked BEFORE the ledger: a resume event for an incident this call is
	// not going to lift would be a false entry in the audit trail, and the
	// ledger is append-only.
	if paused.IncidentID != expected {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w: holding incident is %s, not %s",
			id, ErrIncidentMismatch, paused.IncidentID, expected)
	}

	// Ledger first, matching PauseSession: a crash between the two leaves the
	// session paused with a resume event recorded, which a retry makes right.
	// The other order would clear the pin with nothing saying who lifted it.
	if err := m.appendPauseLedger(ctx, rec, domain.LifecycleKindResume, expected, ""); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w", id, err)
	}

	// The CAS carries the CALLER's expectation, not the value just read, so the
	// check above is not merely advisory: a pin that changes between that read
	// and this write still fails here.
	cleared, err := m.store.ClearSessionPauseIfIncident(ctx, id, expected, m.clock())
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("resume %s: persist: %w", id, err)
	}
	if !cleared {
		// Conditional on the incident, so this means the pin changed under us.
		// Lifting whatever is there now would resume the session on evidence
		// this caller never saw.
		current, ok, readErr := m.store.GetSession(ctx, id)
		if readErr != nil {
			return domain.SessionRecord{}, fmt.Errorf("resume %s: re-read after contended write: %w", id, readErr)
		}
		if !ok {
			return domain.SessionRecord{}, fmt.Errorf("resume %s: %w", id, ErrNotFound)
		}
		if current.Metadata.Pause == nil {
			return current, nil // someone else resumed the same incident; converged
		}
		return domain.SessionRecord{}, fmt.Errorf("resume %s: %w: holding incident is %s, not %s",
			id, ErrIncidentMismatch, current.Metadata.Pause.IncidentID, expected)
	}

	rec.Metadata.Pause = nil
	m.logger.Info("session resumed", "sessionID", id, "incident", expected)
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

// pausedSkip reports whether an AUTOMATIC boot-time action must leave this
// session alone, and logs the skip so a quiet boot is not mistaken for a boot
// with nothing to do.
//
// Every automatic relaunch path funnels through this: post-stop switch
// recovery, the live pass's save-and-teardown (whose restore marker is what
// causes the relaunch), and RestoreAll's worker loop and orchestrator election.
// A pause that stops AO writing to a session but lets boot relaunch its agent
// would not be a pause at all — it would just be quiet until the next restart.
func (m *Manager) pausedSkip(rec domain.SessionRecord, action string) bool {
	if rec.Metadata.Pause == nil {
		return false
	}
	m.logger.Info("skipping automatic action for a paused session",
		"sessionID", rec.ID, "action", action,
		"incident", rec.Metadata.Pause.IncidentID,
		"reason", string(rec.Metadata.Pause.Reason))
	return true
}

// recordConfirmedExit persists the death boot just proved, without touching
// anything else. It exists only for the paused path: every other caller reaches
// save-and-teardown, which records the exit as part of terminating the session.
//
// The write goes through the generic UpdateSession deliberately — pause is
// column-owned, so a full-row write CANNOT clear the pin. That is the property
// making this safe to do while a pause is held.
func (m *Manager) recordConfirmedExit(ctx context.Context, rec domain.SessionRecord) error {
	if rec.Activity.State == domain.ActivityExited {
		return nil // already recorded on an earlier boot
	}
	m.logger.Info("recording confirmed runtime exit for a paused session",
		"sessionID", rec.ID, "wasActivity", string(rec.Activity.State))
	rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: m.clock()}
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		// Boot-fatal. Continuing would serve a read model boot has already
		// disproved: the row still claims the agent is working, so the UI
		// offers "Resume" for a process that is gone and never suggests a
		// restart. See ErrPausedLivenessUnresolved.
		return fmt.Errorf("%w: session %s: %w", ErrPausedLivenessUnresolved, rec.ID, err)
	}
	return nil
}
