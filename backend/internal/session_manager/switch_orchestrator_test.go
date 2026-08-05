package sessionmanager

import (
	"context"
	"errors"
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

// TestFreshOrchestratorConversation_TakesProjectGateBeforeSwitchFence pins the
// lock order the manager's ownership boundary depends on: projectOwnership ->
// beginSwitch. Without the gate, EnsureOrchestrator could retire and replace
// this very session mid-saga.
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
	})
	if !errors.Is(err, ErrOrchestratorCrossHarness) {
		t.Fatalf("err = %v, want ErrOrchestratorCrossHarness", err)
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
