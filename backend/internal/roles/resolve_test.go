package roles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func writeProfiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"orchestrator.md": "---\nid: orchestrator\nname: Orchestrator\nroleReminder: coordinate\n---\n# Orch\n",
		"implementor.md":  "---\nid: implementor\nname: Implementor\nroleReminder: implement\n---\n# Impl\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testMap() domain.RoleMap {
	return domain.RoleMap{
		SchemaVersion:    domain.RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]domain.RoleBinding{
			"orchestrator": {
				Template: "orchestrator",
				Harness:  domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{
					WorkspaceWrites: true,
					CanSpawn:        true,
				},
			},
			"implementor": {
				Template: "implementor",
				Harness:  domain.HarnessCodex,
				Permissions: domain.RoleExecutionPolicy{
					WorkspaceWrites: true,
					CanSpawn:        false,
				},
			},
		},
	}
}

func TestResolve_StrictWorkerRequiresRole(t *testing.T) {
	_, err := Resolve(ResolveInput{
		Map:  testMap(),
		Kind: domain.KindWorker,
	})
	if !errors.Is(err, ErrRoleRequired) {
		t.Fatalf("err = %v, want ErrRoleRequired", err)
	}
}

func TestResolve_StrictRejectsHarnessOverride(t *testing.T) {
	dir := writeProfiles(t)
	loader := NewLoader(NewArtifactStore(), dir)
	_, err := Resolve(ResolveInput{
		Map:             testMap(),
		RoleID:          "implementor",
		Kind:            domain.KindWorker,
		ExplicitHarness: domain.HarnessPi,
		Loader:          loader,
	})
	if !errors.Is(err, ErrHarnessOverrideForbidden) {
		t.Fatalf("err = %v, want ErrHarnessOverrideForbidden", err)
	}
}

func TestResolve_RejectsMatchingHarnessOverride(t *testing.T) {
	dir := writeProfiles(t)
	loader := NewLoader(NewArtifactStore(), dir)
	_, err := Resolve(ResolveInput{
		Map:             testMap(),
		RoleID:          "implementor",
		Kind:            domain.KindWorker,
		ExplicitHarness: domain.HarnessCodex,
		Loader:          loader,
	})
	if !errors.Is(err, ErrHarnessOverrideForbidden) {
		t.Fatalf("err = %v, want ErrHarnessOverrideForbidden for matching harness", err)
	}
}

func TestResolve_StrictOrchestratorAutoBindsRole(t *testing.T) {
	dir := writeProfiles(t)
	loader := NewLoader(NewArtifactStore(), dir)
	r, err := Resolve(ResolveInput{
		Map:  testMap(),
		Kind: domain.KindOrchestrator,
		// no RoleID — strict map must auto-bind orchestratorRole
		Loader: loader,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RoleID != "orchestrator" {
		t.Fatalf("role = %q, want orchestrator", r.RoleID)
	}
	if r.Session.ResolvedHarness != domain.HarnessClaudeCode {
		t.Fatalf("harness = %q", r.Session.ResolvedHarness)
	}
	if !r.Session.ResolvedPermissions.WorkspaceWrites {
		t.Fatal("strict orchestration must preserve the configured writable policy")
	}
}

func TestResolve_NonStrictOrchestratorAutoBindsWithoutLegacyOverride(t *testing.T) {
	m := testMap()
	m.StrictDelegation = false
	r, err := Resolve(ResolveInput{
		Map:  m,
		Kind: domain.KindOrchestrator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RoleID != domain.DefaultOrchestratorRoleID {
		t.Fatalf("non-strict orch role = %q, want default orchestrator", r.RoleID)
	}
}

func TestResolve_NonStrictExplicitHarnessRemainsLegacy(t *testing.T) {
	m := testMap()
	m.StrictDelegation = false
	r, err := Resolve(ResolveInput{
		Map:             m,
		Kind:            domain.KindOrchestrator,
		ExplicitHarness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RoleID != "" {
		t.Fatalf("explicit legacy orchestrator unexpectedly pinned role %q", r.RoleID)
	}
}

func TestResolve_OK(t *testing.T) {
	dir := writeProfiles(t)
	loader := NewLoader(NewArtifactStore(), dir)
	r, err := Resolve(ResolveInput{
		Map:    testMap(),
		RoleID: "implementor",
		Kind:   domain.KindWorker,
		Loader: loader,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Session.ResolvedHarness != domain.HarnessCodex {
		t.Fatalf("harness = %q", r.Session.ResolvedHarness)
	}
	if r.Template.ArtifactID == "" || r.Session.TemplateSHA256 == "" {
		t.Fatal("expected template artifact pin")
	}
	if !strings.Contains(r.Template.SystemPrompt(), "Implementor") {
		t.Fatalf("system prompt: %s", r.Template.SystemPrompt())
	}
}

func TestDelegationContractMarkdown(t *testing.T) {
	md := DelegationContractMarkdown("demo", testMap())
	if !strings.Contains(md, "--role implementor") {
		t.Fatalf("missing spawn line: %s", md)
	}
	if !strings.Contains(md, "### Forbidden") {
		t.Fatal("contract must include Forbidden section")
	}
	if !strings.Contains(md, "instruction-enforced") {
		t.Fatal("contract must state that strict coordination is instruction-enforced")
	}
	required := md
	if i := strings.Index(md, "### Forbidden"); i >= 0 {
		required = md[:i]
	}
	if strings.Contains(required, "--agent") || strings.Contains(required, "--harness") {
		t.Fatal("required spawn section must not teach --agent/--harness")
	}
}

// MASTER_PLAN's rule is that a role-pinned session's execution fields belong to
// the host, and the doc comment on Resolve has always claimed "any explicit
// harness/model execution field is rejected". Only harness was checked.
//
// The two fields fail differently, which is why both are pinned here. An
// explicit MODEL was silently overwritten by the role patch and the request
// still reported success — the caller could believe it had pinned a model for
// the life of the session. An explicit MODE is not overwritten at all, so it
// survived into launch and actually steered a role-pinned session.
func TestResolveRejectsExecutionOverridesAlongsideARole(t *testing.T) {
	base := func() ResolveInput {
		return ResolveInput{
			Map: domain.RoleMap{
				SchemaVersion:    domain.RoleMapSchemaVersion,
				StrictDelegation: true,
				OrchestratorRole: "orchestrator",
				Roles: map[string]domain.RoleBinding{
					"orchestrator": {Harness: domain.HarnessCodex, Template: "orchestrator",
						Permissions: domain.RoleExecutionPolicy{CanSpawn: true}},
					"implementor": {Harness: domain.HarnessCodex, Model: "gpt-5.6-codex", Template: "implementor",
						Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true}},
				},
			},
			RoleID: "implementor",
			Kind:   domain.KindWorker,
		}
	}

	for _, tc := range []struct {
		name   string
		mutate func(*ResolveInput)
		want   error
	}{
		{"explicit model", func(in *ResolveInput) { in.ExplicitModel = "gpt-4" }, ErrModelOverrideForbidden},
		// Even the binding's own value: routing must never depend on what the
		// client supplied, or the client's copy of the map becomes load-bearing.
		{"model that matches the binding", func(in *ResolveInput) { in.ExplicitModel = "gpt-5.6-codex" }, ErrModelOverrideForbidden},
		{"explicit mode", func(in *ResolveInput) { in.ExplicitMode = "ultra" }, ErrModelOverrideForbidden},
		{"explicit harness", func(in *ResolveInput) { in.ExplicitHarness = domain.HarnessCursor }, ErrHarnessOverrideForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base()
			tc.mutate(&in)
			_, err := Resolve(in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Resolve = %v, want %v", err, tc.want)
			}
		})
	}
}

// Whitespace is not a selection. Rejecting it would refuse requests that chose
// nothing, which is what a client sends when the user left the field alone.
func TestResolveAllowsBlankExecutionFieldsAlongsideARole(t *testing.T) {
	in := ResolveInput{
		Map: domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {Harness: domain.HarnessCodex, Template: "orchestrator",
					Permissions: domain.RoleExecutionPolicy{CanSpawn: true}},
				"implementor": {Harness: domain.HarnessCodex, Template: "implementor",
					Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true}},
			},
		},
		RoleID:        "implementor",
		Kind:          domain.KindWorker,
		ExplicitModel: "   ",
		ExplicitMode:  "\t",
	}
	if _, err := Resolve(in); err != nil {
		t.Fatalf("Resolve rejected a request that selected nothing: %v", err)
	}
}
