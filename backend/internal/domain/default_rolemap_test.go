package domain

import "testing"

func TestStarterRoleMapPreservesLegacyWorkersAndEnablesOrchestratorSwitch(t *testing.T) {
	m := StarterRoleMap(
		FailoverTarget{Harness: HarnessClaudeCode},
		FailoverTarget{Harness: HarnessClaudeCode, Model: "worker-model"},
	)
	if m.StrictDelegation {
		t.Fatal("starter map must not make existing worker commands strict")
	}
	if got := m.Roles[DefaultOrchestratorRoleID]; got.Harness != HarnessClaudeCode || !got.Permissions.WorkspaceWrites || !got.Permissions.CanSpawn {
		t.Fatalf("orchestrator binding = %+v", got)
	}
	if got := m.Roles[DefaultImplementorRoleID]; got.Model != "worker-model" || !got.Permissions.WorkspaceWrites || got.Permissions.CanSpawn {
		t.Fatalf("implementor binding = %+v", got)
	}
	targets := RoleAuthorizedSwitchTargets(m, DefaultOrchestratorRoleID)
	if len(targets) != 2 || targets[0].Harness != HarnessClaudeCode || targets[1].Harness != HarnessCodex {
		t.Fatalf("orchestrator targets = %+v", targets)
	}
	if roleID, ok := AdoptableOrchestratorRole(m, HarnessClaudeCode); !ok || roleID != DefaultOrchestratorRoleID {
		t.Fatalf("adoptable role = %q, %v", roleID, ok)
	}
}

func TestAdoptableOrchestratorRoleRefusesUnprovenLegacyIdentity(t *testing.T) {
	m := StarterRoleMap(
		FailoverTarget{Harness: HarnessClaudeCode, Model: "opus"},
		FailoverTarget{Harness: HarnessClaudeCode},
	)
	if roleID, ok := AdoptableOrchestratorRole(m, HarnessClaudeCode); ok || roleID != "" {
		t.Fatalf("model-specific legacy role was adoptable: %q, %v", roleID, ok)
	}
	if roleID, ok := AdoptableOrchestratorRole(m, HarnessCodex); ok || roleID != "" {
		t.Fatalf("wrong-harness legacy role was adoptable: %q, %v", roleID, ok)
	}
}
