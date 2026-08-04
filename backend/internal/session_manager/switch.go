package sessionmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/handoff"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// SwitchRequest starts a worker harness switch or same-harness fresh conversation.
type SwitchRequest struct {
	SessionID domain.SessionID
	// TargetHarness is the destination harness. Empty with FreshConversation
	// keeps the current harness.
	TargetHarness domain.AgentHarness
	// TargetModel optional; empty keeps / clears to role-pinned model.
	TargetModel string
	// Semantic is agent-authored context (untrusted for git/tests).
	Semantic domain.SemanticHandoffV1
	// FreshConversation forces same harness + kind fresh_conversation.
	FreshConversation bool
}

// SwitchResult is the outcome of a completed switch/fresh saga (target ack).
type SwitchResult struct {
	Session      domain.SessionRecord
	Compiled     domain.CompiledHandoff
	GenerationID string
	Kind         domain.LifecycleLedgerKind
	Mode         RestoreMode
}

// SwitchWorker runs the #3548-class worker switch / fresh-conversation saga:
// fence → ledger phases → observe → compile → pre_stop → stop source →
// post_stop (handoff retained) → relaunch target → target_ack.
//
// Pre-stop failure: source remains usable.
// Post-stop failure: handoff retained on ledger; returns ErrSwitchPostStop.
func (m *Manager) SwitchWorker(ctx context.Context, req SwitchRequest) (SwitchResult, error) {
	if !m.beginSwitch(req.SessionID) {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrSwitchInProgress)
	}
	defer m.endSwitch(req.SessionID)

	rec, ok, err := m.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindWorker {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrNotWorker)
	}
	if rec.IsTerminated {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrTerminated)
	}
	meta := rec.Metadata
	if meta.WorkspacePath == "" || meta.RuntimeHandleID == "" {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrIncompleteHandle)
	}

	fromHarness := rec.Harness
	toHarness := req.TargetHarness
	kind := domain.LifecycleKindSwitch
	sameHarness := req.FreshConversation
	if req.FreshConversation {
		toHarness = fromHarness
		kind = domain.LifecycleKindFreshConversation
		sameHarness = true
	}
	if toHarness == "" {
		toHarness = fromHarness
		sameHarness = true
		if kind == domain.LifecycleKindSwitch && fromHarness == toHarness {
			// Explicit same-harness without FreshConversation flag still allowed
			// as fresh-style restart when harness unchanged.
			kind = domain.LifecycleKindFreshConversation
			sameHarness = true
		}
	}
	if !sameHarness && fromHarness == toHarness {
		sameHarness = true
		kind = domain.LifecycleKindFreshConversation
	}

	if err := requireSwitchCaps(fromHarness, toHarness, sameHarness); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %q", req.SessionID, ErrUnknownHarness, toHarness)
	}
	// WW=false roles must land on a RO-capable target harness.
	if rec.Metadata.Role.RoleID != "" && !rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(toHarness); err != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: %w: %v", req.SessionID, ErrReadOnlyUnsupported, err)
		}
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}

	sourceGen := strings.TrimSpace(meta.RuntimeLaunchID)
	targetGen := m.newSwitchGeneration()
	roleID := strings.TrimSpace(meta.Role.RoleID)
	fromModel := strings.TrimSpace(meta.Role.ResolvedModel)
	toModel := strings.TrimSpace(req.TargetModel)
	if toModel == "" {
		toModel = fromModel
	}

	sem := req.Semantic
	if sem.SchemaVersion == 0 {
		sem.SchemaVersion = domain.SemanticHandoffSchemaVersion
	}
	if sem.SourceGeneration == "" {
		sem.SourceGeneration = sourceGen
	}
	if sem.NativeSessionID == "" {
		sem.NativeSessionID = meta.AgentSessionID
	}

	// --- requested ---
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseRequested, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", "{}"); err != nil {
		return SwitchResult{}, err
	}

	obs := handoff.ObserveWorkspace(ctx, handoff.ObserveInput{
		Worktree:     meta.WorkspacePath,
		GenerationID: sourceGen,
		Now:          m.clock(),
	})
	compiled := handoff.Compile(handoff.CompileInput{
		Semantic:         sem,
		Observed:         obs,
		RoleID:           roleID,
		TargetGeneration: targetGen,
		SameHarness:      sameHarness,
		FromHarness:      fromHarness,
		ToHarness:        toHarness,
	})
	payload, _ := json.Marshal(map[string]any{
		"semantic": sem,
		"observed": obs,
		"compiled": compiled.Text,
	})

	// --- pre_stop: source still usable ---
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePreStop, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", string(payload)); err != nil {
		return SwitchResult{}, err
	}

	// Stop source runtime; worktree retained. Failure here is still pre-stop
	// if Destroy fails without killing the process — we treat Destroy error as
	// pre-stop failure (source presumed usable).
	handle := ports.RuntimeHandle{ID: meta.RuntimeHandleID}
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", string(payload))
		return SwitchResult{}, fmt.Errorf("switch %s: pre-stop destroy: %w", req.SessionID, err)
	}

	// --- post_stop: handoff retained for retry ---
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePostStop, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", string(payload)); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: ledger: %v", req.SessionID, ErrSwitchPostStop, err)
	}

	// Pin target harness/model; force fresh native session; inject compiled handoff.
	rec.Harness = toHarness
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	if rec.Metadata.Role.RoleID != "" {
		rec.Metadata.Role.ResolvedHarness = toHarness
		rec.Metadata.Role.ResolvedModel = toModel
	}
	rec.Metadata.Prompt = composeSwitchPrompt(rec.Metadata.Prompt, compiled.Text)
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: persist target pin: %v", req.SessionID, ErrSwitchPostStop, err)
	}

	ws := ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
		RepoPath:  rec.Metadata.WorkspaceRepoPath,
	}
	// nil restartHandle → Create fresh (not native resume).
	result, err := m.relaunchSession(ctx, "switch", rec, project, ws, nil)
	if err != nil {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", "", string(payload))
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %v", req.SessionID, ErrSwitchPostStop, err)
	}

	// --- target_ack: target owns input ---
	if err := m.appendSwitchLedger(ctx, result.Session, kind, domain.LifecyclePhaseTargetAck, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", result.Session.Metadata.AgentSessionID, string(payload)); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: target ack ledger: %w", req.SessionID, err)
	}

	return SwitchResult{
		Session:      result.Session,
		Compiled:     compiled,
		GenerationID: targetGen,
		Kind:         kind,
		Mode:         result.Mode,
	}, nil
}

// FreshConversation is same-harness switch with a new native session + handoff.
func (m *Manager) FreshConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (SwitchResult, error) {
	return m.SwitchWorker(ctx, SwitchRequest{
		SessionID:         sessionID,
		Semantic:          semantic,
		FreshConversation: true,
	})
}

func requireSwitchCaps(from, to domain.AgentHarness, sameHarness bool) error {
	fc := capabilities.For(from)
	tc := capabilities.For(to)
	if !fc.SpawnSupported || !tc.SpawnSupported {
		return fmt.Errorf("%w: spawn_supported required", ErrSwitchNotSupported)
	}
	// Same-harness fresh still requires switch_supported (native restart path).
	if !fc.SwitchSupported {
		return fmt.Errorf("%w: source %q", ErrSwitchNotSupported, from)
	}
	if !sameHarness && !tc.SwitchSupported {
		return fmt.Errorf("%w: target %q", ErrSwitchNotSupported, to)
	}
	if sameHarness && !tc.SwitchSupported {
		return fmt.Errorf("%w: harness %q", ErrSwitchNotSupported, to)
	}
	return nil
}

func (m *Manager) beginSwitch(id domain.SessionID) bool {
	m.switchMu.Lock()
	defer m.switchMu.Unlock()
	if _, exists := m.switching[id]; exists {
		return false
	}
	// Also refuse if resume is in flight (shared worktree/runtime).
	m.resumeMu.Lock()
	_, resuming := m.resuming[id]
	m.resumeMu.Unlock()
	if resuming {
		return false
	}
	m.switching[id] = struct{}{}
	return true
}

func (m *Manager) endSwitch(id domain.SessionID) {
	m.switchMu.Lock()
	delete(m.switching, id)
	m.switchMu.Unlock()
}

func (m *Manager) newSwitchGeneration() string {
	if m.newLaunchID != nil {
		return m.newLaunchID()
	}
	return uuid.NewString()
}

func (m *Manager) appendSwitchLedger(
	ctx context.Context,
	rec domain.SessionRecord,
	kind domain.LifecycleLedgerKind,
	phase domain.LifecycleLedgerPhase,
	gen string,
	from, to domain.AgentHarness,
	fromModel, toModel, roleID, sourceNative, targetNative, payload string,
) error {
	id := fmt.Sprintf("%s-%s-%s", phase, gen, uuid.NewString())
	err := m.store.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
		ID:                    id,
		SessionID:             rec.ID,
		ProjectID:             rec.ProjectID,
		Kind:                  kind,
		Phase:                 phase,
		GenerationID:          gen,
		FromHarness:           from,
		ToHarness:             to,
		FromModel:             fromModel,
		ToModel:               toModel,
		RoleID:                roleID,
		SourceNativeSessionID: sourceNative,
		TargetNativeSessionID: targetNative,
		PayloadJSON:           payload,
		CreatedAt:             m.clock(),
	})
	if err != nil {
		return fmt.Errorf("lifecycle ledger %s/%s: %w", kind, phase, err)
	}
	return nil
}

func composeSwitchPrompt(priorTask, compiled string) string {
	compiled = strings.TrimSpace(compiled)
	priorTask = strings.TrimSpace(priorTask)
	switch {
	case compiled == "":
		return priorTask
	case priorTask == "":
		return compiled
	default:
		return compiled + "\n\n## Prior task prompt\n" + priorTask
	}
}
