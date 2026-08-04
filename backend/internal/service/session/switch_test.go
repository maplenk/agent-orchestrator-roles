package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

func seedSwitchSession(st *fakeStore, id domain.SessionID, harness domain.AgentHarness) {
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{
			RoleMap: domain.RoleMap{
				SchemaVersion: domain.RoleMapSchemaVersion,
				Roles: map[string]domain.RoleBinding{
					"implementor": {
						Template: "implementor", Harness: domain.HarnessClaudeCode,
						Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
					},
				},
				Failover: domain.FailoverConfig{
					Mode: domain.FailoverModeManual,
					Roles: map[string][]domain.FailoverTarget{
						"implementor": {{Harness: domain.HarnessCodex}},
					},
				},
			},
		},
	}
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: harness,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "rt-1", WorkspacePath: "/ws",
			Role: domain.SessionRoleBinding{RoleID: "implementor", ResolvedHarness: harness},
		},
	}
}

func TestSwitchWorker_AuthorizesFailoverTarget(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})

	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, Objective: "keep going",
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if cmd.switchCalls != 1 {
		t.Fatalf("switchCalls=%d", cmd.switchCalls)
	}
	if cmd.lastSwitch.TargetHarness != domain.HarnessCodex {
		t.Fatalf("target=%q", cmd.lastSwitch.TargetHarness)
	}
	if out.GenerationID != "gen-sw-1" || out.Kind != domain.LifecycleKindSwitch {
		t.Fatalf("out=%+v", out)
	}
}

func TestSwitchWorker_RejectsUnauthorizedHarness(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessPi,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_TARGET_UNAUTHORIZED" {
		t.Fatalf("err=%v want SWITCH_TARGET_UNAUTHORIZED", err)
	}
}

func TestSwitchWorker_RequiresRoleMap(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{RoleID: "implementor"}},
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "ROLE_MAP_REQUIRED" {
		t.Fatalf("err=%v want ROLE_MAP_REQUIRED", err)
	}
}

func TestSwitchWorker_MapsNotSupported(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{switchErr: sessionmanager.ErrSwitchNotSupported}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_NOT_SUPPORTED" {
		t.Fatalf("err=%v want SWITCH_NOT_SUPPORTED", err)
	}
}

func TestFreshConversation_NoFreeFormHarness(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessCodex)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	out, err := svc.FreshConversation(context.Background(), id, "refresh context")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.freshCalls != 1 || cmd.switchCalls != 0 {
		t.Fatalf("fresh=%d switch=%d", cmd.freshCalls, cmd.switchCalls)
	}
	if out.Kind != domain.LifecycleKindFreshConversation {
		t.Fatalf("kind=%s", out.Kind)
	}
}

func TestSwitchWorker_SameHarnessIsFresh(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.freshCalls != 1 || cmd.switchCalls != 0 {
		t.Fatalf("fresh=%d switch=%d", cmd.freshCalls, cmd.switchCalls)
	}
}

func TestSwitchWorker_EmptyConfiguredModelRejectsExplicitModel(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	// failover codex has empty model (= provider default only)
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "o3",
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_TARGET_UNAUTHORIZED" {
		t.Fatalf("err=%v want SWITCH_TARGET_UNAUTHORIZED", err)
	}
}

func TestSwitchWorker_AmbiguousModelRequiresExplicit(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	proj := st.projects["mer"]
	proj.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "o3"},
		{Harness: domain.HarnessCodex, Model: "o4"},
	}
	st.projects["mer"] = proj
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})

	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "TARGET_MODEL_REQUIRED" {
		t.Fatalf("err=%v want TARGET_MODEL_REQUIRED", err)
	}

	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "o3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.lastSwitch.TargetModel != "o3" {
		t.Fatalf("model=%q", cmd.lastSwitch.TargetModel)
	}
	if out.GenerationID == "" {
		t.Fatal("missing generation")
	}
}
