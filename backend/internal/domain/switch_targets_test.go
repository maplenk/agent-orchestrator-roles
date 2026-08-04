package domain

import "testing"

func TestRoleAuthorizedSwitchTargets(t *testing.T) {
	m := RoleMap{
		SchemaVersion: RoleMapSchemaVersion,
		Roles: map[string]RoleBinding{
			"implementor": {Template: "implementor", Harness: HarnessClaudeCode, Model: "sonnet"},
		},
		Failover: FailoverConfig{
			Mode: FailoverModeManual,
			Roles: map[string][]FailoverTarget{
				"implementor": {
					{Harness: HarnessCodex, Model: "o3"},
					{Harness: HarnessClaudeCode, Model: "sonnet"}, // duplicate primary
				},
			},
		},
	}
	got := RoleAuthorizedSwitchTargets(m, "implementor")
	if len(got) != 2 {
		t.Fatalf("targets = %+v, want 2 unique", got)
	}
	if !SwitchTargetAuthorized(m, "implementor", HarnessCodex, "") {
		t.Fatal("codex should be authorized without model")
	}
	if !SwitchTargetAuthorized(m, "implementor", HarnessCodex, "o3") {
		t.Fatal("codex+o3 should be authorized")
	}
	if SwitchTargetAuthorized(m, "implementor", HarnessCodex, "wrong-model") {
		t.Fatal("codex+wrong-model must not be authorized")
	}
	if SwitchTargetAuthorized(m, "implementor", HarnessPi, "") {
		t.Fatal("pi not on map")
	}
	if SwitchTargetAuthorized(m, "reviewer", HarnessCodex, "") {
		t.Fatal("unknown role")
	}
	if SwitchTargetAuthorized(RoleMap{}, "implementor", HarnessCodex, "") {
		t.Fatal("empty role map")
	}
}
