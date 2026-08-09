package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

var (
	errAutomaticFailoverDisabled         = errors.New("automatic failover is not enabled for this incident")
	errAutomaticFailoverAlreadyAttempted = errors.New("automatic failover already attempted for this incident")
)

type automaticFailoverKey struct {
	SessionID  domain.SessionID
	IncidentID string
}

// AutomaticFailoverRequest names the exact structured-limit incident that was
// just pinned. There is deliberately no target input: ContinueFailover remains
// the only authority that selects a rung and mints a switch generation.
type AutomaticFailoverRequest struct {
	IncidentID string
}

// AutomaticFailoverResult describes the one-shot automatic decision. Enabled
// means this call confirmed the current project/runtime policy and found that
// the incident had already consumed its automatic action. Attempted means this
// call handed the new or recoverable same-generation attempt to Continue.
// A concurrent duplicate may report both false because it deliberately does
// not wait for, or infer policy from, the call that owns the same incident.
type AutomaticFailoverResult struct {
	Session   domain.SessionRecord
	Enabled   bool
	Attempted bool
	Continue  ContinueFailoverResult
}

// ContinueAutomaticFailover advances at most one rung for a newly pinned
// structured-limit incident when the project explicitly selects automatic
// mode. It is not a second failover implementation: the accepted manual
// Continue transaction owns target selection, generation identity, runtime
// switching, crash convergence, attempt accounting, and incident-bound pin
// clearing.
//
// A terminal failed attempt suppresses every later automatic action. Requested
// and post_stop attempts are instead re-driven on their own stored target and
// generation, and acked-but-pinned attempts converge only by clearing the exact
// incident after promotion proof. Duplicate vendor delivery and the boot
// durability pass therefore recover the first action but can never select a
// second rung. There is no timer, scheduler, retry loop, or background worker.
func (m *Manager) ContinueAutomaticFailover(
	ctx context.Context,
	id domain.SessionID,
	req AutomaticFailoverRequest,
) (AutomaticFailoverResult, error) {
	incident := strings.TrimSpace(req.IncidentID)
	if err := domain.ValidateIncidentID(incident); err != nil {
		return AutomaticFailoverResult{}, fmt.Errorf("automatic failover %s: %w: %w", id, ErrIncidentRequired, err)
	}

	// Duplicate structured deliveries can race in the same daemon. Serialize
	// their read-before-first-attempt window and exclude Restart Agent across
	// the generation check, durable append, and switch ownership handoff. The
	// durable attempt row handles every later delivery and survives a restart.
	if !m.beginAutomaticFailover(id, incident) {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return AutomaticFailoverResult{}, fmt.Errorf("automatic failover %s: read concurrent state: %w", id, err)
		}
		if !ok {
			return AutomaticFailoverResult{}, fmt.Errorf("automatic failover %s: %w", id, ErrNotFound)
		}
		return AutomaticFailoverResult{Session: rec}, nil
	}
	defer m.endAutomaticFailover(id, incident)

	continued, err := m.continueFailover(ctx, id, ContinueFailoverRequest{IncidentID: incident}, true)
	if errors.Is(err, errAutomaticFailoverDisabled) || errors.Is(err, errAutomaticFailoverAlreadyAttempted) {
		rec, ok, readErr := m.store.GetSession(ctx, id)
		if readErr != nil {
			return AutomaticFailoverResult{}, fmt.Errorf("automatic failover %s: read no-op state: %w", id, readErr)
		}
		if !ok {
			return AutomaticFailoverResult{}, fmt.Errorf("automatic failover %s: %w", id, ErrNotFound)
		}
		return AutomaticFailoverResult{
			Session: rec,
			Enabled: errors.Is(err, errAutomaticFailoverAlreadyAttempted),
		}, nil
	}
	if err != nil {
		rec, ok, readErr := m.store.GetSession(ctx, id)
		attempted := false
		var attemptsErr error
		if store, storeErr := m.failoverStore(); storeErr == nil {
			attempts, listErr := store.ListSessionFailoverAttemptsByIncident(ctx, id, incident)
			if listErr != nil {
				attemptsErr = fmt.Errorf("automatic failover %s: read attempt truth: %w", id, listErr)
			} else {
				attempted = len(attempts) != 0
			}
		}
		result := AutomaticFailoverResult{Enabled: attempted, Attempted: attempted}
		if ok {
			result.Session = rec
		}
		continueErr := fmt.Errorf("automatic failover %s: %w", id, err)
		if !ok && readErr == nil {
			readErr = ErrNotFound
		}
		if readErr != nil || attemptsErr != nil {
			var latestErr error
			if readErr != nil {
				latestErr = fmt.Errorf("automatic failover %s: read latest state: %w", id, readErr)
			}
			return result, errors.Join(continueErr, attemptsErr, latestErr)
		}
		return result, continueErr
	}
	return AutomaticFailoverResult{
		Session: continued.Session, Enabled: true, Attempted: true, Continue: continued,
	}, nil
}

// automaticFailoverPolicy is the runtime gate for legacy/stored configs as
// well as newly validated ones. Automatic work is allowed only for a durable
// structured usage-limit pin with exact recorded-source provenance and exact
// opt-in mode. Runtime capabilities are checked separately against the source
// and durable target immediately before any recovery or new selection.
func (m *Manager) automaticFailoverPolicy(
	ctx context.Context,
	rec domain.SessionRecord,
) (domain.ProjectRecord, domain.AgentHarness, bool, error) {
	pause := rec.Metadata.Pause
	if pause == nil || pause.Reason != domain.PauseReasonUsageLimit ||
		pause.DetectedBy != domain.PauseDetectionStructured {
		return domain.ProjectRecord{}, "", false, nil
	}
	if err := pause.Validate(); err != nil {
		return domain.ProjectRecord{}, "", false, fmt.Errorf("invalid structured pause: %w", err)
	}
	env, err := domain.ParseLimitEnvelope(pause.EvidenceJSON)
	if err != nil {
		return domain.ProjectRecord{}, "", false, fmt.Errorf("parse structured pause envelope: %w", err)
	}
	sourceHarness := domain.AgentHarness(strings.TrimSpace(string(pause.Harness)))
	if sourceHarness == "" || env.Harness == "" || env.Harness != sourceHarness {
		return domain.ProjectRecord{}, "", false, fmt.Errorf(
			"structured pause harness provenance mismatch: pause=%q envelope=%q",
			pause.Harness, env.Harness)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.ProjectRecord{}, "", false, err
	}
	if project.Config.RoleMap.Failover.Mode != domain.FailoverModeAutomatic {
		return project, sourceHarness, false, nil
	}
	return project, sourceHarness, true, nil
}

func (m *Manager) automaticFailoverHarnessSupported(h domain.AgentHarness) bool {
	caps := m.automaticFailoverCaps(h)
	return caps.SpawnSupported && caps.SwitchSupported && caps.LimitDetectionSupported
}

func (m *Manager) automaticFailoverCaps(h domain.AgentHarness) capabilities.Caps {
	if m.automaticFailoverCapsOverride != nil {
		return m.automaticFailoverCapsOverride(h)
	}
	return capabilities.For(h)
}

func (m *Manager) beginAutomaticFailover(id domain.SessionID, incident string) bool {
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	if _, exists := m.resuming[id]; exists {
		return false
	}
	if _, exists := m.switching[id]; exists {
		return false
	}
	if m.automaticFailovers == nil {
		m.automaticFailovers = make(map[automaticFailoverKey]struct{})
	}
	key := automaticFailoverKey{SessionID: id, IncidentID: incident}
	if _, exists := m.automaticFailovers[key]; exists {
		return false
	}
	m.automaticFailovers[key] = struct{}{}
	return true
}

func (m *Manager) endAutomaticFailover(id domain.SessionID, incident string) {
	m.ownershipMu.Lock()
	delete(m.automaticFailovers, automaticFailoverKey{SessionID: id, IncidentID: incident})
	m.ownershipMu.Unlock()
}
