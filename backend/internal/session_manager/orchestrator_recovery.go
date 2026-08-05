package sessionmanager

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// RecoverOrchestratorReplacements makes a zero-owner project non-terminal.
//
// EnsureOrchestrator records intent before it retires anything, so an
// interrupted replacement — spawn failed, or the daemon died mid-window —
// leaves a durable "this project is owed an orchestrator". This pass honours
// those at boot.
//
// Deliberately NOT fail-closed, unlike the reap queue and the launch-cleanup
// chain. Those describe state AO cannot describe or correct, where serving
// would let a real process diverge from the database. This is the opposite: a
// project with no orchestrator is inert. Refusing to boot over it would take a
// daemon that is fine for every other project and stop it, which is a worse
// outcome than one project waiting for its next attempt. The intent is durable,
// so nothing is lost by trying again later.
func (m *Manager) RecoverOrchestratorReplacements(ctx context.Context) error {
	intents, err := m.store.ListOrchestratorReplacementIntents(ctx)
	if err != nil {
		// Surfaced, not swallowed: a missing table means the schema is not what
		// this code expects. The caller logs it; boot continues, because an
		// unreadable intent list still cannot corrupt anything.
		return fmt.Errorf("orchestrator recovery: read intents: %w", err)
	}
	if len(intents) == 0 {
		return nil
	}
	m.logger.Info("orchestrator recovery: outstanding replacements", "count", len(intents))

	var failures []error
	for _, intent := range intents {
		if err := m.recoverOneReplacement(ctx, intent); err != nil {
			failures = append(failures, err)
			if stampErr := m.store.RecordOrchestratorReplacementAttempt(
				ctx, intent.ProjectID, m.clock(), err.Error()); stampErr != nil {
				m.logger.Warn("orchestrator recovery: could not stamp attempt",
					"project", intent.ProjectID, "err", stampErr)
			}
		}
	}
	return errors.Join(failures...)
}

// recoverOneReplacement discharges a single project's obligation under that
// project's ownership gate.
func (m *Manager) recoverOneReplacement(ctx context.Context, intent domain.OrchestratorReplacementIntent) error {
	release, err := m.acquireProjectOwnership(ctx, intent.ProjectID)
	if err != nil {
		return fmt.Errorf("orchestrator recovery %s: %w", intent.ProjectID, err)
	}
	defer release()

	// Clean up whatever the interrupted retirement left behind before deciding
	// whether an owner exists — a half-retired row can look like one.
	if err := m.reconcileOrchestratorRetirement(ctx, intent.ProjectID); err != nil {
		return fmt.Errorf("orchestrator recovery %s: %w", intent.ProjectID, err)
	}

	existing, err := m.activeOrchestratorRecords(ctx, intent.ProjectID)
	if err != nil {
		return fmt.Errorf("orchestrator recovery %s: %w", intent.ProjectID, err)
	}
	if len(existing) > 0 {
		// Already recovered — the replacement completed, or a previous boot
		// finished it. Discharge and move on.
		m.logger.Info("orchestrator recovery: project already has an owner; discharging intent",
			"project", intent.ProjectID, "owner", newestOrchestratorRecord(existing).ID)
		return m.store.DeleteOrchestratorReplacementIntent(ctx, intent.ProjectID)
	}

	m.logger.Warn("orchestrator recovery: project has no orchestrator; spawning replacement",
		"project", intent.ProjectID, "retired", intent.RetiredSessionID,
		"priorAttempts", intent.AttemptCount)

	// A standard orchestrator, not a replay of the original request. The
	// orchestrator's prompt, role, workspace and branch are all host-derived
	// from the project, so a default spawn produces the same coordinator the
	// interrupted call was creating. Persisting an entire SpawnConfig to
	// reproduce operator-supplied extras would add a durable copy of
	// spawn-time input for no gain in the recovered outcome.
	if _, _, _, err := m.spawnUnderOwnership(ctx, ports.SpawnConfig{
		ProjectID: intent.ProjectID,
		Kind:      domain.KindOrchestrator,
	}); err != nil {
		return fmt.Errorf("orchestrator recovery %s: spawn: %w", intent.ProjectID, err)
	}
	return m.store.DeleteOrchestratorReplacementIntent(ctx, intent.ProjectID)
}

// reconcileOrchestratorRetirement repairs the residue of a retirement that was
// interrupted between its two writes.
//
// finalizeRetirement marks terminated and then releases the workspace claim.
// Those cannot be one write — MarkTerminated goes through the lifecycle manager
// while the claim is a plain metadata update — so a crash can leave either
// half-state, and this pass clears both. It is idempotent and cheap on a
// healthy project.
//
// Callers must already hold the project's ownership gate.
func (m *Manager) reconcileOrchestratorRetirement(ctx context.Context, project domain.ProjectID) error {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return fmt.Errorf("list sessions for %s: %w", project, err)
	}
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator {
			continue
		}
		switch {
		case rec.IsTerminated && rec.Metadata.WorkspacePath != "":
			// Terminated but still naming the canonical worktree: the residue
			// of finalizeRetirement's own ordering. Harmless while
			// canonicalWorkspaceHeldByActiveOrchestrator guards path-keyed
			// teardown, but the alias should not outlive the boot that found it.
			m.logger.Warn("orchestrator recovery: clearing stale workspace claim on a retired orchestrator",
				"project", project, "session", rec.ID, "path", rec.Metadata.WorkspacePath)
			if err := m.releaseRetiredWorkspaceClaim(ctx, rec.ID); err != nil {
				return fmt.Errorf("clear stale claim on %s: %w", rec.ID, err)
			}

		case !rec.IsTerminated && rec.Metadata.WorkspacePath == "":
			// ACTIVE with no workspace. It cannot coordinate anything, yet it
			// occupies the project's single active-orchestrator slot, so no
			// successor can be created while it stands and EnsureOrchestrator's
			// idempotent path would hand a caller this empty shell. Nothing
			// legitimate looks like this at boot: a healthy orchestrator always
			// records the canonical path, and a spawn in flight would hold the
			// gate this pass now holds.
			m.logger.Warn("orchestrator recovery: terminating an active orchestrator that owns no workspace",
				"project", project, "session", rec.ID)
			if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
				return fmt.Errorf("terminate claimless orchestrator %s: %w", rec.ID, err)
			}
		}
	}
	return nil
}
