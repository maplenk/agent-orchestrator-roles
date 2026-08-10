package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Authorization is a policy-adapter concern and must complete before either
// switch implementation receives a mutating command. This matrix deliberately
// distinguishes provider-default authorization from an explicit model.
func TestPolicyCharacterization_RoleModelAuthorizationPrecedesManagerMutation(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*fakeStore, domain.SessionID)
		target      domain.AgentHarness
		model       string
		wantAPIcode string
	}{
		{
			name:        "harness absent from role ladder",
			target:      domain.HarnessPi,
			wantAPIcode: "SWITCH_TARGET_UNAUTHORIZED",
		},
		{
			name:        "provider default is not a wildcard for explicit model",
			target:      domain.HarnessCodex,
			model:       "o3",
			wantAPIcode: "SWITCH_TARGET_UNAUTHORIZED",
		},
		{
			name: "role map unavailable",
			mutate: func(st *fakeStore, _ domain.SessionID) {
				project := st.projects["mer"]
				project.Config.RoleMap = domain.RoleMap{}
				st.projects["mer"] = project
			},
			target:      domain.HarnessCodex,
			wantAPIcode: "ROLE_MAP_REQUIRED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newFakeStore()
			id := domain.SessionID("mer-1")
			seedSwitchSession(store, id, domain.HarnessClaudeCode)
			if tt.mutate != nil {
				tt.mutate(store, id)
			}
			manager := &fakeCommander{}
			service := NewWithDeps(Deps{Manager: manager, Store: store})

			_, err := service.SwitchWorker(context.Background(), SwitchWorkerRequest{
				SessionID: id, TargetHarness: tt.target, TargetModel: tt.model,
			})
			var apiError *apierr.Error
			if !errors.As(err, &apiError) || apiError.Code != tt.wantAPIcode {
				t.Fatalf("error = %v, want API code %s", err, tt.wantAPIcode)
			}
			if manager.switchCalls != 0 || manager.orchestratorSwitchCalls != 0 ||
				manager.freshCalls != 0 || manager.orchestratorFreshCalls != 0 {
				t.Fatalf(
					"unauthorized request reached manager: worker=%d orchestrator=%d fresh=%d orchestrator-fresh=%d",
					manager.switchCalls,
					manager.orchestratorSwitchCalls,
					manager.freshCalls,
					manager.orchestratorFreshCalls,
				)
			}
		})
	}
}
