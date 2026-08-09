package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestCanonicalAgentSwitchPolicyAuthorizedTargetReachesEngineOnce(t *testing.T) {
	store := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(store, id, domain.HarnessClaudeCode)
	manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}}
	service := NewWithDeps(Deps{Manager: manager, Store: store})

	sw, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "authorized-canonical-intent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if manager.canonicalSwitchCalls != 1 {
		t.Fatalf("canonical switch calls = %d, want 1", manager.canonicalSwitchCalls)
	}
	if sw.TargetHarness != domain.HarnessCodex {
		t.Fatalf("target harness = %q, want codex", sw.TargetHarness)
	}
}

func TestCanonicalAgentSwitchPolicyFailsClosedBeforeEngine(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*fakeStore, domain.SessionID)
		target   domain.AgentHarness
		wantCode string
	}{
		{
			name: "missing role pin",
			mutate: func(store *fakeStore, id domain.SessionID) {
				rec := store.sessions[id]
				rec.Metadata.Role.RoleID = ""
				store.sessions[id] = rec
			},
			target: domain.HarnessCodex, wantCode: "ROLE_PIN_REQUIRED",
		},
		{
			name: "role map absent",
			mutate: func(store *fakeStore, _ domain.SessionID) {
				project := store.projects["mer"]
				project.Config.RoleMap = domain.RoleMap{}
				store.projects["mer"] = project
			},
			target: domain.HarnessCodex, wantCode: "ROLE_MAP_REQUIRED",
		},
		{
			name: "role removed",
			mutate: func(store *fakeStore, _ domain.SessionID) {
				project := store.projects["mer"]
				project.Config.RoleMap.Roles = map[string]domain.RoleBinding{}
				store.projects["mer"] = project
			},
			target: domain.HarnessCodex, wantCode: "ROLE_NOT_IN_MAP",
		},
		{
			name: "ambiguous target models",
			mutate: func(store *fakeStore, _ domain.SessionID) {
				project := store.projects["mer"]
				project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
					{Harness: domain.HarnessCodex, Model: "gpt-5"},
					{Harness: domain.HarnessCodex, Model: "o3"},
				}
				store.projects["mer"] = project
			},
			target: domain.HarnessCodex, wantCode: "TARGET_MODEL_REQUIRED",
		},
		{
			name: "paused worker",
			mutate: func(store *fakeStore, id domain.SessionID) {
				rec := store.sessions[id]
				rec.Metadata.Pause = &domain.SessionPause{IncidentID: "incident-1"}
				store.sessions[id] = rec
			},
			target: domain.HarnessCodex, wantCode: "SWITCH_PAUSED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			id := domain.SessionID("mer-1")
			seedSwitchSession(store, id, domain.HarnessClaudeCode)
			tt.mutate(store, id)
			manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}}
			service := NewWithDeps(Deps{Manager: manager, Store: store})

			_, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
				TargetHarness: tt.target, IdempotencyKey: "refused-canonical-intent",
			})
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code != tt.wantCode {
				t.Fatalf("error = %v, want %s", err, tt.wantCode)
			}
			if manager.canonicalSwitchCalls != 0 {
				t.Fatalf("refused intent reached durable engine %d times", manager.canonicalSwitchCalls)
			}
		})
	}
}
