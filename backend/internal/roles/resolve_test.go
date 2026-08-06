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
					WorkspaceWrites: false,
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
	if r.Session.ResolvedPermissions.WorkspaceWrites {
		t.Fatal("orchestrator must pin workspaceWrites=false")
	}
}

func TestResolve_NonStrictOrchestratorNoAutoBind(t *testing.T) {
	m := testMap()
	m.StrictDelegation = false
	r, err := Resolve(ResolveInput{
		Map:  m,
		Kind: domain.KindOrchestrator,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.RoleID != "" {
		t.Fatalf("non-strict orch without RoleID must not auto-bind, got %q", r.RoleID)
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
	required := md
	if i := strings.Index(md, "### Forbidden"); i >= 0 {
		required = md[:i]
	}
	if strings.Contains(required, "--agent") || strings.Contains(required, "--harness") {
		t.Fatal("required spawn section must not teach --agent/--harness")
	}
}
