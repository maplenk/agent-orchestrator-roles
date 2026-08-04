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

// switchPayload is the durable post_stop handoff blob (JSON).
type switchPayload struct {
	Semantic domain.SemanticHandoffV1    `json:"semantic"`
	Observed domain.ObservedWorkspaceV1  `json:"observed"`
	Compiled string                      `json:"compiled"`
}

// RecoverSwitchFromPostStop re-drives a worker switch/fresh that reached
// post_stop (source stopped, handoff on ledger) but never target_ack.
// Safe to call on boot and after ErrSwitchPostStop.
//
// Idempotent: if no incomplete post_stop exists, returns ErrSwitchNothingToRecover.
func (m *Manager) RecoverSwitchFromPostStop(ctx context.Context, sessionID domain.SessionID) (SwitchResult, error) {
	if !m.beginSwitch(sessionID) {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrSwitchInProgress)
	}
	defer m.endSwitch(sessionID)

	rec, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindWorker {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrNotWorker)
	}
	if rec.IsTerminated {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrTerminated)
	}
	if rec.Metadata.WorkspacePath == "" {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrIncompleteHandle)
	}

	events, err := m.store.ListLifecycleLedger(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: list ledger: %w", sessionID, err)
	}
	recov, ok := findRecoverablePostStop(events)
	if !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrSwitchNothingToRecover)
	}

	var payload switchPayload
	if raw := strings.TrimSpace(recov.PostStop.PayloadJSON); raw != "" && raw != "{}" {
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: payload: %w", sessionID, err)
		}
	}
	if strings.TrimSpace(payload.Compiled) == "" {
		// Rebuild from semantic/observed if older entries only stored fragments.
		same := recov.PostStop.FromHarness == recov.PostStop.ToHarness || recov.Kind == domain.LifecycleKindFreshConversation
		compiled := handoff.Compile(handoff.CompileInput{
			Semantic:         payload.Semantic,
			Observed:         payload.Observed,
			RoleID:           recov.PostStop.RoleID,
			TargetGeneration: recov.GenerationID,
			SameHarness:      same,
			FromHarness:      recov.PostStop.FromHarness,
			ToHarness:        recov.PostStop.ToHarness,
		})
		payload.Compiled = compiled.Text
	}

	toHarness := recov.PostStop.ToHarness
	if toHarness == "" {
		toHarness = rec.Harness
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %q", sessionID, ErrUnknownHarness, toHarness)
	}
	if rec.Metadata.Role.RoleID != "" && !rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(toHarness); err != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %v", sessionID, ErrReadOnlyUnsupported, err)
		}
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, err)
	}

	// Best-effort destroy any stale runtime handle left from a crash mid-relaunch.
	if hid := strings.TrimSpace(rec.Metadata.RuntimeHandleID); hid != "" {
		handle := ports.RuntimeHandle{ID: hid}
		if alive, err := m.runtime.IsAlive(ctx, handle); err == nil && alive {
			_ = m.runtime.Destroy(ctx, handle)
		} else if err == nil && !alive {
			// already dead
		} else {
			// Probe failed: still try destroy (idempotent adapters).
			_ = m.runtime.Destroy(ctx, handle)
		}
	}

	// Re-apply target pin from durable post_stop (authoritative for recovery).
	fromHarness := recov.PostStop.FromHarness
	fromModel := recov.PostStop.FromModel
	toModel := recov.PostStop.ToModel
	roleID := recov.PostStop.RoleID
	if roleID == "" {
		roleID = rec.Metadata.Role.RoleID
	}

	rec.Harness = toHarness
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	if rec.Metadata.Role.RoleID != "" {
		rec.Metadata.Role.ResolvedHarness = toHarness
		if toModel != "" {
			rec.Metadata.Role.ResolvedModel = toModel
		}
	}
	// Inject compiled handoff once (avoid stacking on repeated recoveries).
	if payload.Compiled != "" && !strings.Contains(rec.Metadata.Prompt, "Host-compiled handoff") {
		// Strip a prior failed compose marker if present; otherwise prepend handoff.
		rec.Metadata.Prompt = composeSwitchPrompt(rec.Metadata.Prompt, payload.Compiled)
	} else if payload.Compiled != "" && !strings.Contains(rec.Metadata.Prompt, payload.Compiled) {
		// Handoff header present but text drifted — recompose from task tail if possible.
		rec.Metadata.Prompt = composeSwitchPrompt(stripCompiledHandoff(rec.Metadata.Prompt), payload.Compiled)
	}
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: persist: %v", sessionID, ErrSwitchPostStop, err)
	}

	ws := ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
		RepoPath:  rec.Metadata.WorkspaceRepoPath,
	}
	result, err := m.relaunchSession(ctx, "switch-recover", rec, project, ws, nil)
	if err != nil {
		_ = m.appendSwitchLedger(ctx, rec, recov.Kind, domain.LifecyclePhaseFailed, recov.GenerationID, fromHarness, toHarness, fromModel, toModel, roleID, "", "", recov.PostStop.PayloadJSON)
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %v", sessionID, ErrSwitchPostStop, err)
	}

	payloadBytes, _ := json.Marshal(payload)
	if err := m.appendSwitchLedger(ctx, result.Session, recov.Kind, domain.LifecyclePhaseTargetAck, recov.GenerationID, fromHarness, toHarness, fromModel, toModel, roleID, "", result.Session.Metadata.AgentSessionID, string(payloadBytes)); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: target ack ledger: %w", sessionID, err)
	}

	compiled := domain.CompiledHandoff{
		Text:             payload.Compiled,
		RoleID:           roleID,
		SourceGeneration: payload.Semantic.SourceGeneration,
		TargetGeneration: recov.GenerationID,
		Semantic:         payload.Semantic,
		Observed:         payload.Observed,
	}
	return SwitchResult{
		Session:      result.Session,
		Compiled:     compiled,
		GenerationID: recov.GenerationID,
		Kind:         recov.Kind,
		Mode:         result.Mode,
	}, nil
}

type recoverablePostStop struct {
	PostStop     domain.LifecycleLedgerRecord
	Kind         domain.LifecycleLedgerKind
	GenerationID string
}

func isSwitchLedgerKind(k domain.LifecycleLedgerKind) bool {
	return k == domain.LifecycleKindSwitch || k == domain.LifecycleKindFreshConversation
}

// findRecoverablePostStop returns the newest post_stop for a generation that
// never received target_ack. A later "failed" phase does not clear recoverability.
func findRecoverablePostStop(events []domain.LifecycleLedgerRecord) (recoverablePostStop, bool) {
	acked := map[string]bool{}
	for _, e := range events {
		if isSwitchLedgerKind(e.Kind) && e.Phase == domain.LifecyclePhaseTargetAck && e.GenerationID != "" {
			acked[e.GenerationID] = true
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if !isSwitchLedgerKind(e.Kind) || e.Phase != domain.LifecyclePhasePostStop {
			continue
		}
		if e.GenerationID == "" || acked[e.GenerationID] {
			continue
		}
		return recoverablePostStop{PostStop: e, Kind: e.Kind, GenerationID: e.GenerationID}, true
	}
	return recoverablePostStop{}, false
}

func (m *Manager) hasIncompletePostStop(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	events, err := m.store.ListLifecycleLedger(ctx, sessionID)
	if err != nil {
		return false, err
	}
	_, ok := findRecoverablePostStop(events)
	return ok, nil
}

// stripCompiledHandoff removes a leading host-compiled handoff section so recovery
// can re-inject a fresh compiled blob without stacking.
func stripCompiledHandoff(prompt string) string {
	const marker = "## Host-compiled handoff"
	const prior = "## Prior task prompt"
	p := strings.TrimSpace(prompt)
	if !strings.Contains(p, marker) {
		return p
	}
	if i := strings.Index(p, prior); i >= 0 {
		return strings.TrimSpace(p[i+len(prior):])
	}
	// No prior-task marker: drop everything from the handoff header.
	if i := strings.Index(p, marker); i >= 0 {
		return strings.TrimSpace(p[:i])
	}
	return p
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
