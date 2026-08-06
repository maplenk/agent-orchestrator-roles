package sessionmanager

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OrchestratorRetireNotice warns an outgoing orchestrator to stop coordinating.
//
// It must not promise a new workspace: the orchestrator worktree and branch are
// canonical per project, so the successor reuses this exact workspace once
// RetireForReplacement releases it.
const OrchestratorRetireNotice = "AO is replacing this project orchestrator. " +
	"Stop coordinating new work now; a fresh orchestrator will take over this workspace."

// acquireProjectOwnership takes the project-scoped ownership gate and returns a
// release func. It is the ownership boundary for every operation that can
// create, retire, or activate an orchestrator.
//
// Why a gate and not just the schema: a unique index arbitrates final row
// cardinality, but the restore path creates or *adopts* the canonical
// orchestrator worktree before flipping an existing row to active, and
// gitworktree.Create adopts an existing registration rather than failing. The
// damage therefore lands before any constraint is reached. Only serializing the
// whole operation prevents it.
//
// Lock order is projectOwnership -> beginSwitch -> lifecycle/store, and must
// never be inverted. ownershipMu is held only to look the channel up, never
// while waiting on it, so a blocked caller cannot stall unrelated projects.
// Different projects run fully concurrently; only identical project ids
// serialize.
func (m *Manager) acquireProjectOwnership(ctx context.Context, project domain.ProjectID) (func(), error) {
	gate := m.projectOwnershipGate(project)
	select {
	case gate <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-gate }) }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("project ownership %s: %w", project, ctx.Err())
	}
}

func (m *Manager) projectOwnershipGate(project domain.ProjectID) chan struct{} {
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	if m.projectOwnership == nil {
		m.projectOwnership = make(map[domain.ProjectID]chan struct{})
	}
	gate, ok := m.projectOwnership[project]
	if !ok {
		gate = make(chan struct{}, 1)
		m.projectOwnership[project] = gate
	}
	return gate
}

// EnsureOrchestratorResult carries the raw outcome of EnsureOrchestrator. The
// caller converts to a presentation model and emits telemetry *after* the gate
// has been released.
type EnsureOrchestratorResult struct {
	Record            domain.SessionRecord
	PromptBytes       int
	SystemPromptBytes int
	// Reused is true when an existing active orchestrator was returned instead
	// of spawning one (the idempotent, non-clean path).
	Reused bool
}

// EnsureOrchestrator is the single high-level ownership command: it resolves the
// current owner, and when clean is set, retires every active orchestrator and
// spawns the successor — all under one project gate, so no concurrent restore,
// spawn, or retirement can interleave with the retire→spawn window.
//
// When clean is false it is idempotent: an existing active orchestrator is
// returned as-is.
//
// Callers must not hold the project gate; this acquires it.
func (m *Manager) EnsureOrchestrator(ctx context.Context, cfg ports.SpawnConfig, clean bool) (EnsureOrchestratorResult, error) {
	cfg.Kind = domain.KindOrchestrator
	release, err := m.acquireProjectOwnership(ctx, cfg.ProjectID)
	if err != nil {
		return EnsureOrchestratorResult{}, err
	}
	defer release()

	// Read only after the gate is held: any pre-gate view of ownership is stale
	// by construction.
	existing, err := m.activeOrchestratorRecords(ctx, cfg.ProjectID)
	if err != nil {
		return EnsureOrchestratorResult{}, err
	}

	if !clean {
		// Either branch below ends with the project owning an orchestrator, and
		// the intent means "this project is OWED one" — so both discharge it.
		// A stranded project is most often rescued by an ordinary idempotent
		// spawn like this one, not by a clean replacement, and leaving the
		// intent behind would leave a durable record contradicting the live
		// state until the next boot happened to notice.
		if len(existing) > 0 {
			m.dischargeReplacementIntent(ctx, cfg.ProjectID)
			return EnsureOrchestratorResult{Record: newestOrchestratorRecord(existing), Reused: true}, nil
		}
		rec, promptBytes, systemPromptBytes, err := m.spawnUnderOwnership(ctx, cfg)
		if err != nil {
			return EnsureOrchestratorResult{}, err
		}
		m.dischargeReplacementIntent(ctx, cfg.ProjectID)
		return EnsureOrchestratorResult{Record: rec, PromptBytes: promptBytes, SystemPromptBytes: systemPromptBytes}, nil
	}

	// Persist intent BEFORE the first destructive step. Retire-first semantics
	// force a zero-owner interval — the canonical worktree must be released
	// before the successor can create it — so the guarantee cannot be "never
	// zero"; it is "never stuck at zero". Writing this first is what makes the
	// difference: a crash or spawn failure from here on leaves a durable record
	// that the project is owed an orchestrator, which boot recovery honours.
	//
	// A failure to record intent aborts BEFORE anything is destroyed. Retiring
	// without it is the one ordering that can strand a project silently.
	var retiring domain.SessionID
	if len(existing) > 0 {
		retiring = newestOrchestratorRecord(existing).ID
	}
	if err := m.store.PutOrchestratorReplacementIntent(ctx, domain.OrchestratorReplacementIntent{
		ProjectID:        cfg.ProjectID,
		RetiredSessionID: retiring,
		RequestedAt:      m.clock(),
	}); err != nil {
		return EnsureOrchestratorResult{}, fmt.Errorf("record replacement intent for %s: %w", cfg.ProjectID, err)
	}

	for _, orch := range existing {
		// Best effort: a retire notice can legitimately be suppressed (pane
		// exited, awaiting input). Replacement must not depend on it landing.
		if sendErr := m.Send(ctx, orch.ID, OrchestratorRetireNotice); sendErr != nil {
			m.logger.Warn("orchestrator retire notice not delivered",
				"session", orch.ID, "project", cfg.ProjectID, "err", sendErr)
		}
		if err := m.retireForReplacementUnderOwnership(ctx, orch.ID); err != nil {
			return EnsureOrchestratorResult{}, err
		}
	}

	rec, promptBytes, systemPromptBytes, err := m.spawnUnderOwnership(ctx, cfg)
	if err != nil {
		// Intent deliberately RETAINED: the project is now at zero owners and
		// this is exactly the state recovery exists for.
		return EnsureOrchestratorResult{}, err
	}
	m.dischargeReplacementIntent(ctx, cfg.ProjectID)
	return EnsureOrchestratorResult{Record: rec, PromptBytes: promptBytes, SystemPromptBytes: systemPromptBytes}, nil
}

// dischargeReplacementIntent clears a project's obligation once a successor is
// live. Best-effort by design: a retained intent costs one redundant "is there
// an orchestrator?" check on the next boot, which finds one and discharges it,
// whereas failing the call would report a successful replacement as an error.
func (m *Manager) dischargeReplacementIntent(ctx context.Context, project domain.ProjectID) {
	if err := m.store.DeleteOrchestratorReplacementIntent(ctx, project); err != nil {
		m.logger.Warn("orchestrator replacement intent not cleared; boot will re-check",
			"project", project, "err", err)
	}
}

// activeOrchestratorRecords lists every non-terminated orchestrator for a
// project. Callers must already hold the project gate.
func (m *Manager) activeOrchestratorRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	out := make([]domain.SessionRecord, 0, 1)
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			out = append(out, rec)
		}
	}
	return out, nil
}

// newestOrchestratorRecord picks a deterministic survivor: newest CreatedAt,
// then newest UpdatedAt, then lexically greatest id. Deliberately not
// ListSessions order, which is insertion order and would make the choice
// depend on row history.
func newestOrchestratorRecord(recs []domain.SessionRecord) domain.SessionRecord {
	sorted := append([]domain.SessionRecord(nil), recs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return orchestratorRecordNewer(sorted[i], sorted[j])
	})
	return sorted[0]
}

func orchestratorRecordNewer(a, b domain.SessionRecord) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.After(b.CreatedAt)
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

// ProjectOrchestrator returns the project's current owner, resolved under the
// project ownership gate by the ONE rule every other path uses
// (newestOrchestratorRecord). ok=false means the project has none.
//
// Exported because delegation has to address the orchestrator, and must not
// re-derive ownership to do it. A second resolver in the service layer is
// exactly the defect 2B-0a removed: the manager's took the first active row in
// list order while the service took the newest by CreatedAt, and they disagreed
// precisely during a transfer window — the moment it matters. Migration 0057
// now makes two active orchestrators unrepresentable, but that is a reason to
// have one resolver, not a licence to add another.
//
// The gate is released before the caller sends anything, so no pane write is
// ever made while holding it.
func (m *Manager) ProjectOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, bool, error) {
	release, err := m.acquireProjectOwnership(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	defer release()

	recs, err := m.activeOrchestratorRecords(ctx, projectID)
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("project orchestrator %s: %w", projectID, err)
	}
	if len(recs) == 0 {
		return domain.SessionRecord{}, false, nil
	}
	return newestOrchestratorRecord(recs), true, nil
}
