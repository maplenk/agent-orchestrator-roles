package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// pauseCommander is the manager surface the operator pause path needs. Narrow
// on purpose, and separate from switchCommander: nothing here touches the
// switch saga.
type pauseCommander interface {
	PauseSession(ctx context.Context, id domain.SessionID, req sessionmanager.PauseRequest) (domain.SessionRecord, error)
	ResumeSession(ctx context.Context, id domain.SessionID, expectedIncident string) (domain.SessionRecord, error)
}

// PauseSession parks a session at an operator's request.
//
// It accepts ONLY an operator pause. A usage_limit pause requires a structured
// envelope that only a harness adapter can produce (3A-2b), and exposing it
// here would hand every API client the free-text channel MASTER_PLAN §7 rule 1
// exists to close — the reason field would become "say you hit a limit and AO
// believes you". Operators pause; they do not report limits.
//
// incidentID is required and client-generated. That is deliberate: the ledger
// row is written before the pin, so a server-minted id the caller never learns
// cannot be retried against, and a retry would open a second incident for one
// real pause. See docs/roles/PHASE3A_PAUSE_CONTRACT.md §3.
func (s *Service) PauseSession(ctx context.Context, sessionID domain.SessionID, incidentID, reason string) (domain.SessionRecord, error) {
	incident := strings.TrimSpace(incidentID)
	if incident == "" {
		return domain.SessionRecord{}, apierr.Invalid("PAUSE_INCIDENT_REQUIRED",
			"An incidentId is required so a retried request cannot open a second incident", nil)
	}
	if r := strings.TrimSpace(reason); r != "" && r != string(domain.PauseReasonOperator) {
		return domain.SessionRecord{}, apierr.Invalid("PAUSE_REASON_INVALID",
			fmt.Sprintf("reason must be %q; a usage limit can only be recorded from a structured harness envelope",
				domain.PauseReasonOperator), nil)
	}

	pc, ok := s.manager.(pauseCommander)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w", ErrSwitchNotWired)
	}
	rec, err := pc.PauseSession(ctx, sessionID, sessionmanager.PauseRequest{
		IncidentID: incident,
		Reason:     domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator,
	})
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return rec, nil
}

// ResumeSession lifts a pause the caller names.
//
// It does NOT start an agent. A session paused while its agent died stays dead
// after a resume — boot deliberately no longer relaunches paused sessions, so
// restarting is a separate, explicit act. Resuming a dead session is still
// meaningful: it records the decision and makes the session restore-eligible.
func (s *Service) ResumeSession(ctx context.Context, sessionID domain.SessionID, incidentID string) (domain.SessionRecord, error) {
	incident := strings.TrimSpace(incidentID)
	if incident == "" {
		return domain.SessionRecord{}, apierr.Invalid("PAUSE_INCIDENT_REQUIRED",
			"An incidentId is required: resume must name the incident it answers, not lift whatever is current", nil)
	}
	pc, ok := s.manager.(pauseCommander)
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("%w", ErrSwitchNotWired)
	}
	return pc.ResumeSession(ctx, sessionID, incident)
}
