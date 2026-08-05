package sessionmanager

import (
	"context"
	"fmt"
	"sort"

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
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator {
			continue
		}
		obs.Workers = append(obs.Workers, domain.ObservedWorkerV1{
			SessionID:    rec.ID,
			Harness:      rec.Harness,
			RoleID:       rec.Metadata.Role.RoleID,
			Branch:       rec.Metadata.Branch,
			Activity:     string(rec.Activity.State),
			IsTerminated: rec.IsTerminated,
		})
	}
	// Stable order: the compiled handoff is embedded in a launch prompt, and an
	// order that shifts between runs makes diffing two handoffs useless.
	sort.Slice(obs.Workers, func(i, j int) bool { return obs.Workers[i].SessionID < obs.Workers[j].SessionID })
	return obs, nil
}

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
//
// Cross-harness is deliberately refused here. Under strict delegation an
// orchestrator must be workspaceWrites:false, which requires
// read_only_enforced, which only Codex advertises — so the only legal strict
// in-place operation is codex -> codex, which is a fresh conversation and not a
// switch. Rather than let a non-strict project take a path the strict one
// cannot, cross-harness orchestrator switch is its own slice (2B-3) gated on
// Claude RO.
func (m *Manager) FreshOrchestratorConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (SwitchResult, error) {
	rec, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("orchestrator fresh %s: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("orchestrator fresh %s: %w", sessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindOrchestrator {
		return SwitchResult{}, fmt.Errorf("orchestrator fresh %s: %w", sessionID, ErrNotOrchestrator)
	}

	// Gate FIRST, then the saga's own fence inside SwitchWorker.
	release, err := m.acquireProjectOwnership(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("orchestrator fresh %s: %w", sessionID, err)
	}
	defer release()

	return m.switchUnderOwnership(ctx, SwitchRequest{
		SessionID:         sessionID,
		Semantic:          semantic,
		FreshConversation: true,
	})
}
