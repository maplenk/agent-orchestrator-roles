package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// failoverCommander is the manager surface used by manual failover. Target
// resolution stays entirely behind this interface: callers provide an incident,
// and the manager selects the next unused role-map rung.
type failoverCommander interface {
	ContinueFailover(
		ctx context.Context,
		id domain.SessionID,
		req sessionmanager.ContinueFailoverRequest,
	) (sessionmanager.ContinueFailoverResult, error)

	FailoverPreview(
		ctx context.Context,
		id domain.SessionID,
	) (sessionmanager.FailoverPreview, error)
}

// ContinueFailoverOutcome is the API-facing result of a manual continuation.
// Target is an output only: there is deliberately no service request type with a
// harness, model, or role field.
type ContinueFailoverOutcome struct {
	Session      domain.Session
	IncidentID   string
	GenerationID string
	Target       domain.FailoverTarget
	RungIndex    int
	AttemptSeq   int
	Reused       bool
}

// ContinueFailover moves a paused worker to the next host-authorized failover
// rung. The incident must name the exact pause being answered.
func (s *Service) ContinueFailover(
	ctx context.Context,
	sessionID domain.SessionID,
	incidentID string,
) (ContinueFailoverOutcome, error) {
	incident, err := validIncident(incidentID,
		"An incidentId is required: continue must name the incident it answers")
	if err != nil {
		return ContinueFailoverOutcome{}, err
	}

	fc, ok := s.manager.(failoverCommander)
	if !ok {
		return ContinueFailoverOutcome{}, fmt.Errorf("%w", sessionmanager.ErrFailoverNotWired)
	}
	res, err := fc.ContinueFailover(ctx, sessionID, sessionmanager.ContinueFailoverRequest{
		IncidentID: incident,
	})
	if err != nil {
		return ContinueFailoverOutcome{}, toAPIError(err)
	}
	sess, err := s.toSession(ctx, res.Session)
	if err != nil {
		return ContinueFailoverOutcome{}, err
	}
	return ContinueFailoverOutcome{
		Session:      sess,
		IncidentID:   res.IncidentID,
		GenerationID: res.GenerationID,
		Target:       res.Target,
		RungIndex:    res.RungIndex,
		AttemptSeq:   res.AttemptSeq,
		Reused:       res.Reused,
	}, nil
}

// FailoverPreview asks the manager what Continue would do for the session. The
// service intentionally does not inspect the role map or attempt history itself.
func (s *Service) FailoverPreview(
	ctx context.Context,
	sessionID domain.SessionID,
) (sessionmanager.FailoverPreview, error) {
	fc, ok := s.manager.(failoverCommander)
	if !ok {
		return sessionmanager.FailoverPreview{}, fmt.Errorf("%w", sessionmanager.ErrFailoverNotWired)
	}
	preview, err := fc.FailoverPreview(ctx, sessionID)
	if err != nil {
		return sessionmanager.FailoverPreview{}, toAPIError(err)
	}
	return preview, nil
}
