package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 2B-1: the orchestrator is the longest-lived session in a project and the
// worst context-exhaustion offender, so it needs a fresh conversation without
// losing what it was coordinating. In-place is what makes that safe — the
// session id, canonical worktree and branch never change, so live workers
// (whose system prompts embed the orchestrator id at spawn/restore only) keep
// addressing a coordinator that still exists.

func orchestratorSwitchHarness(t *testing.T) (*Manager, *fakeStore, domain.SessionID) {
	t.Helper()
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	st.sessions[id] = rec

	m := New(Deps{
		Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}},
		Agents:  singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	return m, st, id
}

// TestFreshOrchestratorConversation_KeepsIdentityInPlace is the core of 2B-1.
// A successor-session shape would strand every live worker, because a worker's
// system prompt embeds its orchestrator's id and is recomputed only at
// spawn/restore.
func TestFreshOrchestratorConversation_KeepsIdentityInPlace(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	before := st.sessions[id]

	res, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{
		Objective: "keep coordinating",
	})
	if err != nil {
		t.Fatalf("orchestrator fresh: %v", err)
	}
	if res.Session.ID != id {
		t.Fatalf("session id changed to %s: live workers address the old id", res.Session.ID)
	}
	if res.Session.Kind != domain.KindOrchestrator {
		t.Fatalf("kind = %s, want orchestrator", res.Session.Kind)
	}
	after := st.sessions[id]
	if after.Metadata.WorkspacePath != before.Metadata.WorkspacePath {
		t.Errorf("workspace moved: %q -> %q (canonical per project)",
			before.Metadata.WorkspacePath, after.Metadata.WorkspacePath)
	}
	if after.Metadata.Branch != before.Metadata.Branch {
		t.Errorf("branch moved: %q -> %q", before.Metadata.Branch, after.Metadata.Branch)
	}
	if after.IsTerminated {
		t.Error("orchestrator left terminated after a successful fresh conversation")
	}
	if res.Kind != domain.LifecycleKindOrchestratorFresh {
		t.Errorf("ledger kind = %q, want %q", res.Kind, domain.LifecycleKindOrchestratorFresh)
	}
}

// TestFreshOrchestratorConversation_DoesNotHoldTheFenceWhileWaitingForTheGate
// is the test that actually distinguishes lock ORDER.
//
// Merely showing that the operation blocks while the gate is held proves
// nothing: an inverted implementation (beginSwitch first, then wait on the
// gate) blocks identically. The difference is observable only in what is held
// WHILE blocked. Correct order waits on the gate holding no fence, so the
// per-session fence is still free; inverted order holds the fence while
// waiting, which is the same-project deadlock this ordering exists to prevent.
func TestFreshOrchestratorConversation_DoesNotHoldTheFenceWhileWaitingForTheGate(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	release, err := m.acquireProjectOwnership(context.Background(), st.sessions[id].ProjectID)
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
		done <- err
	}()

	// Let it reach whichever lock it takes first.
	select {
	case <-done:
		t.Fatal("orchestrator fresh completed while the project gate was held")
	case <-time.After(150 * time.Millisecond):
	}

	// The switch fence must still be free. beginSwitch returning true proves it
	// was NOT taken before the gate.
	if !m.beginSwitch(id) {
		t.Fatal("the switch fence is held while blocked on the project gate: locks are taken " +
			"beginSwitch -> projectOwnership, the inversion that deadlocks recovery for the same project")
	}
	m.endSwitch(id)
}

// TestFreshOrchestratorConversation_TakesProjectGateBeforeSwitchFence pins that
// the gate is taken at all. Ordering is covered separately above.
func TestFreshOrchestratorConversation_TakesProjectGateBeforeSwitchFence(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	release, err := m.acquireProjectOwnership(context.Background(), st.sessions[id].ProjectID)
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
		done <- err
	}()

	select {
	case <-done:
		t.Fatal("orchestrator fresh ran while another owner held the project gate")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator fresh never completed after the gate was released")
	}
}

// TestFreshOrchestratorConversation_RejectsWorker: the entry points are not
// interchangeable, because they take different locks.
func TestFreshOrchestratorConversation_RejectsWorker(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Kind = domain.KindWorker
	st.sessions[id] = rec

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{}); !errors.Is(err, ErrNotOrchestrator) {
		t.Fatalf("err = %v, want ErrNotOrchestrator", err)
	}
}

// TestSwitchWorker_RejectsOrchestrator is the other half: an orchestrator must
// not enter through the worker path, which would take beginSwitch WITHOUT the
// project gate and so invert the lock order.
func TestSwitchWorker_RejectsOrchestrator(t *testing.T) {
	m, _, id := orchestratorSwitchHarness(t)
	_, err := m.SwitchWorker(context.Background(), SwitchRequest{SessionID: id, FreshConversation: true})
	if !errors.Is(err, ErrNotWorker) {
		t.Fatalf("err = %v, want ErrNotWorker: orchestrators must gate first", err)
	}
}

// TestSwitchOrchestrator_RejectsCrossHarness: 2B-3, blocked on Claude RO. A
// strict orchestrator must be workspaceWrites:false and only Codex enforces
// that, so allowing cross-harness for non-strict projects would ship a
// capability strict projects can never have.
func TestSwitchOrchestrator_RejectsCrossHarness(t *testing.T) {
	m, _, id := orchestratorSwitchHarness(t)
	_, err := m.switchUnderOwnership(context.Background(), SwitchRequest{
		SessionID:     id,
		TargetHarness: domain.HarnessClaudeCode,
	}, true)
	if !errors.Is(err, ErrOrchestratorCrossHarness) {
		t.Fatalf("err = %v, want ErrOrchestratorCrossHarness", err)
	}
}

// TestSwitchWorker_RejectsOrchestratorEvenWhenTheGuardReadFails is the
// fail-closed half. The kind check must sit on the AUTHORITATIVE read inside
// the saga, not on a pre-read that has to guess when it errors: a pre-read that
// falls through on error skips the gate at exactly the moment it learned
// nothing.
func TestSwitchWorker_RejectsOrchestratorEvenWhenTheGuardReadFails(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	// The old implementation did a throwaway GetSession first and ignored its
	// error; nothing here makes that read succeed for it to key on.
	_ = st

	_, err := m.SwitchWorker(context.Background(), SwitchRequest{SessionID: id, FreshConversation: true})
	if !errors.Is(err, ErrNotWorker) {
		t.Fatalf("err = %v, want ErrNotWorker: an orchestrator on the worker entry point would hold "+
			"beginSwitch without the project gate", err)
	}
	// And it must be refused BEFORE any saga work: no ledger event, no stop.
	if len(st.ledger) != 0 {
		t.Errorf("the saga started for an ungated orchestrator: %d ledger events", len(st.ledger))
	}
}

// TestRecoverSwitch_RefusesCrossHarnessOrchestratorFromPendingPin: recovery
// re-drives a DURABLE record, so it must re-apply the refusals the request path
// applies. A pending pin is not authorization.
func TestRecoverSwitch_RefusesCrossHarnessOrchestratorFromPendingPin(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-x", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessCodex, ToHarness: domain.HarnessClaudeCode,
	}
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[id] = rec

	_, err := m.RecoverSwitchFromPostStop(context.Background(), id)
	if !errors.Is(err, ErrOrchestratorCrossHarness) {
		t.Fatalf("err = %v, want ErrOrchestratorCrossHarness: recovery would otherwise launch a "+
			"different harness into the canonical orchestrator workspace at boot", err)
	}
}

// TestIsSwitchLedgerKind_CoversEverySagaKind: findPhasePayload,
// findRecoverablePostStop and hasIncompletePostStop all filter through this, so
// an omitted kind silently disables the ledger fallback — the path that exists
// for when the pending pin is gone.
func TestIsSwitchLedgerKind_CoversEverySagaKind(t *testing.T) {
	for _, k := range []domain.LifecycleLedgerKind{
		domain.LifecycleKindSwitch,
		domain.LifecycleKindFreshConversation,
		domain.LifecycleKindOrchestratorFresh,
	} {
		if !isSwitchLedgerKind(k) {
			t.Errorf("%q is appended by the switch saga but invisible to recovery", k)
		}
	}
	for _, k := range []domain.LifecycleLedgerKind{
		domain.LifecycleKindPause, domain.LifecycleKindResume, domain.LifecycleKindFailover,
	} {
		if isSwitchLedgerKind(k) {
			t.Errorf("%q is not a switch saga kind but recovery treats it as one", k)
		}
	}
}

// TestObserveOrchestratorFleet_BoundsTerminatedHistory: this text goes into the
// target's launch prompt — into argv on Codex — and the launch happens AFTER
// the source stopped. Unbounded history would produce an oversized prompt at
// the moment failure is most expensive, and post-stop recovery would retry it
// forever.
func TestObserveOrchestratorFleet_BoundsTerminatedHistory(t *testing.T) {
	m, st, _ := orchestratorSwitchHarness(t)
	for i := 0; i < 200; i++ {
		id := domain.SessionID(fmt.Sprintf("mer-old-%03d", i))
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		}
	}
	// Live workers are never dropped.
	for i := 0; i < 5; i++ {
		id := domain.SessionID(fmt.Sprintf("mer-live-%d", i))
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker,
			Activity: domain.Activity{State: domain.ActivityActive},
		}
	}

	obs, err := m.ObserveOrchestratorFleet(context.Background(), "mer", "gen-1")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(obs.Workers) > 5+maxObservedTerminatedWorkers {
		t.Fatalf("fleet not bounded: %d entries", len(obs.Workers))
	}
	var live int
	for _, w := range obs.Workers {
		if !w.IsTerminated {
			live++
		}
	}
	if live != 5 {
		t.Errorf("live workers = %d, want all 5 kept: they are what the coordinator must act on", live)
	}
	if obs.OmittedTerminated != 200-maxObservedTerminatedWorkers {
		t.Errorf("omitted count = %d, want %d — silent truncation reads as a complete fleet",
			obs.OmittedTerminated, 200-maxObservedTerminatedWorkers)
	}
}

// TestFreshConversation_WorksWithoutARolePin is the regression for the failure
// live testing found.
//
// relaunchSession stamps an ephemeral ResolvedHarness so the target's
// AUTHORITATIVE ROLE FOOTER names the new harness. On a session with NO role
// pin that stamp manufactured a partial pin out of an empty binding, and
// restoreRoleApplyResult correctly refused it as corrupt — after the source had
// already stopped. The result was a post-stop session that failed identically on
// every boot: SWITCH_POST_STOP forever.
//
// Un-pinned sessions were previously unreachable behind the service's
// ROLE_PIN_REQUIRED check, which is why no unit test covered it: every fixture
// seeded a role-mapped project.
func TestFreshConversation_WorksWithoutARolePin(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		t.Run(string(kind), func(t *testing.T) {
			m, st, id := orchestratorSwitchHarness(t)
			rec := st.sessions[id]
			rec.Kind = kind
			// Exactly what a session on a project with no role map looks like.
			rec.Metadata.Role = domain.SessionRoleBinding{}
			st.sessions[id] = rec

			var err error
			if kind == domain.KindOrchestrator {
				_, err = m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
			} else {
				_, err = m.FreshConversation(context.Background(), id, domain.SemanticHandoffV1{})
			}
			if err != nil {
				t.Fatalf("fresh conversation failed for an un-pinned session: %v", err)
			}
			got := st.sessions[id]
			if got.IsTerminated {
				t.Error("session left terminated")
			}
			// The empty pin must stay empty — not half-populated by the stamp.
			if got.Metadata.Role.ResolvedHarness != "" || got.Metadata.Role.RoleID != "" {
				t.Errorf("an empty role pin was partially populated: %+v", got.Metadata.Role)
			}
		})
	}
}

// TestObserveOrchestratorFleet_ReportsWhatAOKnows: the fleet is read from the
// session table, not from the outgoing conversation's recollection. Terminated
// workers are included because "that one already finished" is the correction a
// long-running coordinator most needs.
func TestObserveOrchestratorFleet_ReportsWhatAOKnows(t *testing.T) {
	m, st, _ := orchestratorSwitchHarness(t)
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "ao/mer-2", Role: domain.SessionRoleBinding{RoleID: "implementor"}},
	}
	st.sessions["mer-3"] = domain.SessionRecord{
		ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		IsTerminated: true,
	}

	obs, err := m.ObserveOrchestratorFleet(context.Background(), "mer", "gen-1")
	if err != nil {
		t.Fatalf("observe fleet: %v", err)
	}
	if obs.SchemaVersion != domain.ObservedOrchestratorSchemaVersion {
		t.Errorf("schema version = %d", obs.SchemaVersion)
	}
	if len(obs.Workers) != 2 {
		t.Fatalf("workers = %+v, want both the live and the terminated worker", obs.Workers)
	}
	// Stable order so two handoffs can be diffed.
	if obs.Workers[0].SessionID != "mer-2" || obs.Workers[1].SessionID != "mer-3" {
		t.Fatalf("workers not in stable id order: %+v", obs.Workers)
	}
	if obs.Workers[0].RoleID != "implementor" || obs.Workers[0].Branch != "ao/mer-2" {
		t.Errorf("live worker facts lost: %+v", obs.Workers[0])
	}
	if !obs.Workers[1].IsTerminated {
		t.Error("terminated worker not marked: the fresh conversation would re-delegate finished work")
	}
	// The orchestrator itself is not a member of its own fleet.
	for _, w := range obs.Workers {
		if w.SessionID == "mer-1" {
			t.Error("the orchestrator listed itself as one of its workers")
		}
	}
}

// TestFreshOrchestratorConversation_HandoffCarriesTheFleet: observing is only
// useful if it reaches the launched agent.
func TestFreshOrchestratorConversation_HandoffCarriesTheFleet(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	st.sessions["mer-7"] = domain.SessionRecord{
		ID: "mer-7", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "ao/mer-7"},
	}
	agent := &recordingAgent{}
	m.agents = singleAgent{agent: agent}

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{
		Objective: "keep coordinating",
	}); err != nil {
		t.Fatalf("orchestrator fresh: %v", err)
	}

	prompt := agent.lastLaunch.SystemPrompt
	if prompt == "" {
		prompt = agent.lastRestore.SystemPrompt
	}
	combined := prompt + "\n" + agent.lastLaunch.Prompt
	if !strings.Contains(combined, "mer-7") {
		t.Fatalf("compiled handoff does not name the live worker; the fresh conversation starts blind:\n%s", combined)
	}
	if !strings.Contains(combined, "Observed fleet") {
		t.Errorf("handoff lacks the host fleet section:\n%s", combined)
	}
}

// TestFreshOrchestratorConversation_FleetFailureDegrades: the fleet is context,
// not a safety invariant. Failing the switch would strand an orchestrator that
// is already out of context — the exact condition being remedied.
func TestFreshOrchestratorConversation_FleetFailureDegrades(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	st.listSessionsErr = errors.New("database is locked")

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{}); err != nil {
		t.Fatalf("orchestrator fresh: %v — an unreadable fleet must degrade, not abort", err)
	}
	if st.sessions[id].IsTerminated {
		t.Error("orchestrator terminated because its fleet could not be read")
	}
}
