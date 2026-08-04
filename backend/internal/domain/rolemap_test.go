package domain

import (
	"strings"
	"testing"
)

func sampleStrictMap() RoleMap {
	return RoleMap{
		SchemaVersion:    RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]RoleBinding{
			"orchestrator": {
				Template: "orchestrator",
				Harness:  HarnessClaudeCode,
				Permissions: RoleExecutionPolicy{
					WorkspaceWrites: false,
					CanSpawn:        true,
				},
			},
			"implementor": {
				Template: "implementor",
				Harness:  HarnessCodex,
				Permissions: RoleExecutionPolicy{
					WorkspaceWrites: true,
					CanSpawn:        false,
				},
				When: []string{"implementation", "bugfix"},
			},
			"ui": {
				Template: "ui-implementor",
				Harness:  HarnessPi,
				Model:    "zai/test",
				Permissions: RoleExecutionPolicy{
					WorkspaceWrites: true,
					CanSpawn:        false,
				},
			},
		},
		Failover: FailoverConfig{
			Mode: FailoverModeManual,
			Roles: map[string][]FailoverTarget{
				"implementor": {
					{Harness: HarnessPi, Model: "kimi/test"},
				},
			},
		},
	}
}

func TestRoleMapValidate_OK(t *testing.T) {
	m := sampleStrictMap()
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRoleMapValidate_RejectsLiteralDefaultModel(t *testing.T) {
	m := sampleStrictMap()
	b := m.Roles["implementor"]
	b.Model = "default"
	m.Roles["implementor"] = b
	if err := m.Validate(); err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("err = %v, want default model rejection", err)
	}
}

func TestRoleMapValidate_StrictOrchestratorMustNotWrite(t *testing.T) {
	m := sampleStrictMap()
	b := m.Roles["orchestrator"]
	b.Permissions.WorkspaceWrites = true
	m.Roles["orchestrator"] = b
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for orchestrator workspaceWrites")
	}
}

func TestRoleMapValidate_StrictOrchestratorMustSpawn(t *testing.T) {
	m := sampleStrictMap()
	b := m.Roles["orchestrator"]
	b.Permissions.CanSpawn = false
	m.Roles["orchestrator"] = b
	if err := m.Validate(); err == nil {
		t.Fatal("expected error for orchestrator canSpawn false")
	}
}

func TestRoleMapValidate_UnknownHarness(t *testing.T) {
	m := sampleStrictMap()
	b := m.Roles["ui"]
	b.Harness = AgentHarness("not-a-real-harness")
	m.Roles["ui"] = b
	if err := m.Validate(); err == nil {
		t.Fatal("expected unknown harness error")
	}
}

func TestRoleMapValidate_FailoverRoleMustExist(t *testing.T) {
	m := sampleStrictMap()
	m.Failover.Roles["ghost"] = []FailoverTarget{{Harness: HarnessCodex}}
	if err := m.Validate(); err == nil {
		t.Fatal("expected missing failover role error")
	}
}

func TestRoleMapSHA256_Stable(t *testing.T) {
	m := sampleStrictMap()
	a, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a == "" {
		t.Fatalf("unstable or empty hash: %q vs %q", a, b)
	}
}

func TestRoleMapIsZero(t *testing.T) {
	if !(RoleMap{}).IsZero() {
		t.Fatal("empty map should be zero")
	}
	if sampleStrictMap().IsZero() {
		t.Fatal("sample should not be zero")
	}
}
