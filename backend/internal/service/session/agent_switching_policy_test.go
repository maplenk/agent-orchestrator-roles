package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type idempotentAgentSwitchPolicyStore struct {
	*fakeStore
	existing domain.AgentSwitch
}

func (s *idempotentAgentSwitchPolicyStore) GetAgentSwitchByIdempotencyKey(
	_ context.Context, id domain.SessionID, key string,
) (domain.AgentSwitch, bool, error) {
	if s.existing.SessionID == id && s.existing.IdempotencyKey == key {
		return s.existing, true, nil
	}
	return domain.AgentSwitch{}, false, nil
}

func TestCanonicalAgentSwitchPolicyAuthorizedTargetReachesEngineOnce(t *testing.T) {
	store := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(store, id, domain.HarnessClaudeCode)
	rec := store.sessions[id]
	rec.Metadata.RuntimeLaunchID = "source-generation-1"
	store.sessions[id] = rec
	project := store.projects["mer"]
	project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{{
		Harness: domain.HarnessCodex, Model: "gpt-5",
	}}
	store.projects["mer"] = project
	manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}}
	service := NewWithDeps(Deps{Manager: manager, Store: store})

	sw, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
		TargetHarness: domain.HarnessCodex, TargetModel: "gpt-5",
		IdempotencyKey: "authorized-canonical-intent",
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
	cfg := manager.lastCanonicalConfig
	if cfg.TargetModel != "gpt-5" || cfg.ExpectedSourceGenerationID != "source-generation-1" ||
		cfg.RoleSnapshot.RoleID != "implementor" || cfg.RoleSnapshot.ResolvedHarness != domain.HarnessCodex ||
		cfg.RoleSnapshot.ResolvedModel != "gpt-5" || cfg.RoleSnapshot.RoleMapSHA256 == "" ||
		cfg.AllocateTargetGeneration == nil {
		t.Fatalf("manager config did not carry exact authorized intent: %+v", cfg)
	}
}

func TestCanonicalAgentSwitchPolicyFailsClosedBeforeEngine(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*fakeStore, domain.SessionID)
		target   domain.AgentHarness
		model    string
		wantCode string
	}{
		{
			name: "explicit model not authorized",
			mutate: func(store *fakeStore, _ domain.SessionID) {
				project := store.projects["mer"]
				project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{{
					Harness: domain.HarnessCodex, Model: "gpt-5",
				}}
				store.projects["mer"] = project
			},
			target: domain.HarnessCodex, model: "o3", wantCode: "SWITCH_TARGET_UNAUTHORIZED",
		},
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
				TargetHarness: tt.target, TargetModel: tt.model,
				IdempotencyKey: "refused-canonical-intent",
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

func TestCanonicalAgentSwitchPolicyIdempotentRetryUsesDurableIntent(t *testing.T) {
	base := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(base, id, domain.HarnessClaudeCode)
	snapshot := domain.SessionRoleBinding{
		RoleID: "implementor", RoleMapSchemaVersion: domain.RoleMapSchemaVersion,
		RoleMapSHA256: "durable-map-sha", TemplateArtifactID: "artifact-1",
		TemplateSHA256: "template-sha", ResolvedHarness: domain.HarnessCodex,
		ResolvedModel: "gpt-5",
	}
	existing := domain.AgentSwitch{
		ID: "switch-existing", SessionID: id, IdempotencyKey: "stable-key",
		TargetHarness: domain.HarnessCodex, TargetModel: "gpt-5",
		SourceGenerationID: "original-source-generation", RoleSnapshot: snapshot,
		State: domain.AgentSwitchDelivering,
	}
	store := &idempotentAgentSwitchPolicyStore{fakeStore: base, existing: existing}
	// Policy changed after the saga became durable. Recovery must use the stored
	// immutable intent rather than trying to authorize it against this new map.
	project := base.projects["mer"]
	project.Config.RoleMap = domain.RoleMap{}
	base.projects["mer"] = project
	rec := base.sessions[id]
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: "newer-pause"}
	rec.Metadata.RuntimeLaunchID = "newer-source-generation"
	base.sessions[id] = rec
	manager := &adversarialSwitchCommander{fakeCommander: &fakeCommander{}, canonicalResult: existing}
	service := NewWithDeps(Deps{Manager: manager, Store: store})

	got, err := service.SwitchAgent(context.Background(), id, SwitchAgentInput{
		TargetHarness: domain.HarnessCodex, TargetModel: "gpt-5",
		Note: "same note", IdempotencyKey: "stable-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != existing.ID || manager.canonicalSwitchCalls != 1 {
		t.Fatalf("idempotent retry = %+v calls=%d", got, manager.canonicalSwitchCalls)
	}
	if cfg := manager.lastCanonicalConfig; cfg.ExpectedSourceGenerationID != existing.SourceGenerationID ||
		!reflect.DeepEqual(cfg.RoleSnapshot, snapshot) || cfg.TargetModel != existing.TargetModel {
		t.Fatalf("retry did not carry durable intent: %+v", cfg)
	}
}
