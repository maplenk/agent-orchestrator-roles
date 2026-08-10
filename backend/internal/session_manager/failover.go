package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Phase 3B failover (PHASE3B_MVP_CONTRACT sections 6, 6a, 6b).
//
// Continue is the only one of the three pause controls that changes harness or
// model, and it never changes role_id. It does not open a relaunch path of its
// own: it drives the switch saga that already exists, pinning the generation it
// minted so the durable attempt row and the runtime agree by identity rather
// than by inference.
//
// The shape of this file is set by one rule from section 6a: terminal-vs-
// recoverable is decided by whether the SOURCE WAS STOPPED, not by whether the
// call returned an error. A failure after the source stops is finished by the
// saga that already exists, so marking it terminal is what produced a `failed`
// attempt later reaching target_ack, and a Continue that opened a second
// runtime over an unrecovered switch.

// failoverAttemptStore is the narrow durable surface Continue needs.
//
// Declared here and type-asserted from m.store rather than added to the Store
// interface, which is the existing precedent (interface_transition.go does the
// same with interfaceTransitionStore) and which avoids editing the 7000-line
// fakeStore in manager_test.go that another agent is also working in.
//
// Unlike the interface-transition assertion, a missing implementation is NOT a
// silent degrade: Continue returns ErrFailoverNotWired, because a continuation
// that cannot record an attempt cannot be made idempotent, and an idempotence
// check that silently always says "no prior attempt" is how one incident spends
// every rung on the ladder.
type failoverAttemptStore interface {
	AppendSessionFailoverAttemptWithLedger(ctx context.Context, attempt domain.FailoverAttempt, ledger domain.LifecycleLedgerRecord) error
	ListSessionFailoverAttemptsByIncident(ctx context.Context, sessionID domain.SessionID, incidentID string) ([]domain.FailoverAttempt, error)
	ListSessionFailoverAttemptsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.FailoverAttempt, error)
	UpdateSessionFailoverAttemptState(ctx context.Context, attemptID string, from, to domain.FailoverAttemptState, updatedAt time.Time) (bool, error)
}

// activeAgentSwitchReader is deliberately narrower than ports.AgentSwitchStore:
// failover only needs the durable reservation fact. Keeping the read optional
// preserves focused manager fakes while the production SQLite store supplies
// it. A nonterminal canonical agent-switch saga owns the session and its target
// runtime, so the legacy failover path must not insert an attempt or spend a
// ladder rung while that ownership is unresolved.
type activeAgentSwitchReader interface {
	GetActiveAgentSwitch(context.Context, domain.SessionID) (domain.AgentSwitch, bool, error)
}

func (m *Manager) failoverStore() (failoverAttemptStore, error) {
	s, ok := m.store.(failoverAttemptStore)
	if !ok {
		return nil, ErrFailoverNotWired
	}
	return s, nil
}

// ContinueFailover moves a paused, role-pinned worker to the next authorized
// failover rung and lifts the pause once the target acks.
//
// The order of operations is contract section 6 and is not negotiable. In
// particular every refusal below happens BEFORE anything durable is written, so
// a refused Continue leaves no ledger row, no attempt row and no runtime change
// to explain later.
func (m *Manager) ContinueFailover(
	ctx context.Context,
	id domain.SessionID,
	req ContinueFailoverRequest,
) (ContinueFailoverResult, error) {
	return m.continueFailover(ctx, id, req, false)
}

// continueFailover is the single manual/automatic saga entry. Automatic is a
// trigger policy only: it may select the first rung for an incident, but every
// durable and runtime transition below is the accepted Continue transaction.
func (m *Manager) continueFailover(
	ctx context.Context,
	id domain.SessionID,
	req ContinueFailoverRequest,
	automatic bool,
) (ContinueFailoverResult, error) {
	incident := strings.TrimSpace(req.IncidentID)
	if err := domain.ValidateIncidentID(incident); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: %w", id, ErrIncidentRequired, err)
	}
	store, err := m.failoverStore()
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, err)
	}

	// Lazy crash reconciliation, at an entry point this file owns. Boot's
	// pausedSkip already excludes paused sessions from automatic post_stop
	// recovery, so without this an attempt interrupted by a crash would still
	// read as in-flight here. Exported as ReconcileFailoverAttempts so boot can
	// call it too, without this file reaching into manager.go's reconcile
	// region.
	if err := m.ReconcileFailoverAttempts(ctx, id); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: reconcile: %w", id, err)
	}

	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read session: %w", id, err)
	}
	if !ok {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, ErrNotFound)
	}
	if err := failoverEligible(rec); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, err)
	}

	// The pin is checked against the CALLER's incident and is never re-read as
	// authority: an action raised for incident A landing after B replaced it
	// must fail, not continue B on evidence nobody looked at.
	paused := rec.Metadata.Pause
	if paused == nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, ErrNotPaused)
	}
	if paused.IncidentID != incident {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: holding incident is %s, not %s",
			id, ErrIncidentMismatch, paused.IncidentID, incident)
	}

	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	if roleID == "" {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, domain.ErrFailoverRoleRequired)
	}

	var project domain.ProjectRecord
	var automaticSource domain.AgentHarness
	if automatic {
		var enabled bool
		project, automaticSource, enabled, err = m.automaticFailoverPolicy(ctx, rec)
		if err != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: automatic policy: %w", id, err)
		}
		if !enabled {
			return ContinueFailoverResult{}, errAutomaticFailoverDisabled
		}
	}

	attempts, err := store.ListSessionFailoverAttemptsByIncident(ctx, id, incident)
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read attempts: %w", id, err)
	}
	var activeAgentSwitch domain.AgentSwitch
	var hasActiveAgentSwitch bool
	if reader, ok := m.store.(activeAgentSwitchReader); ok {
		activeAgentSwitch, hasActiveAgentSwitch, err = reader.GetActiveAgentSwitch(ctx, id)
		if err != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: read active agent switch: %w", id, err)
		}
	}
	_, canonicalAgentSwitchEngine := m.store.(ports.AgentSwitchStore)

	// Idempotence, BEFORE anything durable (contract section 6 rule 5). Keyed on
	// Terminal() via ActiveFailoverAttempt, not on == requested: a post_stop is
	// non-terminal, and failing to adopt one is how a duplicate Continue opens a
	// second runtime over a source that is already stopped.
	if active, ok := domain.ActiveFailoverAttempt(attempts); ok {
		if automatic {
			if active.FromHarness != automaticSource || rec.Harness != automaticSource ||
				!m.automaticFailoverHarnessSupported(active.FromHarness) ||
				!m.automaticFailoverHarnessSupported(active.ToHarness) {
				return ContinueFailoverResult{}, errAutomaticFailoverDisabled
			}
		}
		if hasActiveAgentSwitch && !agentSwitchMatchesFailoverAttempt(activeAgentSwitch, active) {
			return ContinueFailoverResult{}, fmt.Errorf(
				"continue %s: %w: active agent switch %s does not own failover attempt %s",
				id, ErrFailoverRecoveryRequired, activeAgentSwitch.ID, active.ID)
		}
		if canonicalAgentSwitchEngine &&
			(hasActiveAgentSwitch || strings.TrimSpace(active.RoleSnapshot.RoleID) != "") {
			return m.continueCanonicalFailoverAttempt(ctx, store, rec, active, nil)
		}
		return m.adoptFailoverAttempt(ctx, store, rec, active)
	}
	if hasActiveAgentSwitch {
		return ContinueFailoverResult{}, fmt.Errorf(
			"continue %s: %w: agent switch %s is %s on generation %s",
			id, ErrFailoverRecoveryRequired, activeAgentSwitch.ID,
			activeAgentSwitch.State, activeAgentSwitch.TargetGenerationID)
	}
	// D5's convergence branch. `acked` is terminal, so the check above will not
	// catch it, but a crash between the ack and the pin clear leaves exactly
	// this: a still-paused session whose move already happened. Without it the
	// retry would spend a SECOND rung to redo a completed continuation.
	//
	// `acked` is NOT sufficient to clear the pin, and that distinction is the
	// whole of this branch. The switch saga makes its target_ack ledger row
	// durable BEFORE it promotes the session, and ReconcileFailoverAttempts
	// marks the attempt from that ledger row -- so an attempt can read `acked`
	// while SwitchPending is still set and the session still names the SOURCE
	// harness. Clearing the pin there would lift a human's pause on a move that
	// has not landed; falling through instead (which is what happened when the
	// only guard was the runtime generation) would mint a NEW rung and run a
	// second switch over an unpromoted one. Promotion has to be proven, not
	// inferred from the ack.
	if latest, ok := domain.LatestFailoverAttempt(attempts); ok &&
		latest.State == domain.FailoverAttemptAcked &&
		latest.GenerationID != "" {
		if failoverPromotionSettled(rec, latest) {
			return m.finishFailoverPinClear(ctx, rec, latest)
		}
		if automatic && (latest.FromHarness != automaticSource || rec.Harness != automaticSource ||
			!m.automaticFailoverHarnessSupported(latest.FromHarness) ||
			!m.automaticFailoverHarnessSupported(latest.ToHarness)) {
			return ContinueFailoverResult{}, errAutomaticFailoverDisabled
		}
		return m.convergeUnpromotedAck(ctx, store, rec, latest)
	}

	// A structured limit belongs to the runtime generation that reported it,
	// not to whichever same-harness process happens to hold the session later.
	// This gate applies only before the FIRST automatic attempt: once an attempt
	// is durable, requested/post_stop/acked recovery follows that attempt's own
	// target and switch generation instead of reinterpreting the source pin.
	if automatic && len(attempts) == 0 {
		observed := strings.TrimSpace(paused.ObservedRuntimeLaunchID)
		current := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
		if observed == "" || current == "" || observed != current {
			return ContinueFailoverResult{}, errAutomaticFailoverDisabled
		}
	}

	// Automatic mode gets exactly one new rung selection per stable incident.
	// Any prior terminal attempt means a first automatic action already ran; a
	// failed rung stays paused for explicit manual Continue. Active attempts and
	// ack convergence were handled above so crash recovery still reuses their
	// stored target and generation instead of selecting another rung.
	if automatic && len(attempts) != 0 {
		return ContinueFailoverResult{}, errAutomaticFailoverAlreadyAttempted
	}

	if domain.CountFailoverAttempts(attempts) >= domain.MaxFailoversPerIncident {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w (%d of %d)",
			id, domain.ErrFailoverLimitReached, len(attempts), domain.MaxFailoversPerIncident)
	}

	if !automatic {
		project, err = m.loadProject(ctx, rec.ProjectID)
		if err != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, err)
		}
	} else if rec.Harness != automaticSource || !m.automaticFailoverHarnessSupported(automaticSource) {
		return ContinueFailoverResult{}, errAutomaticFailoverDisabled
	}
	current := domain.FailoverTarget{
		Harness: rec.Harness,
		Model:   strings.TrimSpace(rec.Metadata.Role.ResolvedModel),
	}
	// `used` is every prior attempt for this incident in ANY state: a rung that
	// failed is spent, not retried. The one-shot automatic trigger can select a
	// rung only when this set is empty; after a failure, only explicit manual
	// Continue may select the next unused target.
	target, rungIndex, err := domain.NextFailoverRung(
		project.Config.RoleMap, roleID, current, domain.UsedFailoverTargets(attempts))
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, err)
	}
	if automatic && !m.automaticFailoverHarnessSupported(target.Harness) {
		return ContinueFailoverResult{}, errAutomaticFailoverDisabled
	}

	// Contract section 6b: the generation is minted HERE, before the durable
	// write, and pinned into the saga. That is what lets the attempt row carry
	// its generation from its very first write.
	generation := m.newSwitchGeneration()
	sourceGeneration := strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
	if canonicalAgentSwitchEngine && sourceGeneration == "" {
		return ContinueFailoverResult{}, fmt.Errorf(
			"continue %s: %w: source generation is unavailable", id, ErrFailoverRecoveryRequired)
	}
	seq := domain.NextFailoverSeq(attempts)
	now := m.clock()
	roleSnapshot := domain.SessionRoleBinding{}
	if canonicalAgentSwitchEngine {
		roleSnapshot, err = m.failoverAgentSwitchRoleSnapshot(ctx, rec, domain.FailoverAttempt{
			RoleID: roleID, ToHarness: target.Harness, ToModel: target.Model,
		})
		if err != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: snapshot authorized role: %w", id, err)
		}
	}

	attempt := domain.FailoverAttempt{
		ID:                 domain.FailoverAttemptID(id, incident, seq),
		SessionID:          id,
		ProjectID:          rec.ProjectID,
		IncidentID:         incident,
		Seq:                seq,
		RoleID:             roleID,
		FromHarness:        rec.Harness,
		FromModel:          current.Model,
		ToHarness:          target.Harness,
		ToModel:            target.Model,
		RungIndex:          rungIndex,
		GenerationID:       generation,
		SourceGenerationID: sourceGeneration,
		RoleSnapshot:       roleSnapshot,
		State:              domain.FailoverAttemptRequested,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	admitAttempt := func(admitCtx context.Context) error {
		// ONE transaction for the ledger row and the attempt row (section 6
		// rule 1). The canonical engine invokes this under its per-session gate,
		// after active-saga and source-generation checks but before preserving
		// native state or creating the saga.
		if err := store.AppendSessionFailoverAttemptWithLedger(admitCtx, attempt,
			m.failoverLedgerRecord(attempt, domain.LifecyclePhaseRequested)); err != nil {
			return err
		}
		m.logger.Info("failover continue requested",
			"sessionID", id, "incident", incident, "seq", seq, "generation", generation,
			"sourceGeneration", sourceGeneration, "role", roleID,
			"from", string(rec.Harness), "to", string(target.Harness),
			"toModel", target.Model, "rung", rungIndex)
		return nil
	}
	if canonicalAgentSwitchEngine {
		return m.continueCanonicalFailoverAttempt(ctx, store, rec, attempt, admitAttempt)
	}
	if err := admitAttempt(ctx); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, err)
	}

	// One relaunch path: the existing worker switch entry point.
	res, switchErr := m.SwitchWorker(ctx, SwitchRequest{
		SessionID:         id,
		TargetHarness:     target.Harness,
		TargetModel:       target.Model,
		ForceGenerationID: generation,
		PauseIncidentID:   incident,
		// Defense in depth under beginSwitch. The automatic ownership fence
		// excludes Restart Agent across the earlier check and durable append;
		// this authoritative re-read refuses any other path that nevertheless
		// replaced the source before runtime work.
		ExpectedSourceRuntimeLaunchID: func() string {
			if automatic {
				return paused.ObservedRuntimeLaunchID
			}
			return ""
		}(),
		Semantic: domain.SemanticHandoffV1{
			SchemaVersion:    domain.SemanticHandoffSchemaVersion,
			SourceGeneration: strings.TrimSpace(rec.Metadata.RuntimeLaunchID),
			NativeSessionID:  rec.Metadata.AgentSessionID,
		},
	})
	if switchErr != nil {
		// Another Continue may have adopted this exact durable attempt and won
		// the switch ownership fence between our append and SwitchWorker. That
		// caller owns the shared attempt now. Marking it failed here can race its
		// target ack and leave a live target behind a terminal-failed row.
		if errors.Is(switchErr, ErrSwitchOperationInProgress) {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", id, switchErr)
		}
		return m.recordFailoverFailure(ctx, store, rec, attempt, switchErr)
	}
	return m.completeFailoverAttempt(ctx, store, rec, attempt, res, false)
}

func failoverAgentSwitchIDKey(attempt domain.FailoverAttempt) string {
	return "failover:" + attempt.ID
}

func failoverAgentSwitchNote(attempt domain.FailoverAttempt) string {
	return "failover attempt " + attempt.ID
}

func agentSwitchMatchesFailoverAttempt(sw domain.AgentSwitch, attempt domain.FailoverAttempt) bool {
	if sw.FailoverAttemptID != attempt.ID || sw.SessionID != attempt.SessionID ||
		sw.IdempotencyKey != failoverAgentSwitchIDKey(attempt) ||
		sw.TargetHarness != attempt.ToHarness || strings.TrimSpace(sw.TargetModel) != strings.TrimSpace(attempt.ToModel) ||
		string(sw.TargetGenerationID) != strings.TrimSpace(attempt.GenerationID) {
		return false
	}
	if strings.TrimSpace(attempt.RoleSnapshot.RoleID) != "" && sw.RoleSnapshot != attempt.RoleSnapshot {
		return false
	}
	return attempt.SourceGenerationID == "" ||
		string(sw.SourceGenerationID) == strings.TrimSpace(attempt.SourceGenerationID)
}

func (m *Manager) failoverAgentSwitchRoleSnapshot(
	ctx context.Context,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
) (domain.SessionRoleBinding, error) {
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRoleBinding{}, err
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	binding, ok := roleMap.Roles[attempt.RoleID]
	if !ok {
		return domain.SessionRoleBinding{}, ErrFailoverRecoveryRequired
	}
	sha, err := roleMap.SHA256()
	if err != nil {
		return domain.SessionRoleBinding{}, err
	}
	snapshot := rec.Metadata.Role
	snapshot.RoleID = attempt.RoleID
	snapshot.RoleMapSchemaVersion = roleMap.SchemaVersion
	snapshot.RoleMapSHA256 = sha
	snapshot.ResolvedHarness = attempt.ToHarness
	snapshot.ResolvedModel = strings.TrimSpace(attempt.ToModel)
	snapshot.ResolvedPermissions = binding.Permissions
	return snapshot, nil
}

func (m *Manager) continueCanonicalFailoverAttempt(
	ctx context.Context,
	store failoverAttemptStore,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
	admit func(context.Context) error,
) (ContinueFailoverResult, error) {
	agentStore, ok := m.store.(ports.AgentSwitchStore)
	if !ok {
		return ContinueFailoverResult{}, ErrFailoverNotWired
	}

	sourceGeneration := strings.TrimSpace(attempt.SourceGenerationID)
	roleSnapshot := attempt.RoleSnapshot
	if existing, found, err := agentStore.GetAgentSwitchByIdempotencyKey(
		ctx, attempt.SessionID, failoverAgentSwitchIDKey(attempt)); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read failover saga: %w", rec.ID, err)
	} else if found {
		if !agentSwitchMatchesFailoverAttempt(existing, attempt) {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: saga %s does not match attempt %s",
				rec.ID, ErrFailoverRecoveryRequired, existing.ID, attempt.ID)
		}
		if sourceGeneration == "" {
			return ContinueFailoverResult{}, fmt.Errorf(
				"continue %s: %w: legacy attempt %s has a saga but no durable source generation",
				rec.ID, ErrFailoverRecoveryRequired, attempt.ID)
		}
		if existing.RoleSnapshot != attempt.RoleSnapshot {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: saga %s role snapshot differs from attempt %s",
				rec.ID, ErrFailoverRecoveryRequired, existing.ID, attempt.ID)
		}
	} else {
		// Upgraded legacy requested attempts can lack the source fence. Re-drive
		// only while the source record still matches every durable attempt fact;
		// otherwise a human must resolve which runtime owns the session.
		if sourceGeneration == "" {
			if rec.Harness != attempt.FromHarness ||
				strings.TrimSpace(rec.Metadata.Role.ResolvedModel) != strings.TrimSpace(attempt.FromModel) {
				return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: legacy attempt source changed",
					rec.ID, ErrFailoverRecoveryRequired)
			}
			sourceGeneration = strings.TrimSpace(rec.Metadata.RuntimeLaunchID)
		}
		if sourceGeneration == "" {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: source generation is unavailable",
				rec.ID, ErrFailoverRecoveryRequired)
		}
		if strings.TrimSpace(roleSnapshot.RoleID) == "" ||
			roleSnapshot.ResolvedHarness != attempt.ToHarness ||
			strings.TrimSpace(roleSnapshot.ResolvedModel) != strings.TrimSpace(attempt.ToModel) {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: attempt %s has no exact role snapshot",
				rec.ID, ErrFailoverRecoveryRequired, attempt.ID)
		}
	}

	admitted := admit == nil
	admitUnderGate := admit
	if admit != nil {
		admitUnderGate = func(admitCtx context.Context) error {
			if err := admit(admitCtx); err != nil {
				return err
			}
			admitted = true
			return nil
		}
	}
	sw, switchErr := m.switchAgentWithAdmission(ctx, rec.ID, SwitchAgentConfig{
		TargetHarness:              attempt.ToHarness,
		TargetModel:                attempt.ToModel,
		Note:                       failoverAgentSwitchNote(attempt),
		IdempotencyKey:             failoverAgentSwitchIDKey(attempt),
		RequiredTargetGenerationID: domain.AgentGenerationID(attempt.GenerationID),
		ExpectedSourceGenerationID: domain.AgentGenerationID(sourceGeneration),
		RoleSnapshot:               roleSnapshot,
		FailoverAttemptID:          attempt.ID,
	}, admitUnderGate)
	if switchErr != nil {
		if !admitted {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", rec.ID, switchErr)
		}
		if sw.ID != "" && !sw.State.Terminal() {
			if attempt.State == domain.FailoverAttemptRequested {
				_, _ = store.UpdateSessionFailoverAttemptState(ctx, attempt.ID,
					attempt.State, domain.FailoverAttemptPostStop, m.clock())
			}
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: %w",
				rec.ID, ErrFailoverRecoveryRequired, switchErr)
		}
		return m.recordFailoverFailure(ctx, store, rec, attempt, switchErr)
	}
	if !agentSwitchMatchesFailoverAttempt(sw, attempt) {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: saga %s does not match attempt %s",
			rec.ID, ErrFailoverRecoveryRequired, sw.ID, attempt.ID)
	}
	if sw.State != domain.AgentSwitchCompleted {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: saga %s remains %s",
			rec.ID, ErrFailoverRecoveryRequired, sw.ID, sw.State)
	}
	current, found, err := m.store.GetSession(ctx, rec.ID)
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read promoted session: %w", rec.ID, err)
	}
	if !found || current.Harness != attempt.ToHarness ||
		strings.TrimSpace(current.Metadata.Role.ResolvedModel) != strings.TrimSpace(attempt.ToModel) ||
		strings.TrimSpace(current.Metadata.RuntimeLaunchID) != strings.TrimSpace(attempt.GenerationID) {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: completed saga did not promote exact attempt intent",
			rec.ID, ErrFailoverRecoveryRequired)
	}
	return m.completeFailoverAttempt(ctx, store, rec, attempt, SwitchResult{
		Session: current, GenerationID: attempt.GenerationID, Kind: domain.LifecycleKindFailover,
	}, admit == nil)
}

// failoverEligible refuses the session kinds that cannot enter the saga at all.
// Separate from the pause checks so the same order can be reused by the preview
// without duplicating the reasoning.
func failoverEligible(rec domain.SessionRecord) error {
	if rec.IsTerminated {
		return ErrTerminated
	}
	if rec.Kind != domain.KindWorker {
		// Worker Continue remains its own operator-paused ladder. Orchestrators
		// switch through the gated orchestrator switch surface, not this incident
		// state machine.
		return ErrNotWorker
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return ErrSwitchChatUnsupported
	}
	return nil
}

// adoptFailoverAttempt is contract section 6 rules 5 and 6: a duplicate
// Continue never starts a new attempt, and an incomplete post_stop belonging to
// an abandoned attempt is COMPLETED on the same generation instead of being
// redone on a fresh rung. A live owner is different: it returns
// ErrSwitchOperationInProgress without mutating the attempt because fence occupancy does
// not prove that owner's switch will eventually acknowledge the target.
//
// No path advances the ladder. An abandoned attempt is finished on its stored
// rung and generation; a live one is left untouched for its current owner.
// Neither is an automatic retry: adoption never selects a new rung. A new rung
// is selected only by explicit manual Continue or the first, capability-gated
// automatic action for an incident.
func (m *Manager) adoptFailoverAttempt(
	ctx context.Context,
	store failoverAttemptStore,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
) (ContinueFailoverResult, error) {
	incompleteGen, hasIncomplete, err := m.incompleteSwitchGeneration(ctx, rec)
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read recovery state: %w", rec.ID, err)
	}
	if !hasIncomplete {
		// Two very different worlds reach here, and answering them the same way
		// is a silent failure.
		//
		// Either the saga is running RIGHT NOW under beginSwitch -- a genuine
		// duplicate Continue -- or the daemon died between the attempt's durable
		// write and SwitchWorker establishing any pending state at all, leaving a
		// `requested` attempt that never launched anything. Reporting reuse for
		// the second case tells the operator the move happened, spends the rung,
		// and parks the session paused forever with no runtime and nothing left
		// that would ever drive it: boot is deliberately passive here.
		//
		// beginSwitch is the discriminator, and it is exactly the right one
		// because it is in-memory: a live saga holds it and refuses with
		// ErrSwitchOperationInProgress, while after a crash it is free and the re-drive
		// proceeds. Nothing durable can tell these apart, which is why the old
		// code could not.
		//
		// The re-drive is not a retry: it uses the attempt's OWN stored target
		// and generation, so it is the same rung on the same identity, and no
		// second attempt row is ever minted.
		res, switchErr := m.SwitchWorker(ctx, SwitchRequest{
			SessionID:         rec.ID,
			TargetHarness:     attempt.ToHarness,
			TargetModel:       attempt.ToModel,
			ForceGenerationID: attempt.GenerationID,
			PauseIncidentID:   attempt.IncidentID,
			Semantic: domain.SemanticHandoffV1{
				SchemaVersion:    domain.SemanticHandoffSchemaVersion,
				SourceGeneration: strings.TrimSpace(rec.Metadata.RuntimeLaunchID),
				NativeSessionID:  rec.Metadata.AgentSessionID,
			},
		})
		if errors.Is(switchErr, ErrSwitchOperationInProgress) {
			// A live fence distinguishes an abandoned requested attempt from a
			// transition running right now, but it does not prove the owner is
			// this attempt or that it will eventually succeed. Preserve the
			// requested row and surface the conflict; only target_ack plus pin
			// clear is a completed Continue result.
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", rec.ID, switchErr)
		}
		if switchErr != nil {
			return m.recordFailoverFailure(ctx, store, rec, attempt, switchErr)
		}
		return m.completeFailoverAttempt(ctx, store, rec, attempt, res, true)
	}
	if incompleteGen != attempt.GenerationID {
		// Section 6b removed the guess that used to live here. The generation
		// is an identity now, so a mismatch is a genuine ambiguity for a human
		// rather than something to resolve by matching harness/model and ledger
		// order — and a wrong resolution there is a second runtime.
		return ContinueFailoverResult{}, fmt.Errorf(
			"continue %s: %w: incomplete switch generation %s does not match attempt %s (generation %s)",
			rec.ID, ErrFailoverRecoveryRequired, incompleteGen, attempt.ID, attempt.GenerationID)
	}

	res, err := m.RecoverSwitchFromPostStop(ctx, rec.ID)
	if errors.Is(err, ErrSwitchOperationInProgress) {
		// The matching generation belongs to the live saga that still owns the
		// switch fence. A duplicate Continue observes its durable pending pin,
		// but must not promote the OUTER attempt while the first caller is still
		// between pre_stop and target_ack. Doing so steals the first caller's
		// requested->acked CAS after an otherwise successful switch. Nor may it
		// return success: the owner can still fail and roll back. Preserve state
		// and let the typed conflict reach the operator.
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: recover post_stop: %w", rec.ID, err)
	}
	if err != nil {
		// Still recoverable, and still the same generation. The attempt stays
		// non-terminal so the next Continue (or boot recovery) can finish it;
		// writing `failed` here is precisely the section 6a defect.
		if _, updErr := store.UpdateSessionFailoverAttemptState(ctx, attempt.ID,
			attempt.State, domain.FailoverAttemptPostStop, m.clock()); updErr != nil {
			m.logger.Warn("failover: recording post_stop after failed recovery",
				"sessionID", rec.ID, "attempt", attempt.ID, "error", updErr)
		}
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: recover post_stop: %w", rec.ID, err)
	}
	return m.completeFailoverAttempt(ctx, store, rec, attempt, res, true)
}

// incompleteSwitchGeneration reports the generation of an unfinished switch, if
// any, using the two durable sources the existing recovery machinery uses: the
// pending pin, and the ledger fallback for when the pin is gone.
func (m *Manager) incompleteSwitchGeneration(
	ctx context.Context,
	rec domain.SessionRecord,
) (string, bool, error) {
	if p := rec.Metadata.SwitchPending; p != nil && strings.TrimSpace(p.GenerationID) != "" {
		return strings.TrimSpace(p.GenerationID), true, nil
	}
	events, err := m.store.ListLifecycleLedger(ctx, rec.ID)
	if err != nil {
		return "", false, err
	}
	if post, ok := findRecoverablePostStop(events); ok {
		return post.GenerationID, true, nil
	}
	return "", false, nil
}

// completeFailoverAttempt records a target_ack and lifts the pause.
//
// Order (D5): attempt -> acked, then the ledger target_ack row, then the pin
// clear. The pin clear is LAST and is a compare-and-set on the same incident,
// so a newer pause is never lifted by an older continuation, and a crash before
// it leaves a paused session whose latest attempt is `acked` -- which the
// convergence branch in ContinueFailover recognises and finishes rather than
// redoing on a new rung.
func (m *Manager) completeFailoverAttempt(
	ctx context.Context,
	store failoverAttemptStore,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
	res SwitchResult,
	reused bool,
) (ContinueFailoverResult, error) {
	ok, err := store.UpdateSessionFailoverAttemptState(ctx, attempt.ID,
		attempt.State, domain.FailoverAttemptAcked, m.clock())
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: mark acked: %w", rec.ID, err)
	}
	if !ok {
		// The CAS matched nothing, so the row is not in the state this call
		// believed. Discarding that answer let the pin clear anyway -- the exact
		// inverse of "the pause clears only after target_ack on THIS attempt",
		// and it would also undermine the attempt row's job as the authority on
		// which rungs are spent.
		//
		// Re-read rather than fail outright: post_stop has two legitimate
		// completers racing to the same ack (an operator Continue and boot's
		// recovery), so a loser here is ordinary. If the row is genuinely acked
		// the move landed and only the pin clear is outstanding; anything else
		// is a human's problem, not something to resolve by clearing a pause.
		rows, readErr := store.ListSessionFailoverAttemptsByIncident(ctx, attempt.SessionID, attempt.IncidentID)
		if readErr != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: re-read attempt after CAS miss: %w", rec.ID, readErr)
		}
		settled := false
		for _, row := range rows {
			if row.ID == attempt.ID && row.State == domain.FailoverAttemptAcked {
				settled = true
				break
			}
		}
		if !settled {
			return ContinueFailoverResult{}, fmt.Errorf(
				"continue %s: %w: attempt %s did not reach acked (state transition from %q was lost)",
				rec.ID, ErrFailoverRecoveryRequired, attempt.ID, attempt.State)
		}
	}
	acked := attempt
	acked.State = domain.FailoverAttemptAcked

	if err := m.appendFailoverLedger(ctx, acked, domain.LifecyclePhaseTargetAck); err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", rec.ID, err)
	}

	out, err := m.finishFailoverPinClear(ctx, res.Session, acked)
	if err != nil {
		return ContinueFailoverResult{}, err
	}
	out.Reused = reused
	m.logger.Info("failover continue acked",
		"sessionID", rec.ID, "incident", acked.IncidentID, "seq", acked.Seq,
		"generation", acked.GenerationID, "to", string(acked.ToHarness), "reused", reused)
	return out, nil
}

// failoverPromotionSettled reports whether the session has actually BEEN moved
// to the attempt's target, as opposed to merely having a durable target_ack.
//
// All four conditions are load-bearing, because the ack alone proves none of
// them: the saga writes target_ack before it promotes the session fields, so
// between those two writes the row still carries the source harness, the source
// model and a live SwitchPending pin. Clearing a human's pause on that evidence
// would be "pause cleared before ack" wearing the ack's clothes.
func failoverPromotionSettled(rec domain.SessionRecord, attempt domain.FailoverAttempt) bool {
	if rec.Metadata.SwitchPending != nil {
		return false
	}
	if rec.Harness != attempt.ToHarness {
		return false
	}
	// Compare against the model the SAGA resolves, not the ladder's configured
	// one. resolveTargetModel rewrites an empty SAME-harness target to the
	// source model, so a legal rung like {codex, ""} under a {codex, "gpt-5"}
	// primary -- "fall back to the provider default on this harness", which
	// ValidateRoleMap permits and which is a reasonable ladder entry -- lands on
	// the session as "gpt-5" while the attempt row still records "".
	//
	// A raw equality against attempt.ToModel therefore never settles for that
	// rung, and the session sticks: convergeUnpromotedAck finds no incomplete
	// switch (the ack is durable and pending is already cleared), so it refuses
	// with FAILOVER_RECOVERY_REQUIRED, the pin never clears through Continue,
	// and the rung is spent. Only Resume escapes, and a resume is not a failover.
	want := resolveTargetModel(attempt.ToModel, attempt.FromModel, attempt.FromHarness == attempt.ToHarness)
	if strings.TrimSpace(rec.Metadata.Role.ResolvedModel) != strings.TrimSpace(want) {
		return false
	}
	return strings.TrimSpace(rec.Metadata.RuntimeLaunchID) == strings.TrimSpace(attempt.GenerationID)
}

// convergeUnpromotedAck handles an attempt that reads `acked` while the session
// has not been promoted to its target.
//
// It deliberately never re-drives SwitchWorker the way the `requested` adoption
// path does. A durable target_ack means a target runtime may already exist, so
// re-launching is how this session ends up with two; the only safe completions
// are finishing the SAME generation through the existing recovery path, or
// handing the ambiguity to a human. It also never mints a new rung, and never
// clears the pin before promotion is proven.
func (m *Manager) convergeUnpromotedAck(
	ctx context.Context,
	store failoverAttemptStore,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
) (ContinueFailoverResult, error) {
	incompleteGen, hasIncomplete, err := m.incompleteSwitchGeneration(ctx, rec)
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: read recovery state: %w", rec.ID, err)
	}
	if !hasIncomplete || incompleteGen != attempt.GenerationID {
		return ContinueFailoverResult{}, fmt.Errorf(
			"continue %s: %w: attempt %s is acked on generation %s but the session is not promoted to %s/%q",
			rec.ID, ErrFailoverRecoveryRequired, attempt.ID, attempt.GenerationID,
			attempt.ToHarness, attempt.ToModel)
	}
	res, recErr := m.RecoverSwitchFromPostStop(ctx, rec.ID)
	if recErr != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w: recovering generation %s: %w",
			rec.ID, ErrFailoverRecoveryRequired, attempt.GenerationID, recErr)
	}
	// Already `acked`; completeFailoverAttempt's CAS is a no-op transition it
	// tolerates, and the pin clear it performs is the step that was missing.
	return m.completeFailoverAttempt(ctx, store, rec, attempt, res, true)
}

// finishFailoverPinClear lifts the pause for this incident and returns the
// completed result. Split out because it is also the whole of the convergence
// branch: a retry after a crash between the ack and the clear does only this.
func (m *Manager) finishFailoverPinClear(
	ctx context.Context,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
) (ContinueFailoverResult, error) {
	cleared, err := m.store.ClearSessionPauseIfIncident(ctx, attempt.SessionID, attempt.IncidentID, m.clock())
	if err != nil {
		return ContinueFailoverResult{}, fmt.Errorf("continue %s: clear pause: %w", attempt.SessionID, err)
	}
	if !cleared {
		// The CAS names this incident, so a miss means the pin changed under us
		// -- someone resumed it, or a newer incident now holds the session.
		// Either way the move itself is done and recorded; re-read so the
		// caller sees the truth rather than a stale snapshot.
		current, ok, readErr := m.store.GetSession(ctx, attempt.SessionID)
		if readErr != nil {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: re-read after contended clear: %w",
				attempt.SessionID, readErr)
		}
		if !ok {
			return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", attempt.SessionID, ErrNotFound)
		}
		rec = current
	} else {
		rec.Metadata.Pause = nil
	}
	return failoverResult(rec, attempt, true), nil
}

// recordFailoverFailure classifies a saga failure as terminal or recoverable
// and records it. This function is contract section 6a.
//
// The pause is untouched on every path: failure never lifts the pin.
func (m *Manager) recordFailoverFailure(
	ctx context.Context,
	store failoverAttemptStore,
	rec domain.SessionRecord,
	attempt domain.FailoverAttempt,
	switchErr error,
) (ContinueFailoverResult, error) {
	state := m.failoverFailureState(ctx, rec.ID, attempt.GenerationID, switchErr)
	updated, err := store.UpdateSessionFailoverAttemptState(ctx, attempt.ID,
		attempt.State, state, m.clock())
	if err != nil {
		m.logger.Warn("failover: recording attempt failure",
			"sessionID", rec.ID, "attempt", attempt.ID, "state", string(state), "error", err)
		return ContinueFailoverResult{}, errors.Join(
			fmt.Errorf("continue %s: %w", rec.ID, switchErr),
			fmt.Errorf("continue %s: record attempt failure: %w", rec.ID, err),
		)
	}
	if !updated {
		// A competing adopter may have completed this exact requested attempt
		// while the original caller was waiting to enter SwitchWorker. The stale
		// caller must not append `failed` after the shared row reached `acked`.
		rows, readErr := store.ListSessionFailoverAttemptsByIncident(ctx, attempt.SessionID, attempt.IncidentID)
		if readErr != nil {
			return ContinueFailoverResult{}, errors.Join(
				fmt.Errorf("continue %s: %w", rec.ID, switchErr),
				fmt.Errorf("continue %s: re-read attempt after failure CAS miss: %w", rec.ID, readErr),
			)
		}
		var currentAttempt *domain.FailoverAttempt
		for i := range rows {
			if rows[i].ID == attempt.ID {
				currentAttempt = &rows[i]
				break
			}
		}
		if currentAttempt == nil {
			return ContinueFailoverResult{}, errors.Join(
				fmt.Errorf("continue %s: %w", rec.ID, switchErr),
				fmt.Errorf("continue %s: %w: attempt %s disappeared after failure CAS miss",
					rec.ID, ErrFailoverRecoveryRequired, attempt.ID),
			)
		}
		if currentAttempt.State == domain.FailoverAttemptAcked {
			row := *currentAttempt
			current, ok, currentErr := m.store.GetSession(ctx, rec.ID)
			if currentErr != nil {
				return ContinueFailoverResult{}, errors.Join(
					fmt.Errorf("continue %s: %w", rec.ID, switchErr),
					fmt.Errorf("continue %s: re-read completed attempt session: %w", rec.ID, currentErr),
				)
			}
			if !ok {
				return ContinueFailoverResult{}, errors.Join(
					fmt.Errorf("continue %s: %w", rec.ID, switchErr),
					fmt.Errorf("continue %s: %w", rec.ID, ErrNotFound),
				)
			}
			if failoverPromotionSettled(current, row) {
				return m.finishFailoverPinClear(ctx, current, row)
			}
			return ContinueFailoverResult{}, errors.Join(
				fmt.Errorf("continue %s: %w", rec.ID, switchErr),
				fmt.Errorf("continue %s: %w", rec.ID, ErrSwitchOperationInProgress),
			)
		}
		return ContinueFailoverResult{}, errors.Join(
			fmt.Errorf("continue %s: %w", rec.ID, switchErr),
			fmt.Errorf("continue %s: %w: attempt %s is %s after failure CAS miss",
				rec.ID, ErrFailoverRecoveryRequired, attempt.ID, currentAttempt.State),
		)
	}
	failed := attempt
	failed.State = state

	// A pre-stop failure is over, so it gets a ledger `failed` row. A post-stop
	// failure does NOT: the saga is unfinished, not finished badly, and a
	// `failed` row for a generation that is still going to be recovered is a
	// false entry in an append-only audit trail. The switch saga's own post_stop
	// row already records where it got to.
	if state == domain.FailoverAttemptFailed {
		if err := m.appendFailoverLedger(ctx, failed, domain.LifecyclePhaseFailed); err != nil {
			m.logger.Warn("failover: recording failed ledger row",
				"sessionID", rec.ID, "attempt", attempt.ID, "error", err)
		}
	}

	m.logger.Info("failover continue failed",
		"sessionID", rec.ID, "incident", attempt.IncidentID, "seq", attempt.Seq,
		"generation", attempt.GenerationID, "state", string(state), "error", switchErr)
	return ContinueFailoverResult{}, fmt.Errorf("continue %s: %w", rec.ID, switchErr)
}

// failoverFailureState decides terminal vs recoverable from what the saga
// actually LEFT BEHIND, not from the shape of the error string.
//
// ErrSwitchPostStop is the explicit signal and is honoured first. But it is not
// the only way to end up past the point of no return: the saga also returns a
// plain "pre-stop" error when destroy could not confirm liveness, and in that
// case it deliberately KEEPS the pending fence so recovery can probe and retry.
// Reading that as terminal would mark `failed` an attempt whose generation can
// still reach target_ack -- the exact defect section 6a exists to remove. So the
// question asked here is the durable one: is there still a recoverable fence for
// this generation? If yes the attempt is post_stop; only when nothing was left
// behind is it safe to call the source confirmed-alive and the attempt failed.
func (m *Manager) failoverFailureState(
	ctx context.Context,
	id domain.SessionID,
	generation string,
	switchErr error,
) domain.FailoverAttemptState {
	if errors.Is(switchErr, ErrSwitchPostStop) {
		return domain.FailoverAttemptPostStop
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		// Cannot prove the source survived. Recoverable is the safe answer: a
		// post_stop attempt can still be completed or re-examined, whereas a
		// wrongly-terminal one silently spends a rung and invites a second
		// runtime on the next Continue.
		return domain.FailoverAttemptPostStop
	}
	gen, hasIncomplete, err := m.incompleteSwitchGeneration(ctx, rec)
	if err != nil {
		return domain.FailoverAttemptPostStop
	}
	if hasIncomplete && gen == generation {
		return domain.FailoverAttemptPostStop
	}
	return domain.FailoverAttemptFailed
}

// ReconcileFailoverAttempts settles attempts a crash left mid-flight, using the
// lifecycle ledger as the authority.
//
// Exported deliberately: boot can call it per session at integration without
// this file editing manager.go's reconcile region, which another agent owns. It
// is also called at the top of both entry points here, because boot's
// pausedSkip excludes paused sessions from automatic post_stop recovery -- and
// every session this feature cares about is paused.
//
// It never launches anything and never advances a ladder. It only makes the
// attempt row agree with what the ledger already says happened.
func (m *Manager) ReconcileFailoverAttempts(ctx context.Context, id domain.SessionID) error {
	store, err := m.failoverStore()
	if err != nil {
		return err
	}
	attempts, err := store.ListSessionFailoverAttemptsBySession(ctx, id)
	if err != nil {
		return fmt.Errorf("reconcile failover %s: %w", id, err)
	}
	pending := make([]domain.FailoverAttempt, 0, len(attempts))
	for _, a := range attempts {
		if !a.State.Terminal() {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	events, err := m.store.ListLifecycleLedger(ctx, id)
	if err != nil {
		return fmt.Errorf("reconcile failover %s: read ledger: %w", id, err)
	}
	acked := map[string]bool{}
	postStopped := map[string]bool{}
	failedGen := map[string]bool{}
	for _, e := range events {
		if !isSwitchLedgerKind(e.Kind) || e.GenerationID == "" {
			continue
		}
		switch e.Phase {
		case domain.LifecyclePhaseTargetAck:
			acked[e.GenerationID] = true
		case domain.LifecyclePhasePostStop:
			postStopped[e.GenerationID] = true
		case domain.LifecyclePhaseFailed:
			failedGen[e.GenerationID] = true
		}
	}

	for _, a := range pending {
		if agentStore, ok := m.store.(ports.AgentSwitchStore); ok {
			sw, found, readErr := agentStore.GetAgentSwitchByIdempotencyKey(
				ctx, a.SessionID, failoverAgentSwitchIDKey(a))
			if readErr != nil {
				return fmt.Errorf("reconcile failover %s: read agent saga for attempt %s: %w", id, a.ID, readErr)
			}
			if found {
				if !agentSwitchMatchesFailoverAttempt(sw, a) {
					return fmt.Errorf("reconcile failover %s: %w: saga %s does not match attempt %s",
						id, ErrFailoverRecoveryRequired, sw.ID, a.ID)
				}
				var next domain.FailoverAttemptState
				switch {
				case sw.State == domain.AgentSwitchCompleted:
					next = domain.FailoverAttemptAcked
				case sw.State == domain.AgentSwitchFailed:
					next = domain.FailoverAttemptFailed
				case a.State == domain.FailoverAttemptRequested &&
					(sw.State == domain.AgentSwitchSourceStopped || sw.State == domain.AgentSwitchStartingTarget ||
						sw.State == domain.AgentSwitchTargetReady || sw.State == domain.AgentSwitchDelivering):
					next = domain.FailoverAttemptPostStop
				}
				if next == "" || next == a.State {
					continue
				}
				changed, updateErr := store.UpdateSessionFailoverAttemptState(ctx, a.ID, a.State, next, m.clock())
				if updateErr != nil {
					return fmt.Errorf("reconcile failover %s: attempt %s from agent saga: %w", id, a.ID, updateErr)
				}
				if changed {
					m.logger.Info("failover attempt reconciled from agent switch",
						"sessionID", id, "attempt", a.ID, "switchID", sw.ID,
						"generation", a.GenerationID, "from", string(a.State), "to", string(next))
				}
				continue
			}
		}
		next, ok := reconciledFailoverState(a, acked, postStopped, failedGen)
		if !ok {
			continue
		}
		changed, err := store.UpdateSessionFailoverAttemptState(ctx, a.ID, a.State, next, m.clock())
		if err != nil {
			return fmt.Errorf("reconcile failover %s: attempt %s: %w", id, a.ID, err)
		}
		if changed {
			m.logger.Info("failover attempt reconciled",
				"sessionID", id, "attempt", a.ID, "generation", a.GenerationID,
				"from", string(a.State), "to", string(next))
		}
	}
	return nil
}

// reconciledFailoverState is the pure half of reconciliation: given what the
// ledger says about an attempt's generation, what should the attempt row say?
//
// Only ever moves an attempt FORWARD along the state machine, and only on
// durable evidence. Silence is a valid answer -- an attempt whose generation has
// no verdict yet is left exactly as it is.
func reconciledFailoverState(
	a domain.FailoverAttempt,
	acked, postStopped, failedGen map[string]bool,
) (domain.FailoverAttemptState, bool) {
	if a.GenerationID == "" {
		return "", false
	}
	switch {
	case acked[a.GenerationID]:
		// The move demonstrably happened. Leaving it non-terminal would let the
		// next Continue adopt an attempt that is already complete.
		return domain.FailoverAttemptAcked, true
	case postStopped[a.GenerationID] && a.State == domain.FailoverAttemptRequested:
		// A crash after the source stopped but before the attempt row learned
		// it. Both states are non-terminal so adoption already worked, but the
		// row should not claim the source is still alive.
		return domain.FailoverAttemptPostStop, true
	case failedGen[a.GenerationID] && !postStopped[a.GenerationID] &&
		a.State == domain.FailoverAttemptRequested:
		// A `failed` ledger row with no post_stop for the same generation means
		// the saga stopped while the source was still alive. Requiring the
		// absence of post_stop is what keeps the uncertain-destroy path -- which
		// writes `failed` and then KEEPS its pending fence -- from being marked
		// terminal here.
		return domain.FailoverAttemptFailed, true
	default:
		return "", false
	}
}

// FailoverPreview answers "what would Continue do?" at read time. Derived,
// never stored.
//
// Computed by the manager rather than the service so the ladder is resolved in
// exactly one place, by the same code that will execute it: a second resolution
// in the service is a second source of truth that can disagree with the button
// it labels.
func (m *Manager) FailoverPreview(ctx context.Context, id domain.SessionID) (FailoverPreview, error) {
	out := FailoverPreview{
		NextRungIndex: -1,
		MaxAttempts:   domain.MaxFailoversPerIncident,
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return FailoverPreview{}, fmt.Errorf("failover preview %s: %w", id, err)
	}
	if !ok {
		return FailoverPreview{}, fmt.Errorf("failover preview %s: %w", id, ErrNotFound)
	}

	// Reason precedence is D8, and it is load-bearing beyond cosmetics: contract
	// section 9 wants the whole block null for a session that is "not paused AND
	// has no ladder", and the service derives that from
	// (Pause == nil && Reason == no_ladder). That only works if no_ladder
	// outranks not_paused, so this order is part of the wire contract.
	// The three suppressed sites below deliberately turn an error into a Reason
	// rather than propagating it, and that IS the preview's job: "Continue is
	// unavailable, and here is the machine-readable why".
	// The distinction that matters is kept strictly: every INFRASTRUCTURE
	// failure in this function (GetSession, loadProject, failoverStore,
	// ListSessionFailoverAttemptsByIncident) still returns an error, so a store
	// that cannot be read never renders as an available or explained preview.
	if err := failoverEligible(rec); err != nil {
		out.Reason = FailoverReasonSwitchUnsupported
		return out, nil //nolint:nilerr // classification, not failure — see above
	}
	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	out.RoleID = roleID
	if roleID == "" {
		out.Reason = FailoverReasonNoRolePin
		return out, nil
	}

	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return FailoverPreview{}, fmt.Errorf("failover preview %s: %w", id, err)
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if len(roleMap.Failover.Roles[roleID]) == 0 {
		out.Reason = FailoverReasonNoLadder
		return out, nil
	}

	paused := rec.Metadata.Pause
	if paused == nil {
		out.Reason = FailoverReasonNotPaused
		return out, nil
	}
	out.IncidentID = paused.IncidentID

	store, err := m.failoverStore()
	if err != nil {
		return FailoverPreview{}, fmt.Errorf("failover preview %s: %w", id, err)
	}
	attempts, err := store.ListSessionFailoverAttemptsByIncident(ctx, id, paused.IncidentID)
	if err != nil {
		return FailoverPreview{}, fmt.Errorf("failover preview %s: read attempts: %w", id, err)
	}
	out.AttemptsUsed = domain.CountFailoverAttempts(attempts)
	if out.AttemptsUsed >= domain.MaxFailoversPerIncident {
		out.Reason = FailoverReasonLimitReached
		return out, nil
	}

	current := domain.FailoverTarget{
		Harness: rec.Harness,
		Model:   strings.TrimSpace(rec.Metadata.Role.ResolvedModel),
	}
	target, rungIndex, err := domain.NextFailoverRung(
		roleMap, roleID, current, domain.UsedFailoverTargets(attempts))
	if err != nil {
		// ErrFailoverNoTarget is an expected verdict about the ladder, not a
		// fault: every rung is spent or equals the current target.
		out.Reason = FailoverReasonLadderExhausted
		return out, nil //nolint:nilerr // classification, not failure
	}
	// Capability is checked LAST, on the rung actually selected. A rung whose
	// harness cannot switch is not skipped over in favour of the next one: the
	// resolution rule is host-authorized order, and quietly stepping past an
	// authorized rung would be a silent degrade.
	if err := m.requireSwitchCaps(rec.Harness, target.Harness, rec.Harness == target.Harness); err != nil {
		out.Reason = FailoverReasonSwitchUnsupported
		return out, nil //nolint:nilerr // classification, not failure
	}

	out.Available = true
	out.NextTarget = target
	out.NextRungIndex = rungIndex
	out.Reason = FailoverReasonNone
	return out, nil
}

// failoverLedgerRecord builds one failover ledger row for an attempt phase.
//
// The generation_id column carries the REAL switch generation, not the incident
// id. An earlier draft used the incident because the generation was unknown when
// the requested row was written; section 6b made it known, and a real generation
// lets an audit join ledger rows to the attempt row directly. This is only safe
// because `failover` is not in isSwitchLedgerKind: every scan that interprets a
// generation (findPhasePayload, findRecoverablePostStop, hasIncompletePostStop)
// filters through it, so these rows can never be mistaken for a recoverable
// switch phase. The incident stays addressable through the row id.
func (m *Manager) failoverLedgerRecord(
	attempt domain.FailoverAttempt,
	phase domain.LifecycleLedgerPhase,
) domain.LifecycleLedgerRecord {
	return domain.LifecycleLedgerRecord{
		ID:           domain.FailoverLedgerID(attempt.SessionID, attempt.IncidentID, attempt.Seq, phase),
		SessionID:    attempt.SessionID,
		ProjectID:    attempt.ProjectID,
		Kind:         domain.LifecycleKindFailover,
		Phase:        phase,
		GenerationID: attempt.GenerationID,
		FromHarness:  attempt.FromHarness,
		ToHarness:    attempt.ToHarness,
		FromModel:    attempt.FromModel,
		ToModel:      attempt.ToModel,
		RoleID:       attempt.RoleID,
		PayloadJSON: fmt.Sprintf(`{"incidentId":%q,"attemptSeq":%d,"rungIndex":%d,"state":%q}`,
			attempt.IncidentID, attempt.Seq, attempt.RungIndex, attempt.State),
		CreatedAt: m.clock(),
	}
}

// appendFailoverLedger writes a post-requested failover phase row. The
// requested row is NOT written through here: it goes inside the rule-1
// transaction alongside the attempt row.
func (m *Manager) appendFailoverLedger(
	ctx context.Context,
	attempt domain.FailoverAttempt,
	phase domain.LifecycleLedgerPhase,
) error {
	row := m.failoverLedgerRecord(attempt, phase)
	if events, err := m.store.ListLifecycleLedger(ctx, attempt.SessionID); err == nil {
		for _, e := range events {
			if e.ID == row.ID {
				return nil // retry-safe, same as the pause and switch appends
			}
		}
	}
	if err := m.store.AppendLifecycleLedger(ctx, row); err != nil {
		return fmt.Errorf("lifecycle ledger %s/%s: %w", domain.LifecycleKindFailover, phase, err)
	}
	return nil
}

func failoverResult(rec domain.SessionRecord, attempt domain.FailoverAttempt, reused bool) ContinueFailoverResult {
	return ContinueFailoverResult{
		Session:      rec,
		IncidentID:   attempt.IncidentID,
		GenerationID: attempt.GenerationID,
		Target:       attempt.Target(),
		RungIndex:    attempt.RungIndex,
		AttemptSeq:   attempt.Seq,
		Reused:       reused,
	}
}
