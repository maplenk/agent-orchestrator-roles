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
					WorkspaceWrites: true,
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

func TestRoleMapValidate_StrictOrchestratorMayBeWritable(t *testing.T) {
	m := sampleStrictMap()
	if err := m.Validate(); err != nil {
		t.Fatalf("strict routing/delegation must not imply technical read-only: %v", err)
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

// template is a PROFILE ID; the loader appends the extension. A map that said
// "implementor.md" was accepted here and then failed at spawn time looking for
// "implementor.md.md" — a path nobody wrote, reported long after the config was
// authored, and (before the sentinel wrapping) as an internal error. Refusing
// it where it is written turns a confusing runtime failure into a config
// message naming the exact fix.
func TestRoleMapValidate_RejectsTemplateFilenamesAndPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		want     string
	}{
		{"markdown extension", "implementor.md", "profile id"},
		{"uppercase extension", "Implementor.MD", "profile id"},
		{"relative path", "profiles/implementor", "not a path"},
		{"traversal", "../implementor", "not a path"},
		{"windows separator", `profiles\implementor`, "not a path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sampleStrictMap()
			b := m.Roles["implementor"]
			b.Template = tc.template
			m.Roles["implementor"] = b
			err := m.Validate()
			if err == nil {
				t.Fatalf("Validate accepted template %q", tc.template)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not say %q", err, tc.want)
			}
		})
	}
}

// "readme" is a legitimate profile id that merely contains the letters. The
// check must key on the suffix, not on a substring.
func TestRoleMapValidate_AllowsProfileIdsContainingMd(t *testing.T) {
	m := sampleStrictMap()
	b := m.Roles["implementor"]
	b.Template = "mdx-implementor"
	m.Roles["implementor"] = b
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate rejected a legitimate profile id: %v", err)
	}
}
