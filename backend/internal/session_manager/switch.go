package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
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
	// ForceGenerationID pins the saga's generation. Empty keeps today's
	// behaviour (the saga mints its own), so every existing caller is
	// unaffected.
	//
	// Manual failover (PHASE3B_MVP_CONTRACT section 6b) sets it because its
	// durable attempt row must carry the generation from its FIRST write. With
	// the saga minting it, the row is written empty and stamped afterwards --
	// a third durable write whose crash window can only be closed by guessing
	// which runtime belongs to which attempt, and a wrong guess there is a
	// second runtime.
	ForceGenerationID string
	// PauseIncidentID is set only by the operator-authorized manual Continue
	// saga. It permits that exact incident to drive its existing switch path
	// while every ordinary switch/fresh request remains forbidden under a pause.
	// The value is compared with the durable pin inside beginSwitch; it is never
	// exposed as a free-form switch API field.
	PauseIncidentID string
	// AdoptRoleID is manager-internal authorization for an unpinned legacy
	// orchestrator to adopt the project orchestrator role at this switch's
	// fenced relaunch boundary. SwitchOrchestrator recomputes it under the
	// project gate; callers cannot use it to select an arbitrary role.
	AdoptRoleID string
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
// SwitchWorker is the worker entry point. Orchestrators must arrive via
// FreshOrchestratorConversation, which takes the project ownership gate BEFORE
// the switch fence — entering here would take the locks in the wrong order.
//
// The kind check lives on the AUTHORITATIVE read inside the saga rather than on
// a pre-read here. A pre-read has to decide what to do when it fails, and every
// answer is wrong: proceeding admits an orchestrator with no gate, and refusing
// turns a transient store blip into a failed worker switch. Passing
// ownershipHeld through instead means the one read that already exists decides,
// and it cannot fail open.
func (m *Manager) SwitchWorker(ctx context.Context, req SwitchRequest) (SwitchResult, error) {
	return m.switchUnderOwnership(ctx, req, false)
}

// switchUnderOwnership is the saga proper. ownershipHeld asserts that the caller
// already holds whatever ownership this session's KIND requires: nothing for a
// worker, the project gate for an orchestrator. It is not a hint — an
// orchestrator reaching here without it is refused, because the alternative is
// running the saga with an inverted lock order.
func (m *Manager) switchUnderOwnership(ctx context.Context, req SwitchRequest, ownershipHeld bool) (SwitchResult, error) {
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
	if rec.Kind != domain.KindWorker && rec.Kind != domain.KindOrchestrator {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrNotWorker)
	}
	// A chat session must not enter this saga, and the refusal has to be its
	// own rather than a side effect.
	//
	// It was already refused, but only because the saga demands a runtime
	// handle a chat session has never had — the same precondition that had to
	// be exempted to make Restart work for chat. Correct by accident is not
	// correct: relaxing that precondition again, exactly as Restart needed,
	// would silently admit chat sessions to a saga that stops a tmux runtime,
	// probes it for liveness, and reads an empty handle as confirmed death.
	//
	// The saga can have chat when it can stop and recover a chat controller.
	// Until then this is a stated refusal, before anything is stopped.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrSwitchChatUnsupported)
	}
	// The OTHER saga. beginSwitch above excludes a second switch; it says
	// nothing about an interface transition, which stops and starts the same
	// session's controller from its own goroutine. Two sagas mutating one
	// session independently is how a source gets stopped twice, or a target
	// launched while the other is mid-flight — and a transition committing
	// mode=chat under a running switch also slips past the chat refusal above,
	// because that read happened before the commit.
	//
	// Checked HERE, under beginSwitch, and not before it: a check outside the
	// fence is a read the other saga can invalidate before this one acts.
	if active, err := m.hasActiveInterfaceTransition(ctx, req.SessionID); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: interface transition: %w", req.SessionID, err)
	} else if active {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrInterfaceTransitionInProgress)
	}
	if rec.Kind == domain.KindOrchestrator && !ownershipHeld {
		// Reached the worker entry point. Continuing would hold beginSwitch
		// without the project gate, so a concurrent EnsureOrchestrator(clean)
		// could retire and replace this session while its own saga stops and
		// relaunches it.
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrNotWorker)
	}
	if rec.IsTerminated {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrTerminated)
	}
	if rec.Metadata.Pause != nil && strings.TrimSpace(req.PauseIncidentID) != rec.Metadata.Pause.IncidentID {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, ErrSwitchPaused)
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
	if rec.Kind == domain.KindOrchestrator && sameHarness {
		// A distinct kind so recovery and audit can tell the two sagas apart
		// without re-reading the session: an orchestrator's recovery must run
		// under the project gate, and its handoff carries fleet state.
		kind = domain.LifecycleKindOrchestratorFresh
	}

	if err := m.requireSwitchCaps(fromHarness, toHarness, sameHarness); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %q", req.SessionID, ErrUnknownHarness, toHarness)
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}

	roleBinding := rec.Metadata.Role
	var adoptedRole roleApplyResult
	if adoptRoleID := strings.TrimSpace(req.AdoptRoleID); adoptRoleID != "" {
		if rec.Kind != domain.KindOrchestrator || strings.TrimSpace(roleBinding.RoleID) != "" {
			return SwitchResult{}, fmt.Errorf("switch %s: legacy role adoption is not applicable: %w", req.SessionID, domain.ErrSwitchTargetUnauthorized)
		}
		expectedRoleID, ok := domain.AdoptableOrchestratorRole(project.Config.RoleMap, rec.Harness)
		if !ok || expectedRoleID != adoptRoleID {
			return SwitchResult{}, fmt.Errorf("switch %s: legacy role %q is not adoptable: %w", req.SessionID, adoptRoleID, domain.ErrSwitchTargetUnauthorized)
		}
		roleCfg := ports.SpawnConfig{Kind: domain.KindOrchestrator, RoleID: adoptRoleID}
		adoptedRole, err = applyRoleMap(&roleCfg, project, m.dataDir)
		if err != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: adopt role: %w", req.SessionID, mapRoleError(err))
		}
		if !adoptedRole.Applied || roleCfg.Harness != rec.Harness {
			return SwitchResult{}, fmt.Errorf("switch %s: adopted role source mismatch: %w", req.SessionID, domain.ErrSwitchTargetUnauthorized)
		}
		if err := m.persistRoleTemplateArtifact(ctx, adoptedRole); err != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: persist adopted role template: %w", req.SessionID, err)
		}
		roleBinding = adoptedRole.Binding
	}
	if roleBinding.RoleID != "" && !roleBinding.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(toHarness); err != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: %w: %w", req.SessionID, ErrReadOnlyUnsupported, err)
		}
	}

	// Single generation for ledger + runtime launch. A caller may pin it (see
	// SwitchRequest.ForceGenerationID); empty falls through to the saga's own.
	targetGen := strings.TrimSpace(req.ForceGenerationID)
	if targetGen == "" {
		targetGen = m.newSwitchGeneration()
	}
	roleID := strings.TrimSpace(roleBinding.RoleID)
	fromModel := strings.TrimSpace(roleBinding.ResolvedModel)
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
	sourceGen := strings.TrimSpace(meta.RuntimeLaunchID)
	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseRequested, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", "{}"); err != nil {
		return SwitchResult{}, err
	}

	// Observe while source is still the live workspace owner; attribute to source gen.
	obs := handoff.ObserveWorkspace(ctx, handoff.ObserveInput{
		Worktree:     meta.WorkspacePath,
		GenerationID: sourceGen,
		Now:          m.clock(),
	})
	// An orchestrator additionally carries its fleet. Observed here, under the
	// project gate and before the source stops, for the same reason the git
	// observation is: it must describe the world the outgoing conversation
	// actually lived in.
	//
	// A read failure degrades rather than aborts. The fleet is context, not a
	// safety invariant — losing it makes the fresh conversation less informed,
	// whereas failing the switch strands an orchestrator that is already out of
	// context, which is the condition being remedied.
	var fleet *domain.ObservedOrchestratorV1
	if rec.Kind == domain.KindOrchestrator {
		observed, fleetErr := m.ObserveOrchestratorFleet(ctx, rec.ProjectID, sourceGen)
		if fleetErr != nil {
			m.logger.Warn("switch: fleet observation failed; handoff will omit it",
				"sessionID", rec.ID, "projectID", rec.ProjectID, "error", fleetErr)
		} else {
			fleet = &observed
		}
	}
	compiled := handoff.Compile(handoff.CompileInput{
		Semantic:             sem,
		Observed:             obs,
		ObservedOrchestrator: fleet,
		RoleID:               roleID,
		TargetGeneration:     targetGen,
		SameHarness:          sameHarness,
		FromHarness:          fromHarness,
		ToHarness:            toHarness,
	})
	payloadBytes, _ := json.Marshal(switchPayload{Semantic: sem, Observed: obs, Compiled: compiled.Text})
	payload := string(payloadBytes)

	if err := m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePreStop, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload); err != nil {
		return SwitchResult{}, err
	}

	// Snapshot exact pre-switch metadata for confirmed-alive rollback (pre-stop guarantee).
	preSwitchPrompt := meta.Prompt
	preSwitchAgentSession := meta.AgentSessionID
	preSwitchHandle := meta.RuntimeHandleID
	preSwitchLaunch := meta.RuntimeLaunchID
	preSwitchRole := meta.Role

	// Durable recoverable fence BEFORE destroying the source. If destroy succeeds
	// but a later persist fails, boot recovery can still re-drive from pending.
	pending := &domain.SwitchPending{
		GenerationID:          targetGen,
		Kind:                  kind,
		FromHarness:           fromHarness,
		ToHarness:             toHarness,
		FromModel:             fromModel,
		ToModel:               toModel,
		OriginalTask:          originalTask,
		RoleID:                roleID,
		PayloadJSON:           payload,
		SourceRuntimeHandleID: meta.RuntimeHandleID,
	}
	rec.Metadata.SwitchPending = pending
	if adoptedRole.Applied {
		rec.Metadata.Role = adoptedRole.Binding
	}
	// Compose prompt now so recovery never needs to rebuild from empty payload.
	rec.Metadata.Prompt = composeSwitchPrompt(originalTask, compiled.Text)
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: persist pending before destroy: %w", req.SessionID, err)
	}

	// Stop source with probe-driven transition (Destroy error ≠ source usable).
	sourceDead, err := m.destroyRuntimeProbed(ctx, meta.RuntimeHandleID)
	if err != nil {
		// Uncertain liveness: keep pending so recovery can probe/retry destroy.
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload)
		return SwitchResult{}, fmt.Errorf("switch %s: pre-stop: %w", req.SessionID, err)
	}
	if !sourceDead {
		// Confirmed alive: restore exact pre-switch usability (clear pending + prompt).
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, meta.AgentSessionID, "", payload)
		if rbErr := m.rollbackSwitchPending(ctx, rec, preSwitchPrompt, preSwitchAgentSession, preSwitchHandle, preSwitchLaunch, preSwitchRole); rbErr != nil {
			return SwitchResult{}, fmt.Errorf("switch %s: pre-stop source alive; rollback pending failed: %w: %w", req.SessionID, ErrSwitchUncertain, rbErr)
		}
		return SwitchResult{}, fmt.Errorf("switch %s: pre-stop: source runtime still alive after destroy", req.SessionID)
	}

	// Source dead: clear live handle/ids (pending still carries intent + payload).
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		// Source is dead with pending set — recovery can still continue.
		return SwitchResult{}, fmt.Errorf("switch %s: %w: clear source handle: %w", req.SessionID, ErrSwitchPostStop, err)
	}

	if err := m.ensurePostStopLedger(ctx, rec, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payload); err != nil {
		return SwitchResult{}, fmt.Errorf("switch %s: %w: %w", req.SessionID, ErrSwitchPostStop, err)
	}

	return m.finishSwitchTarget(ctx, rec, project, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payload, compiled)
}

// rollbackSwitchPending restores pre-switch prompt/handles and clears pending so
// a confirmed-alive source remains usable for user/terminal input.
func (m *Manager) rollbackSwitchPending(ctx context.Context, rec domain.SessionRecord, prompt, agentSession, handleID, launchID string, role domain.SessionRoleBinding) error {
	rec.Metadata.SwitchPending = nil
	rec.Metadata.Prompt = prompt
	rec.Metadata.AgentSessionID = agentSession
	rec.Metadata.RuntimeHandleID = handleID
	rec.Metadata.RuntimeLaunchID = launchID
	rec.Metadata.Role = role
	rec.UpdatedAt = m.clock()
	return m.store.UpdateSession(ctx, rec)
}

// ensurePostStopLedger idempotently records post_stop after source death and
// before target launch/ack. Required saga ordering.
func (m *Manager) ensurePostStopLedger(
	ctx context.Context,
	rec domain.SessionRecord,
	kind domain.LifecycleLedgerKind,
	gen string,
	from, to domain.AgentHarness,
	fromModel, toModel, roleID, payload string,
) error {
	return m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhasePostStop, gen, from, to, fromModel, toModel, roleID, "", "", payload)
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
	// Kind decides which locks this needs, so it is resolved before either is
	// taken. An orchestrator's recovery relaunches into the canonical worktree
	// and must hold the project gate for the same reason its saga does, and in
	// the same order: projectOwnership -> beginSwitch.
	pre, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrNotFound)
	}
	gated := pre.Kind == domain.KindOrchestrator
	if gated {
		release, gateErr := m.acquireProjectOwnership(ctx, pre.ProjectID)
		if gateErr != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, gateErr)
		}
		defer release()
	}

	if !m.beginSwitch(sessionID) {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrSwitchInProgress)
	}
	defer m.endSwitch(sessionID)

	// Re-read under both protections; the pre-gate read resolved kind only.
	rec, ok, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, err)
	}
	if !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrNotFound)
	}
	if rec.Kind != domain.KindWorker && rec.Kind != domain.KindOrchestrator {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrNotWorker)
	}
	// The pre-gate read decided whether to take the gate; this read is the
	// authoritative one. Checking only "is it a known kind" would be weaker than
	// the decision already made, so recovery would proceed ungated if the two
	// reads disagreed. Session kind is immutable in practice, which is exactly
	// why a disagreement means something is wrong rather than something changed.
	if (rec.Kind == domain.KindOrchestrator) != gated {
		return SwitchResult{}, fmt.Errorf(
			"recover switch %s: session kind changed under the gate decision (%s); refusing", sessionID, rec.Kind)
	}
	if rec.IsTerminated {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrTerminated)
	}
	// Recovery re-enters the saga, so it re-applies the saga's refusals — this
	// one included. Recovery rechecks kind, cross-harness orchestrator policy,
	// adapter availability and read-only support, and a chat row reaching here
	// would otherwise walk into terminal-oriented probing and finishSwitchTarget
	// with an empty runtime handle, which this saga reads as confirmed death.
	//
	// A chat+pending row should be unreachable now that both sagas share a
	// fence, but "should be unreachable" is what recovery exists to disbelieve:
	// its whole job is states nobody meant to create.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrSwitchChatUnsupported)
	}
	// The other saga, checked here for the same reason the interactive path
	// checks it: beginSwitch excludes a second SWITCH and says nothing about an
	// interface transition, which drives the same session's controller from its
	// own goroutine.
	//
	// Recovery reaches states the interactive path cannot. A row carrying BOTH
	// an incomplete switch and an active transition is unreachable now that the
	// two sagas share a fence, but it is exactly what a pre-fix, legacy or
	// hand-edited database can hold — and recovery exists to meet those. Before
	// the ledger is read or any runtime touched, because after either is too
	// late to be a refusal.
	if active, err := m.hasActiveInterfaceTransition(ctx, sessionID); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: interface transition: %w", sessionID, err)
	} else if active {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w", sessionID, ErrInterfaceTransitionInProgress)
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
		if pending.GenerationID == "corrupt" || pending.GenerationID == "__invalid__" {
			return SwitchResult{}, fmt.Errorf("recover switch %s: corrupt switch_pending_json; refuse auto recovery", sessionID)
		}
		kind = pending.Kind
		targetGen = pending.GenerationID
		fromHarness = pending.FromHarness
		toHarness = pending.ToHarness
		fromModel = pending.FromModel
		toModel = pending.ToModel
		roleID = pending.RoleID
		// Prefer payload staged on pending (survives post_stop append failure).
		if raw := strings.TrimSpace(pending.PayloadJSON); raw != "" && raw != "{}" {
			payloadRaw = raw
			var payload switchPayload
			if err := json.Unmarshal([]byte(raw), &payload); err == nil {
				compiledText = payload.Compiled
				sem = payload.Semantic
				obs = payload.Observed
			}
		}
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
		// Fall back to post_stop then pre_stop payload for older sagas / missing pending payload.
		if payloadRaw == "" {
			payloadRaw = recov.PostStop.PayloadJSON
		}
		if payloadRaw == "" {
			if pre := findPhasePayload(events, targetGen, domain.LifecyclePhasePreStop); pre != "" {
				payloadRaw = pre
			}
		}
		if compiledText == "" {
			var payload switchPayload
			if raw := strings.TrimSpace(payloadRaw); raw != "" && raw != "{}" {
				_ = json.Unmarshal([]byte(raw), &payload)
			}
			compiledText = payload.Compiled
			sem = payload.Semantic
			obs = payload.Observed
		}
	}
	// Also recover pre_stop-only when pending exists without post_stop (destroy
	// after pending persist, post_stop never written).
	if payloadRaw == "" && pending != nil {
		if pre := findPhasePayload(events, targetGen, domain.LifecyclePhasePreStop); pre != "" {
			payloadRaw = pre
			var payload switchPayload
			_ = json.Unmarshal([]byte(pre), &payload)
			compiledText = payload.Compiled
			sem = payload.Semantic
			obs = payload.Observed
		}
	}
	if toHarness == "" {
		return SwitchResult{}, fmt.Errorf("recover switch %s: missing target harness", sessionID)
	}
	// Recovery re-drives a durable target, but the pending pin is not by itself
	// authorization. Re-check the exact target against the current host role map
	// while the orchestrator project gate is held. Require the resolved model to
	// equal the durable model: recovery must never reinterpret an empty/default
	// target as a different fixed model.
	if rec.Kind == domain.KindOrchestrator && toHarness != rec.Harness {
		authorizedModel, _, authErr := m.authorizeOrchestratorSwitchTarget(ctx, rec, toHarness, toModel)
		if authErr != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: authorize target: %w", sessionID, authErr)
		}
		if authorizedModel != strings.TrimSpace(toModel) {
			return SwitchResult{}, fmt.Errorf("recover switch %s: durable target model %q resolves to %q: %w",
				sessionID, toModel, authorizedModel, domain.ErrSwitchTargetUnauthorized)
		}
	}
	if _, ok := m.agents.Agent(toHarness); !ok {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %q", sessionID, ErrUnknownHarness, toHarness)
	}
	// Revalidate RO before launching/acking a target (capability may have changed).
	if rec.Metadata.Role.RoleID != "" && !rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(toHarness); err != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w: %w", sessionID, ErrReadOnlyUnsupported, err)
		}
	}

	// Live runtime handling:
	// - matching target gen → ensure post_stop then ack only
	// - still the source handle (destroy never completed) → probe-destroy, ensure post_stop, then continue
	// - any other live gen → uncertain (never dual-launch)
	if hid := strings.TrimSpace(rec.Metadata.RuntimeHandleID); hid != "" {
		alive, probeErr := m.runtime.IsAlive(ctx, ports.RuntimeHandle{ID: hid})
		if probeErr != nil {
			return SwitchResult{}, fmt.Errorf("recover switch %s: %w: probe: %w", sessionID, ErrSwitchUncertain, probeErr)
		}
		if alive {
			if rec.Metadata.RuntimeLaunchID == targetGen {
				if err := m.ensurePostStopLedger(ctx, rec, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payloadRaw); err != nil {
					return SwitchResult{}, fmt.Errorf("recover switch %s: %w: post_stop before ack: %w", sessionID, ErrSwitchPostStop, err)
				}
				return m.ackLiveTarget(ctx, rec, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payloadRaw, compiledText, sem, obs)
			}
			// Retry source stop when pending recorded this handle as the pre-stop source.
			if pending != nil && pending.SourceRuntimeHandleID == hid {
				dead, dErr := m.destroyRuntimeProbed(ctx, hid)
				if dErr != nil {
					return SwitchResult{}, fmt.Errorf("recover switch %s: %w: source still live: %w", sessionID, ErrSwitchUncertain, dErr)
				}
				if !dead {
					// Split from the error case on purpose: destroy returning
					// (false, nil) is "the probe says it is still there", and
					// folding it into the branch above rendered a cause of
					// <nil> — and would render %!w(<nil>) once wrapped.
					return SwitchResult{}, fmt.Errorf("recover switch %s: %w: source still live after destroy reported no error",
						sessionID, ErrSwitchUncertain)
				}
				// Source now dead; fall through to post_stop + relaunch.
			} else {
				return SwitchResult{}, fmt.Errorf("recover switch %s: %w: live runtime %q blocks relaunch (gen %q want %q)",
					sessionID, ErrSwitchUncertain, hid, rec.Metadata.RuntimeLaunchID, targetGen)
			}
		}
		// Dead handle (or just destroyed source): clear and relaunch.
		rec.Metadata.RuntimeHandleID = ""
		rec.Metadata.RuntimeLaunchID = ""
	}

	// Source is dead (or never had a handle). Require durable post_stop before target launch.
	if err := m.ensurePostStopLedger(ctx, rec, kind, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, payloadRaw); err != nil {
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: post_stop before launch: %w", sessionID, ErrSwitchPostStop, err)
	}

	if compiledText == "" && (sem.Objective != "" || obs.Head != "" || obs.Branch != "") {
		// Only recompile when we have real semantic/observed facts — never replace
		// a valid composed prompt with an empty handoff.
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
			PayloadJSON: payloadRaw,
		}
	}
	if strings.TrimSpace(pending.PayloadJSON) == "" && payloadRaw != "" {
		pending.PayloadJSON = payloadRaw
	}
	rec.Metadata.SwitchPending = pending
	rec.Metadata.AgentSessionID = ""
	// Prefer existing composed prompt when it already contains the handoff;
	// only recompose when we have compiled text and the prompt is missing it.
	if compiledText != "" {
		if !strings.Contains(rec.Metadata.Prompt, "Host-compiled handoff") {
			rec.Metadata.Prompt = composeSwitchPrompt(original, compiledText)
		}
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
		ForceFresh:    true,
		RoleModel:     toModel,
		// Post-stop recovery below refuses a terminated session, so the launch
		// rollback must not terminate it; the ErrSwitchPostStop path here is the
		// documented owner of this state.
		KeepSessionOnLaunchFailure: true,
	})
	if err != nil {
		_ = m.appendSwitchLedger(ctx, rec, kind, domain.LifecyclePhaseFailed, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", "", payload)
		// Joined, not "%w: %v": the relaunch error may carry
		// ErrLaunchCleanupUnresolved, and formatting it with %v keeps only the
		// text — boot would then be unable to tell a recoverable post-stop from
		// one that also left a runtime executing.
		return SwitchResult{}, fmt.Errorf("switch %s: %w", rec.ID, errors.Join(ErrSwitchPostStop, err))
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
		return SwitchResult{}, fmt.Errorf("switch %s: %w: re-pin pending after launch: %w", rec.ID, ErrSwitchPostStop, err)
	}

	if err := m.appendSwitchLedger(ctx, live, kind, domain.LifecyclePhaseTargetAck, targetGen, fromHarness, toHarness, fromModel, toModel, roleID, "", live.Metadata.AgentSessionID, payload); err != nil {
		// Target is live but ack not durable — keep pending so input stays gated
		// and recovery only retries ack (alive gen match).
		return SwitchResult{}, fmt.Errorf("switch %s: %w: target ack ledger: %w", rec.ID, ErrSwitchPostStop, err)
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
		// target_ack is already durable and the target runtime is live. Preserve
		// the typed post-stop recovery contract so callers do not report an opaque
		// 500 for a state the next explicit recovery can finish safely.
		return SwitchResult{}, fmt.Errorf("switch %s: %w: promote after ack: %w", rec.ID, ErrSwitchPostStop, err)
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
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: ack: %w", rec.ID, ErrSwitchPostStop, err)
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
		return SwitchResult{}, fmt.Errorf("recover switch %s: %w: promote after ack: %w", rec.ID, ErrSwitchPostStop, err)
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

// destroyRuntimeProbed destroys a handle and reports probe-based liveness.
// Returns (true, nil) when confirmed dead, (false, nil) when confirmed alive,
// and (false, ErrSwitchUncertain) when the probe cannot establish reality.
// Destroy errors alone never imply survival or death.
func (m *Manager) destroyRuntimeProbed(ctx context.Context, handleID string) (dead bool, err error) {
	handleID = strings.TrimSpace(handleID)
	if handleID == "" {
		return true, nil
	}
	handle := ports.RuntimeHandle{ID: handleID}
	_ = m.runtime.Destroy(ctx, handle) // best-effort; probe is authoritative
	alive, probeErr := m.runtime.IsAlive(ctx, handle)
	if probeErr != nil {
		// The adapter keeps server absence as an error so board probes remain
		// inconclusive. Here the source teardown already targeted this handle,
		// and an absent server proves its pane cannot have survived.
		if errors.Is(probeErr, ports.ErrRuntimeServerAbsent) {
			return true, nil
		}
		return false, fmt.Errorf("%w: probe after destroy: %w", ErrSwitchUncertain, probeErr)
	}
	if alive {
		return false, nil // confirmed alive — caller rolls back pending
	}
	return true, nil // confirmed dead
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

// isSwitchLedgerKind selects the ledger kinds the post_stop recovery machinery
// understands. It must list EVERY kind the switch saga can append, or the
// ledger fallback silently stops working for that kind: findPhasePayload,
// findRecoverablePostStop and hasIncompletePostStop all filter through here, so
// an omitted kind makes recovery answer ErrSwitchNothingToRecover for a session
// that has a real unacknowledged post_stop — and the fallback exists precisely
// for when the pending pin is gone.
func isSwitchLedgerKind(k domain.LifecycleLedgerKind) bool {
	return k == domain.LifecycleKindSwitch ||
		k == domain.LifecycleKindFreshConversation ||
		k == domain.LifecycleKindOrchestratorFresh
}

func findPhasePayload(events []domain.LifecycleLedgerRecord, gen string, phase domain.LifecycleLedgerPhase) string {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.GenerationID == gen && e.Phase == phase && isSwitchLedgerKind(e.Kind) {
			return e.PayloadJSON
		}
	}
	return ""
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
