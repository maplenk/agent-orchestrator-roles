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
	// TargetModel optional. Cross-harness empty → provider default (not source model).
	// Same-harness empty → retain source model.
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

// switchPayload is the durable post_stop handoff blob (JSON).
type switchPayload struct {
	Semantic domain.SemanticHandoffV1   `json:"semantic"`
	Observed domain.ObservedWorkspaceV1 `json:"observed"`
	Compiled string                     `json:"compiled"`
}

// SwitchWorker runs the #3548-class worker switch / fresh-conversation saga.
// Current Harness/Role remain the source until durable target_ack promotes the
// pending pin. Generation ID is the launched RuntimeLaunchID.
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
	if rec.Metadata.SwitchPending != nil {
		// In-flight saga: only recovery may continue (do not start a nested switch).
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrSwitchInProgress)
	}
	meta := rec.Metadata
	if meta.WorkspacePath == "" || meta.RuntimeHandleID == "" {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrIncompleteHandle)
	}

	fromHarness := rec.Harness
	toHarness, kind, sameHarness := resolveSwitchTarget(req, fromHarness)

	if err := m.requireSwitchCaps(fromHarness, toHarness, sameHarness); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %q", req.SessionID, ErrUnknownHarness, toHarness)
	}
	if rec.Metadata.Role.RoleID != "" && !rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(toHarness); err != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: %w: %v", req.SessionID, ErrReadOnlyUnsupported, err)
		}
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}

	// Single generation for ledger + runtime launch.
	targetGen := m.newSwitchGeneration()
	roleID := strings.TrimSpace(meta.Role.RoleID)
	fromModel := strings.TrimSpace(meta.Role.ResolvedModel)
	toModel := resolveTargetModel(req.TargetModel, fromModel, sameHarness)

	sem := req.Semantic
	if sem.SchemaVersion == 0 {
		sem.SchemaVersion = domain.SemanticHandoffSchemaVersion
	}
	if sem.SourceGeneration == "" {
		sem.SourceGeneration = strings.TrimSpace(meta.RuntimeLaunchID)
	}
	if sem.NativeSessionID == "" {
		sem.NativeSessionID = meta.AgentSessionID
	}

	originalTask := originalTaskPrompt(meta)
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseRequested, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", "{}"); err != nil {
		return SwitchResult{}, err
	}

	obs := handoff.ObserveWorkspace(ctx, handoff.ObserveInput{
		Worktree:     meta.WorkspacePath,
		GenerationID: targetGen,
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
	payloadBytes, _ := json.Marshal(switchPayload{Semantic: sem, Observed: obs, Compiled: compiled.Text})
	payload := string(payloadBytes)

	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePreStop, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload); err != nil {
		return SwitchResult{}, err
	}

	// Stop source with probe-driven transition (Destroy error ≠ source usable).
	sourceDead, err := m.destroyRuntimeProbed(ctx, meta.RuntimeHandleID)
	if err != nil {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload)
		return SwitchResult{}, fmt.Errorf("switch %s: pre-stop: %w", req.SessionID, err)
	}
	if !sourceDead {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload)
		return SwitchResult{}, fmt.Errorf("switch %s: pre-stop: source runtime still alive after destroy", req.SessionID)
	}

	// Stage pending target WITHOUT promoting current harness/model.
	pending := &domain.SwitchPending{
		GenerationID: targetGen,
		Kind:         kind,
		FromHarness:  fromHarness,
		ToHarness:    toHarness,
		FromModel:    fromModel,
		ToModel:      toModel,
		OriginalTask: originalTask,
		RoleID:       roleID,
	}
	rec.Metadata.SwitchPending = pending
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.Prompt = composeSwitchPrompt(originalTask, compiled.Text)
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: persist pending: %w", req.SessionID, err)
	}

	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePostStop, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", "", payload); err != nil {
		// Source is dead; leave pending for recovery even if this append fails.
		return SwitchResult{}, fmt.Errorf("switch %s: %w: post_stop ledger: %v", req.SessionID, ErrSwitchPostStop, err)
	}

	return m.finishSwitchTarget(ctx, rec, project, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payload, compiled)
}

// FreshConversation is same-harness switch with a new native session + handoff.
func (m *Manager) FreshConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (SwitchResult, error) {
	return m.SwitchWorker(ctx, SwitchRequest{
		SessionID:         sessionID,
		Semantic:          semantic,
		FreshConversation: true,
	})
}

// RecoverSwitchFromPostStop re-drives incomplete post_stop (or pending pin).
// Never launches a second target while a runtime with the pending generation is live.
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
	pending := rec.Metadata.SwitchPending
	if !ok && pending == nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrSwitchNothingToRecover)
	}

	// Prefer durable pending pin; fall back to post_stop ledger row.
	var (
		kind         domain.LifecycleLedgerKind
		targetGen    string
		fromHarness  domain.AgentHarness
		toHarness    domain.AgentHarness
		fromModel    string
		toModel      string
		roleID       string
		payloadRaw   string
		compiledText string
		sem          domain.SemanticHandoffV1
		obs          domain.ObservedWorkspaceV1
	)
	if pending != nil {
		kind = pending.Kind
		targetGen = pending.GenerationID
		fromHarness = pending.FromHarness
		toHarness = pending.ToHarness
		fromModel = pending.FromModel
		toModel = pending.ToModel
		roleID = pending.RoleID
	}
	if ok {
		if targetGen == "" {
			targetGen = recov.GenerationID
		}
		if kind == "" {
			kind = recov.Kind
		}
		if fromHarness == "" {
			fromHarness = recov.PostStop.FromHarness
		}
		if toHarness == "" {
			toHarness = recov.PostStop.ToHarness
		}
		if fromModel == "" {
			fromModel = recov.PostStop.FromModel
		}
		if toModel == "" {
			toModel = recov.PostStop.ToModel
		}
		if roleID == "" {
			roleID = recov.PostStop.RoleID
		}
		payloadRaw = recov.PostStop.PayloadJSON
		var payload switchPayload
		if raw := strings.TrimSpace(payloadRaw); raw != "" && raw != "{}" {
			_ = json.Unmarshal([]byte(raw), &payload)
		}
		compiledText = payload.Compiled
		sem = payload.Semantic
		obs = payload.Observed
	}
	if toHarness == "" {
		return SwitchResult{}, fmt.Errorf("recover switch %s: missing target harness", sessionID)
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %q", sessionID, ErrUnknownHarness, toHarness)
	}

	// Live target for this generation: only promote ack, do not double-launch.
	// A live runtime with a different generation is an uncertain ownership state —
	// never clear the handle and relaunch (would create dual input owners).
	if hid := strings.TrimSpace(rec.Metadata.RuntimeHandleID); hid != "" {
		alive, probeErr := m.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: hid})
		if probeErr != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w: probe: %v", sessionID, ErrSwitchUncertain, probeErr)
		}
		if alive {
			if rec.Metadata.RuntimeLaunchID == targetGen {
				return m.ackLiveTarget(ctx, rec, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payloadRaw, compiledText, sem, obs)
			}
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w: live runtime %q blocks relaunch (gen %q want %q)",
				sessionID, ErrSwitchUncertain, hid, rec.Metadata.RuntimeLaunchID, targetGen)
		}
		// Dead handle: clear and relaunch.
		rec.Metadata.RuntimeHandleID = ""
		rec.Metadata.RuntimeLaunchID = ""
	}

	if compiledText == "" {
		same := fromHarness == toHarness || kind == domain.LifecycleKindFreshConversation
		c := handoff.Compile(handoff.CompileInput{
			Semantic: sem, Observed: obs, RoleID: roleID, TargetGeneration: targetGen,
			SameHarness: same, FromHarness: fromHarness, ToHarness: toHarness,
		})
		compiledText = c.Text
	}
	original := ""
	if pending != nil {
		original = pending.OriginalTask
	}
	if original == "" {
		original = originalTaskPrompt(rec.Metadata)
	}
	if pending == nil {
		pending = &domain.SwitchPending{
			GenerationID: targetGen, Kind: kind, FromHarness: fromHarness, ToHarness: toHarness,
			FromModel: fromModel, ToModel: toModel, OriginalTask: original, RoleID: roleID,
		}
	}
	rec.Metadata.SwitchPending = pending
	rec.Metadata.AgentSessionID = ""
	if !strings.Contains(rec.Metadata.Prompt, "Host-compiled handoff") || !strings.Contains(rec.Metadata.Prompt, compiledText) {
		rec.Metadata.Prompt = composeSwitchPrompt(original, compiledText)
	}
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: persist: %w", sessionID, err)
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, err)
	}
	payloadBytes, _ := json.Marshal(switchPayload{Semantic: sem, Observed: obs, Compiled: compiledText})
	payload := string(payloadBytes)
	if payloadRaw != "" {
		payload = payloadRaw
	}
	compiled := domain.CompiledHandoff{
		Text: compiledText, RoleID: roleID, SourceGeneration: sem.SourceGeneration,
		TargetGeneration: targetGen, Semantic: sem, Observed: obs,
	}
	return m.finishSwitchTarget(ctx, rec, project, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payload, compiled)
}

// finishSwitchTarget launches the pending target with forced generation and
// promotes harness only after durable target_ack.
func (m *Manager) finishSwitchTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	project domain.ProjectRecord,
	kind domain.LifecycleLedgerKind,
	targetGen string,
	fromHarness, toHarness domain.AgentHarness,
	fromModel, toModel, roleID, payload string,
	compiled domain.CompiledHandoff,
) (SwitchResult, error) {
	ws := ports.WorkspaceInfo{
		Path: rec.Metadata.WorkspacePath, Branch: rec.Metadata.Branch,
		SessionID: rec.ID, ProjectID: rec.ProjectID, RepoPath: rec.Metadata.WorkspaceRepoPath,
	}
	// Launch using pending target harness; do not change rec.Harness yet.
	result, err := m.relaunchSession(ctx, "switch", rec, project, ws, nil, relaunchOpts{
		LaunchHarness: toHarness,
		ForceLaunchID: targetGen,
		RoleModel:     toModel,
	})
	if err != nil {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", "", payload)
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %v", rec.ID, ErrSwitchPostStop, err)
	}
	if result.Session.Metadata.RuntimeLaunchID != targetGen {
		// Hard invariant: generation must match. Stop target and leave pending.
		if hid := result.Session.Metadata.RuntimeHandleID; hid != "" {
			_ = m.runtime.Destroy(ctx, ports.RuntimeHandle{ID: hid})
		}
		return SwitchResult{}, fmt.Errorf("switch %s: %w: runtime gen %q != ledger gen %q",
			rec.ID, ErrSwitchPostStop, result.Session.Metadata.RuntimeLaunchID, targetGen)
	}

	// Re-load and promote only after durable ack.
	live := result.Session
	// Preserve pending until ack succeeds.
	live.Metadata.SwitchPending = rec.Metadata.SwitchPending
	if live.Metadata.SwitchPending == nil {
		live.Metadata.SwitchPending = &domain.SwitchPending{
			GenerationID: targetGen, Kind: kind, FromHarness: fromHarness, ToHarness: toHarness,
			FromModel: fromModel, ToModel: toModel, RoleID: roleID,
		}
	}
	if err := m.store.UpdateSession(ctx, live); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: re-pin pending after launch: %v", rec.ID, ErrSwitchPostStop, err)
	}

	if err := m.appendSwitchLedger(ctx, live, kind, domain.LifecyclePhaseTargetAck, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", live.Metadata.AgentSessionID, payload); err != nil {
		// Target is live but ack not durable — keep pending so input stays gated
		// and recovery only retries ack (alive gen match).
		return SwitchResult{}, fmt.Errorf("switch %s: %w: target ack ledger: %v", rec.ID, ErrSwitchPostStop, err)
	}

	// Promote current identity only after durable ack.
	live.Harness = toHarness
	if live.Metadata.Role.RoleID != "" || roleID != "" {
		if live.Metadata.Role.RoleID == "" {
			live.Metadata.Role.RoleID = roleID
		}
		live.Metadata.Role.ResolvedHarness = toHarness
		live.Metadata.Role.ResolvedModel = toModel
	}
	live.Metadata.SwitchPending = nil
	live.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, live); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: promote after ack: %w", rec.ID, err)
	}

	return SwitchResult{
		Session:      live,
		Compiled:     compiled,
		GenerationID: targetGen,
		Kind:         kind,
		Mode:         result.Mode,
	}, nil
}

func (m *Manager) ackLiveTarget(
	ctx context.Context,
	rec domain.SessionRecord,
	kind domain.LifecycleLedgerKind,
	targetGen string,
	fromHarness, toHarness domain.AgentHarness,
	fromModel, toModel, roleID, payload, compiledText string,
	sem domain.SemanticHandoffV1,
	obs domain.ObservedWorkspaceV1,
) (SwitchResult, error) {
	if payload == "" {
		b, _ := json.Marshal(switchPayload{Semantic: sem, Observed: obs, Compiled: compiledText})
		payload = string(b)
	}
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseTargetAck, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", rec.Metadata.AgentSessionID, payload); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: ack: %v", rec.ID, ErrSwitchPostStop, err)
	}
	rec.Harness = toHarness
	if rec.Metadata.Role.RoleID != "" || roleID != "" {
		if rec.Metadata.Role.RoleID == "" {
			rec.Metadata.Role.RoleID = roleID
		}
		rec.Metadata.Role.ResolvedHarness = toHarness
		rec.Metadata.Role.ResolvedModel = toModel
	}
	rec.Metadata.SwitchPending = nil
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: promote: %w", rec.ID, err)
	}
	return SwitchResult{
		Session: rec,
		Compiled: domain.CompiledHandoff{
			Text: compiledText, RoleID: roleID, TargetGeneration: targetGen,
			Semantic: sem, Observed: obs,
		},
		GenerationID: targetGen,
		Kind:         kind,
		Mode:         RestoreModeFresh,
	}, nil
}

// destroyRuntimeProbed destroys a handle and returns whether the runtime is
// confirmed dead. Destroy errors alone do not imply survival.
func (m *Manager) destroyRuntimeProbed(ctx context.Context, handleID string) (dead bool, err error) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return true, nil
	}
	handle := ports.RuntimeHandle{ID: handleID}
	destroyErr := m.runtime.Destroy(ctx, handle)
	alive, probeErr := m.runtime.IsAlive(ctx, handle)
	if probeErr != nil {
		if destroyErr != nil {
			return false, fmt.Errorf("%w: destroy=%v probe=%v", ErrSwitchUncertain, destroyErr, probeErr)
		}
		return false, fmt.Errorf("%w: probe after destroy: %w", ErrSwitchUncertain, probeErr)
	}
	if alive {
		if destroyErr != nil {
			return false, fmt.Errorf("destroy: %w (still alive)", destroyErr)
		}
		return false, fmt.Errorf("runtime still alive after destroy")
	}
	// Confirmed dead — Destroy error is informational only.
	return true, nil
}

func resolveSwitchTarget(req SwitchRequest, fromHarness domain.AgentHarness) (to domain.AgentHarness, kind domain.LifecycleLedgerKind, same bool) {
	to = req.TargetHarness
	kind = domain.LifecycleKindSwitch
	if req.FreshConversation {
		return fromHarness, domain.LifecycleKindFreshConversation, true
	}
	if to == "" || to == fromHarness {
		return fromHarness, domain.LifecycleKindFreshConversation, true
	}
	return to, kind, false
}

func resolveTargetModel(reqModel, fromModel string, sameHarness bool) string {
	reqModel = strings.TrimSpace(reqModel)
	if reqModel != "" {
		return reqModel
	}
	if sameHarness {
		return strings.TrimSpace(fromModel)
	}
	// Cross-harness: never leak source provider model IDs.
	return ""
}

func originalTaskPrompt(meta domain.SessionMetadata) string {
	if meta.SwitchPending != nil && strings.TrimSpace(meta.SwitchPending.OriginalTask) != "" {
		return meta.SwitchPending.OriginalTask
	}
	return stripCompiledHandoff(meta.Prompt)
}

func (m *Manager) requireSwitchCaps(from, to domain.AgentHarness, sameHarness bool) error {
	fc := m.switchCaps(from)
	tc := m.switchCaps(to)
	if !fc.SpawnSupported || !tc.SpawnSupported {
		return fmt.Errorf("%w: spawn_supported required", ErrSwitchNotSupported)
	}
	if !fc.SwitchSupported {
		return fmt.Errorf("%w: source %q", ErrSwitchNotSupported, from)
	}
	if !tc.SwitchSupported {
		return fmt.Errorf("%w: target %q", ErrSwitchNotSupported, to)
	}
	_ = sameHarness
	return nil
}

func (m *Manager) switchCaps(h domain.AgentHarness) capabilities.Caps {
	if m.switchCapsOverride != nil {
		return m.switchCapsOverride(h)
	}
	return capabilities.For(h)
}

func (m *Manager) beginSwitch(id domain.SessionID) bool {
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	if _, exists := m.switching[id]; exists {
		return false
	}
	if _, exists := m.resuming[id]; exists {
		return false
	}
	m.switching[id] = struct{}{}
	return true
}

func (m *Manager) endSwitch(id domain.SessionID) {
	m.ownershipMu.Lock()
	delete(m.switching, id)
	m.ownershipMu.Unlock()
}

func (m *Manager) newSwitchGeneration() string {
	if m.newLaunchID != nil {
		id := m.newLaunchID()
		if strings.TrimSpace(id) != "" {
			return id
		}
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
	// Stable idempotent id: one row per session/generation/phase.
	id := fmt.Sprintf("%s:%s:%s", rec.ID, gen, phase)
	// Skip if already present (retry-safe).
	if events, err := m.store.ListLifecycleLedger(ctx, rec.ID); err == nil {
		for _, e := range events {
			if e.ID == id || (e.GenerationID == gen && e.Phase == phase && isSwitchLedgerKind(e.Kind)) {
				return nil
			}
		}
	}
	err := m.store.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
		ID: id, SessionID: rec.ID, ProjectID: rec.ProjectID,
		Kind: kind, Phase: phase, GenerationID: gen,
		FromHarness: from, ToHarness: to, FromModel: fromModel, ToModel: toModel,
		RoleID: roleID, SourceNativeSessionID: sourceNative, TargetNativeSessionID: targetNative,
		PayloadJSON: payload, CreatedAt: m.clock(),
	})
	if err != nil {
		return fmt.Errorf("lifecycle ledger %s/%s: %w", kind, phase, err)
	}
	return nil
}

func composeSwitchPrompt(priorTask, compiled string) string {
	compiled = strings.TrimSpace(compiled)
	priorTask = strings.TrimSpace(stripCompiledHandoff(priorTask))
	switch {
	case compiled == "":
		return priorTask
	case priorTask == "":
		return compiled
	default:
		return compiled + "\n\n## Prior task prompt\n" + priorTask
	}
}

type recoverablePostStop struct {
	PostStop     domain.LifecycleLedgerRecord
	Kind         domain.LifecycleLedgerKind
	GenerationID string
}

func isSwitchLedgerKind(k domain.LifecycleLedgerKind) bool {
	return k == domain.LifecycleKindSwitch || k == domain.LifecycleKindFreshConversation
}

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
	rec, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if ok && rec.Metadata.SwitchPending != nil && rec.Metadata.SwitchPending.GenerationID != "" {
		return true, nil
	}
	events, err := m.store.ListLifecycleLedger(ctx, sessionID)
	if err != nil {
		return false, err
	}
	_, found := findRecoverablePostStop(events)
	return found, nil
}

// stripCompiledHandoff removes a leading host-compiled handoff section so
// refresh/switch does not nest stale authoritative facts.
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
	if i := strings.Index(p, marker); i >= 0 {
		return strings.TrimSpace(p[:i])
	}
	return p
}
