package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// switch_pending_json remains authoritative for recovery of a legacy in-flight
// worker switch. A new request must not overwrite the pin, touch the runtime,
// or write another lifecycle event.
func TestPolicyCharacterization_LegacySwitchPendingOwnsRecovery(t *testing.T) {
	store := newFakeStore()
	workspace := t.TempDir()
	artifact, sha := pinImplementorTemplate(t, store)
	id := domain.SessionID("mer-1")
	workerSession(store, id, domain.HarnessClaudeCode, workspace, artifact, sha)
	record := store.sessions[id]
	record.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "legacy-target-generation",
		Kind:         domain.LifecycleKindSwitch,
		FromHarness:  domain.HarnessClaudeCode,
		ToHarness:    domain.HarnessCodex,
	}
	store.sessions[id] = record
	runtime := &fakeRuntime{}
	manager := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: store},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	manager.switchCapsOverride = testSwitchCaps

	_, err := manager.SwitchWorker(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	if !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("error = %v, want legacy recovery-required switch conflict", err)
	}
	after := store.sessions[id]
	if after.Metadata.SwitchPending == nil || after.Metadata.SwitchPending.GenerationID != "legacy-target-generation" {
		t.Fatalf("legacy pending ownership changed: %+v", after.Metadata.SwitchPending)
	}
	if runtime.created != 0 || runtime.destroyed != 0 || len(store.ledger) != 0 || store.updateCount != 0 {
		t.Fatalf(
			"refused request had effects: created=%d destroyed=%d ledger=%d updates=%d",
			runtime.created,
			runtime.destroyed,
			len(store.ledger),
			store.updateCount,
		)
	}
}

// The merged tree temporarily contains the fork switch path and the upstream
// durable engine. Both worker entry points must reject orchestrators before a
// saga begins; the orchestrator entry point must likewise reject workers.
func TestPolicyCharacterization_WorkerAndOrchestratorRoutesStayDisjoint(t *testing.T) {
	t.Run("fork worker entry rejects orchestrator", func(t *testing.T) {
		manager, store, id := orchestratorSwitchHarness(t)
		_, err := manager.SwitchWorker(context.Background(), SwitchRequest{
			SessionID: id, FreshConversation: true,
		})
		if !errors.Is(err, ErrNotWorker) {
			t.Fatalf("error = %v, want ErrNotWorker", err)
		}
		if len(store.ledger) != 0 || manager.runtime.(*fakeRuntime).destroyed != 0 {
			t.Fatalf("worker route mutated orchestrator ownership: ledger=%d runtime=%+v", len(store.ledger), manager.runtime)
		}
	})

	t.Run("durable worker engine rejects orchestrator", func(t *testing.T) {
		runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
		manager, store, _ := newSwitchTestManager(t, runtime)
		record := store.sessions["proj-1"]
		record.Kind = domain.KindOrchestrator
		store.sessions[record.ID] = record

		_, err := manager.SwitchAgent(context.Background(), record.ID, SwitchAgentConfig{
			TargetHarness: domain.HarnessCodex, IdempotencyKey: "orchestrator-policy-characterization",
		})
		if !errors.Is(err, ErrUnsupportedSwitchKind) {
			t.Fatalf("error = %v, want ErrUnsupportedSwitchKind", err)
		}
		if len(store.switches) != 0 || runtime.restarted != 0 || runtime.destroyed != 0 {
			t.Fatalf(
				"durable worker engine mutated orchestrator ownership: switches=%d restarts=%d destroys=%d",
				len(store.switches),
				runtime.restarted,
				runtime.destroyed,
			)
		}
	})

	t.Run("orchestrator entry rejects worker", func(t *testing.T) {
		manager, store, id := orchestratorSwitchHarness(t)
		record := store.sessions[id]
		record.Kind = domain.KindWorker
		store.sessions[id] = record

		_, err := manager.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
		if !errors.Is(err, ErrNotOrchestrator) {
			t.Fatalf("error = %v, want ErrNotOrchestrator", err)
		}
		if len(store.ledger) != 0 || manager.runtime.(*fakeRuntime).destroyed != 0 {
			t.Fatalf("orchestrator route mutated worker ownership: ledger=%d runtime=%+v", len(store.ledger), manager.runtime)
		}
	})
}
