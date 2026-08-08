package sessionmanager

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Orchestrator in-place fresh conversation (2B-1).
//
// The orchestrator is the longest-lived session in any project and therefore
// the worst context-exhaustion offender, so giving it a fresh conversation
// without losing its coordination state is the whole point of this slice.
//
// In-place is what makes it tractable: the session id, the canonical worktree
// and the branch are all unchanged, so nothing has to rebind. A worker's system
// prompt embeds its orchestrator's session id and is only recomputed at
// spawn/restore — a successor-session shape would leave every live worker
// addressing a coordinator that no longer exists. Keeping the id sidesteps that
// entirely.

// ObserveOrchestratorFleet reads the project's workers straight from the session
// table, which is what makes the result trustworthy: it is AO's own record, not
// the outgoing orchestrator's recollection of what it started.
//
// Terminated workers are included on purpose. After a long conversation the
// correction a coordinator most needs is usually "that one already finished",
// and omitting them would leave the fresh conversation unable to distinguish
// "never existed" from "done".
func (m *Manager) ObserveOrchestratorFleet(ctx context.Context, projectID domain.ProjectID, generationID string) (domain.ObservedOrchestratorV1, error) {
	recs, err := m.store.ListSessions(ctx, projectID)
	if err != nil {
		return domain.ObservedOrchestratorV1{}, fmt.Errorf("observe fleet for %s: %w", projectID, err)
	}
	obs := domain.ObservedOrchestratorV1{
		SchemaVersion: domain.ObservedOrchestratorSchemaVersion,
		ProjectID:     projectID,
		ObservedAt:    m.clock(),
		GenerationID:  generationID,
	}
	var live, terminated []domain.ObservedWorkerV1
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator {
			continue
		}
		w := domain.ObservedWorkerV1{
			SessionID:    rec.ID,
			Harness:      rec.Harness,
			RoleID:       rec.Metadata.Role.RoleID,
			Branch:       rec.Metadata.Branch,
			Activity:     string(rec.Activity.State),
			IsTerminated: rec.IsTerminated,
		}
		if rec.IsTerminated {
			terminated = append(terminated, w)
		} else {
			live = append(live, w)
		}
	}
	// Stable order: the compiled handoff is embedded in a launch prompt, and an
	// order that shifts between runs makes diffing two handoffs useless.
	byID := func(s []domain.ObservedWorkerV1) {
		sort.Slice(s, func(i, j int) bool { return s[i].SessionID < s[j].SessionID })
	}
	byID(live)
	byID(terminated)

	// BOUNDED, and the bound is not cosmetic. This text goes into the target's
	// launch prompt — on Codex, into argv — and the launch happens AFTER the
	// source has stopped. A project with thousands of historical workers would
	// therefore produce an oversized prompt at the one moment failure is most
	// expensive, and post-stop recovery would retry the same oversized prompt
	// forever.
	//
	// Live workers are never dropped: they are what the coordinator must act on,
	// and a project cannot have an unbounded number of them running. Terminated
	// ones are history, so the newest are kept and the rest are counted. Dropping
	// silently would be the real hazard — a truncated fleet that reads as
	// complete — so the omission is reported in the rendered text.
	obs.Workers = live
	if len(terminated) > maxObservedTerminatedWorkers {
		obs.OmittedTerminated = len(terminated) - maxObservedTerminatedWorkers
		terminated = terminated[len(terminated)-maxObservedTerminatedWorkers:]
	}
	obs.Workers = append(obs.Workers, terminated...)
	return obs, nil
}

// maxObservedTerminatedWorkers caps the history carried into a handoff. Live
// workers are exempt; see ObserveOrchestratorFleet.
const maxObservedTerminatedWorkers = 25

// FreshOrchestratorConversation restarts a project's orchestrator in place with
// a compiled handoff, keeping its session id, worktree and branch.
//
// It takes the PROJECT OWNERSHIP GATE before the per-session switch fence, and
// that order is not incidental: the gate is the manager's ownership boundary
// (lock order projectOwnership -> beginSwitch -> lifecycle/store, never
// inverted). Without it, EnsureOrchestrator could retire and replace this very
// session midway through its own saga — the source stopped, a successor
// spawned, and then the saga relaunching a target into a workspace it no longer
// owns.
func (m *Manager) FreshOrchestratorConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (SwitchResult, error) {
	return m.orchestratorSwitch(ctx, SwitchRequest{
		SessionID:         sessionID,
		Semantic:          semantic,
		FreshConversation: true,
	})
}

// SwitchOrchestrator moves a project's orchestrator to an exact target from
// its host-owned role map. The project ownership gate is acquired before the
// per-session switch fence inside switchUnderOwnership; this lock order is the
// ownership boundary for the canonical orchestrator workspace.
func (m *Manager) SwitchOrchestrator(ctx context.Context, req SwitchRequest) (SwitchResult, error) {
	if req.FreshConversation {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: fresh conversation must use FreshOrchestratorConversation", req.SessionID)
	}
	return m.orchestratorSwitch(ctx, req)
}

func (m *Manager) orchestratorSwitch(ctx context.Context, req SwitchRequest) (SwitchResult, error) {
	sessionID := req.SessionID
	rec, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindOrchestrator {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, ErrNotOrchestrator)
	}

	// Gate FIRST, then the saga's own fence inside switchUnderOwnership.
	release, err := m.acquireProjectOwnership(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, err)
	}
	defer release()

	// The pre-gate read exists only to find the ownership key. Reload both the
	// session and project under the gate before target authorization; otherwise
	// a role-map update can invalidate an authorization while this operation is
	// waiting for ownership.
	rec, ok, err = m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: reload under ownership: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindOrchestrator {
		return SwitchResult{}, fmt.Errorf("orchestrator switch %s: %w", sessionID, ErrNotOrchestrator)
	}
	if !req.FreshConversation {
		model, authErr := m.authorizeOrchestratorSwitchTarget(ctx, rec, req.TargetHarness, req.TargetModel)
		if authErr != nil {
			return SwitchResult{}, fmt.Errorf("orchestrator switch %s: authorize target: %w", sessionID, authErr)
		}
		req.TargetModel = model
	}

	return m.switchUnderOwnership(ctx, req, true)
}

// authorizeOrchestratorSwitchTarget resolves the same primary+ladder target set
// used by the service. Empty model retains the shared resolver's meaning:
// provider default for a unique default target, or the one fixed model when
// that harness has a unique configured entry.
func (m *Manager) authorizeOrchestratorSwitchTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	targetHarness domain.AgentHarness,
	requestedModel string,
) (string, error) {
	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	if roleID == "" {
		return "", fmt.Errorf("role pin required: %w", domain.ErrSwitchTargetUnauthorized)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return "", err
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if roleMap.IsZero() {
		return "", fmt.Errorf("project has no role map: %w", domain.ErrSwitchTargetUnauthorized)
	}
	if _, ok := roleMap.Roles[roleID]; !ok {
		return "", fmt.Errorf("role %q is absent from role map: %w", roleID, domain.ErrSwitchTargetUnauthorized)
	}
	return domain.ResolveAuthorizedSwitchModel(roleMap, roleID, targetHarness, requestedModel)
}
