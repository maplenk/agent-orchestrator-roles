package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type adversarialSwitchCommander struct {
	*fakeCommander
	canonicalSwitchCalls int
}

func (m *adversarialSwitchCommander) SwitchAgent(_ context.Context, id domain.SessionID, cfg sessionmanager.SwitchAgentConfig) (domain.AgentSwitch, error) {
	m.canonicalSwitchCalls++
	return domain.AgentSwitch{ID: "unauthorized-mutation", SessionID: id, TargetHarness: cfg.TargetHarness}, nil
}

// The canonical durable-engine entry point must apply the same fork policy as
// the compatibility endpoint. An unauthorized intent is rejected before the
// manager (the mutation boundary) and leaves durable session/project state
// byte-for-byte unchanged.
func TestCanonicalAgentSwitchAdversarialUnauthorizedIntentHasZeroMutation(t *testing.T) {
	store := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(store, id, domain.HarnessClaudeCode)
	beforeSession := store.sessions[id]
	beforeProject := store.projects["mer"]
	manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}}
	service := NewWithDeps(Deps{Manager: manager, Store: store})

	_, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
		TargetHarness: domain.HarnessPi, IdempotencyKey: "unauthorized-canonical-intent",
	})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "SWITCH_TARGET_UNAUTHORIZED" {
		t.Fatalf("error = %v, want SWITCH_TARGET_UNAUTHORIZED", err)
	}
	if manager.canonicalSwitchCalls != 0 {
		t.Fatalf("unauthorized intent reached durable engine %d times", manager.canonicalSwitchCalls)
	}
	if got := store.sessions[id]; !reflect.DeepEqual(got, beforeSession) {
		t.Fatalf("unauthorized intent mutated session:\nbefore: %+v\nafter:  %+v", beforeSession, got)
	}
	if got := store.projects["mer"]; !reflect.DeepEqual(got, beforeProject) {
		t.Fatalf("unauthorized intent mutated project:\nbefore: %+v\nafter:  %+v", beforeProject, got)
	}
}

func TestCanonicalAgentSwitchAdversarialOrchestratorNeverEntersWorkerEngine(t *testing.T) {
	store := newFakeStore()
	id := domain.SessionID("mer-orchestrator")
	seedSwitchSession(store, id, domain.HarnessClaudeCode)
	rec := store.sessions[id]
	rec.Kind = domain.KindOrchestrator
	store.sessions[id] = rec
	before := store.sessions[id]
	manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}}
	service := NewWithDeps(Deps{Manager: manager, Store: store})

	_, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "orchestrator-canonical-intent",
	})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "WORKER_SESSION_REQUIRED" {
		t.Fatalf("error = %v, want WORKER_SESSION_REQUIRED", err)
	}
	if manager.canonicalSwitchCalls != 0 {
		t.Fatalf("orchestrator entered worker engine %d times", manager.canonicalSwitchCalls)
	}
	if got := store.sessions[id]; !reflect.DeepEqual(got, before) {
		t.Fatalf("orchestrator refusal mutated session:\nbefore: %+v\nafter:  %+v", before, got)
	}
}
