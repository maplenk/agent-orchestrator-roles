package session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type agentSwitchOptionsStore struct {
	*fakeStore
	active    domain.AgentSwitch
	activeErr error
}

func (s *agentSwitchOptionsStore) GetActiveAgentSwitch(
	_ context.Context, id domain.SessionID,
) (domain.AgentSwitch, bool, error) {
	if s.activeErr != nil {
		return domain.AgentSwitch{}, false, s.activeErr
	}
	if s.active.SessionID != id || s.active.State.Terminal() {
		return domain.AgentSwitch{}, false, nil
	}
	return s.active, true, nil
}

func TestAgentSwitchOptionsReturnsExactSupportedRoleTargets(t *testing.T) {
	base := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(base, id, domain.HarnessClaudeCode)
	project := base.projects["mer"]
	project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "gpt-5"},
		{Harness: domain.HarnessPi, Model: "pi-model"},
		{Harness: domain.HarnessCodex, Model: "o3"},
		{Harness: domain.HarnessClaudeCode, Model: "same-provider"},
	}
	base.projects["mer"] = project
	service := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: &agentSwitchOptionsStore{fakeStore: base}})

	options, err := service.AgentSwitchOptions(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	wantTargets := []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "gpt-5"},
		{Harness: domain.HarnessCodex, Model: "o3"},
	}
	if !options.Available || options.Reason != "" || options.RoleID != "implementor" {
		t.Fatalf("options = %+v, want available implementor choices", options)
	}
	if options.Current.Harness != domain.HarnessClaudeCode || !reflect.DeepEqual(options.Targets, wantTargets) {
		t.Fatalf("options = %+v, want targets %+v", options, wantTargets)
	}
}

func TestAgentSwitchOptionsUnavailableReasons(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*fakeStore, domain.SessionID)
		active     domain.AgentSwitch
		wantReason string
	}{
		{
			name: "orchestrator",
			mutate: func(store *fakeStore, id domain.SessionID) {
				rec := store.sessions[id]
				rec.Kind = domain.KindOrchestrator
				store.sessions[id] = rec
			},
			wantReason: agentSwitchOptionsReasonWorkerRequired,
		},
		{
			name: "terminated",
			mutate: func(store *fakeStore, id domain.SessionID) {
				rec := store.sessions[id]
				rec.IsTerminated = true
				store.sessions[id] = rec
			},
			wantReason: agentSwitchOptionsReasonTerminated,
		},
		{
			name: "paused",
			mutate: func(store *fakeStore, id domain.SessionID) {
				rec := store.sessions[id]
				rec.Metadata.Pause = &domain.SessionPause{IncidentID: "incident-1"}
				store.sessions[id] = rec
			},
			wantReason: agentSwitchOptionsReasonPaused,
		},
		{
			name: "active saga",
			active: domain.AgentSwitch{
				ID: "switch-active", SessionID: "mer-1", State: domain.AgentSwitchDelivering,
			},
			wantReason: agentSwitchOptionsReasonInProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newFakeStore()
			id := domain.SessionID("mer-1")
			seedSwitchSession(base, id, domain.HarnessClaudeCode)
			if tt.mutate != nil {
				tt.mutate(base, id)
			}
			service := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: &agentSwitchOptionsStore{
				fakeStore: base, active: tt.active,
			}})

			options, err := service.AgentSwitchOptions(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if options.Available || options.Reason != tt.wantReason || len(options.Targets) != 0 {
				t.Fatalf("options = %+v, want unavailable reason %q", options, tt.wantReason)
			}
		})
	}
}

func TestAgentSwitchOptionsActiveSagaReadFailureIsFailClosed(t *testing.T) {
	base := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(base, id, domain.HarnessClaudeCode)
	service := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: &agentSwitchOptionsStore{
		fakeStore: base, activeErr: errors.New("injected active saga read failure"),
	}})

	_, err := service.AgentSwitchOptions(context.Background(), id)
	if err == nil {
		t.Fatal("expected active saga read failure")
	}
}
