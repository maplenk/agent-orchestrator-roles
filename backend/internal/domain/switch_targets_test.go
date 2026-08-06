package domain

import (
	"errors"
	"testing"
)

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
}

func TestSwitchTargetAuthorized_ExactModel(t *testing.T) {
	m := RoleMap{
		SchemaVersion: RoleMapSchemaVersion,
		Roles: map[string]RoleBinding{
			"implementor": {Template: "implementor", Harness: HarnessClaudeCode},
		},
		Failover: FailoverConfig{
			Roles: map[string][]FailoverTarget{
				"implementor": {
					{Harness: HarnessCodex},           // provider default only
					{Harness: HarnessPi, Model: "k2"}, // fixed model
				},
			},
		},
	}

	// Empty configured model authorizes only provider default (empty request).
	if !SwitchTargetAuthorized(m, "implementor", HarnessCodex, "") {
		t.Fatal("codex + default should authorize")
	}
	if SwitchTargetAuthorized(m, "implementor", HarnessCodex, "o3") {
		t.Fatal("empty configured model must not wildcard explicit client models")
	}

	// Fixed model: exact match only. Unique fixed entry also resolves omitted → that model.
	if !SwitchTargetAuthorized(m, "implementor", HarnessPi, "k2") {
		t.Fatal("pi+k2 should authorize")
	}
	got, err := ResolveAuthorizedSwitchModel(m, "implementor", HarnessPi, "")
	if err != nil || got != "k2" {
		t.Fatalf("resolve pi omitted = %q err=%v, want k2", got, err)
	}
	if SwitchTargetAuthorized(m, "implementor", HarnessPi, "other") {
		t.Fatal("wrong model")
	}
	if SwitchTargetAuthorized(m, "implementor", HarnessCodex, "wrong") {
		t.Fatal("codex wrong model")
	}
}

func TestResolveAuthorizedSwitchModel_Ambiguous(t *testing.T) {
	m := RoleMap{
		SchemaVersion: RoleMapSchemaVersion,
		Roles: map[string]RoleBinding{
			"implementor": {Template: "implementor", Harness: HarnessClaudeCode},
		},
		Failover: FailoverConfig{
			Roles: map[string][]FailoverTarget{
				"implementor": {
					{Harness: HarnessCodex, Model: "o3"},
					{Harness: HarnessCodex, Model: "o4"},
				},
			},
		},
	}
	_, err := ResolveAuthorizedSwitchModel(m, "implementor", HarnessCodex, "")
	if !errors.Is(err, ErrSwitchTargetModelRequired) {
		t.Fatalf("err=%v want ErrSwitchTargetModelRequired", err)
	}
	got, err := ResolveAuthorizedSwitchModel(m, "implementor", HarnessCodex, "o3")
	if err != nil || got != "o3" {
		t.Fatalf("got %q err=%v", got, err)
	}
	_, err = ResolveAuthorizedSwitchModel(m, "implementor", HarnessCodex, "nope")
	if !errors.Is(err, ErrSwitchTargetUnauthorized) {
		t.Fatalf("err=%v", err)
	}
}

func TestResolveAuthorizedSwitchModel_UniqueDefault(t *testing.T) {
	m := RoleMap{
		SchemaVersion: RoleMapSchemaVersion,
		Roles: map[string]RoleBinding{
			"implementor": {Template: "implementor", Harness: HarnessClaudeCode},
		},
		Failover: FailoverConfig{
			Roles: map[string][]FailoverTarget{
				"implementor": {{Harness: HarnessCodex}}, // empty = provider default
			},
		},
	}
	got, err := ResolveAuthorizedSwitchModel(m, "implementor", HarnessCodex, "")
	if err != nil || got != "" {
		t.Fatalf("got %q err=%v, want empty default", got, err)
	}
}
