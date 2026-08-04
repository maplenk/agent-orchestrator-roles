package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
)

func TestComposeSystemPromptWithRole_FailClosed(t *testing.T) {
	_, err := composeSystemPromptWithRole("base", roleApplyResult{Applied: true})
	if err != ErrRolePromptRequired {
		t.Fatalf("err = %v, want ErrRolePromptRequired", err)
	}
}

func TestParseTemplate_CASBytesPreserveSystemPrompt(t *testing.T) {
	raw := []byte("---\nid: implementor\nname: Implementor\ndescription: Ships production changes.\nroleReminder: Stay in scope.\n---\n# Duties\nImplement the requested change.\n")
	cas := roles.NewArtifactStore()
	original, err := roles.ParseTemplate(raw, cas)
	if err != nil {
		t.Fatal(err)
	}
	content, ok := cas.Get(original.ArtifactID)
	if !ok {
		t.Fatalf("artifact %q not found", original.ArtifactID)
	}
	restored, err := roles.ParseTemplate(content, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := restored.SystemPrompt(), original.SystemPrompt(); got != want {
		t.Fatalf("restored system prompt = %q, want %q", got, want)
	}
}

func TestBuildRestoreSystemPrompt_UsesPinnedArtifact(t *testing.T) {
	raw := []byte("---\nid: implementor\nname: Implementor\n---\nPinned role body.\n")
	tmpl, err := roles.ParseTemplate(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	project := domain.ProjectRecord{ID: "mer"}
	st.projects[project.ID] = project
	if err := st.PutTemplateArtifact(ctx, tmpl.ArtifactID, tmpl.SHA256, raw, time.Time{}); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{
			RoleID:             "implementor",
			TemplateArtifactID: tmpl.ArtifactID,
			TemplateSHA256:     tmpl.SHA256,
			ResolvedHarness:    domain.HarnessCodex,
			ResolvedPermissions: domain.RoleExecutionPolicy{
				WorkspaceWrites: true,
			},
		}},
	}

	prompt, role, err := New(Deps{Store: st}).buildRestoreSystemPrompt(ctx, rec, project)
	if err != nil {
		t.Fatal(err)
	}
	if !role.Applied {
		t.Fatal("restored role was not applied")
	}
	for _, want := range []string{"Pinned role body.", "AUTHORITATIVE ROLE FOOTER"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("restored system prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildRestoreSystemPrompt_MissingArtifact(t *testing.T) {
	st := newFakeStore()
	project := domain.ProjectRecord{ID: "mer"}
	st.projects[project.ID] = project
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{
			RoleID:             "implementor",
			TemplateArtifactID: "sha256:missing",
		}},
	}

	_, _, err := New(Deps{Store: st}).buildRestoreSystemPrompt(ctx, rec, project)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want missing artifact error", err)
	}
}

func TestBuildRestoreSystemPrompt_IncompleteRolePinFailClosed(t *testing.T) {
	st := newFakeStore()
	project := domain.ProjectRecord{ID: "mer"}
	st.projects[project.ID] = project
	m := New(Deps{Store: st})

	// RoleID without TemplateArtifactID must not restore as legacy.
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{
			RoleID: "implementor",
		}},
	}
	_, _, err := m.buildRestoreSystemPrompt(ctx, rec, project)
	if !errors.Is(err, ErrIncompleteRolePin) {
		t.Fatalf("role_id only: err = %v, want ErrIncompleteRolePin", err)
	}

	// TemplateArtifactID without RoleID is also corrupt.
	rec.Metadata.Role = domain.SessionRoleBinding{TemplateArtifactID: "sha256:deadbeef"}
	_, _, err = m.buildRestoreSystemPrompt(ctx, rec, project)
	if !errors.Is(err, ErrIncompleteRolePin) {
		t.Fatalf("artifact only: err = %v, want ErrIncompleteRolePin", err)
	}

	// Fully empty pin → legacy (unapplied, no error).
	rec.Metadata.Role = domain.SessionRoleBinding{}
	prompt, role, err := m.buildRestoreSystemPrompt(ctx, rec, project)
	if err != nil {
		t.Fatalf("empty pin: %v", err)
	}
	if role.Applied {
		t.Fatal("empty pin must not apply role")
	}
	if prompt == "" {
		t.Fatal("expected generic standing prompt")
	}

	// Other role fields without either ID (post store hydration) also fail closed.
	rec.Metadata.Role = domain.SessionRoleBinding{
		TemplateSHA256: "deadbeef",
		ResolvedModel:  "x",
	}
	_, _, err = m.buildRestoreSystemPrompt(ctx, rec, project)
	if !errors.Is(err, ErrIncompleteRolePin) {
		t.Fatalf("partial fields only: err = %v, want ErrIncompleteRolePin", err)
	}
}

func TestRestoreAgentConfig_ReappliesResolvedModel(t *testing.T) {
	rec := domain.SessionRecord{
		Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{
			RoleID:        "implementor",
			ResolvedModel: "pinned-role-model",
			ResolvedPermissions: domain.RoleExecutionPolicy{
				WorkspaceWrites: true,
			},
		}},
	}
	project := domain.ProjectRecord{Config: domain.ProjectConfig{
		AgentConfig: domain.AgentConfig{Model: "project-model"},
	}}

	if got := restoreAgentConfig(rec, project).Model; got != "pinned-role-model" {
		t.Fatalf("restored model = %q, want pinned-role-model", got)
	}
}

func TestComposeSystemPromptWithRole_AppendsAuthoritativeFooter(t *testing.T) {
	got, err := composeSystemPromptWithRole("base", roleApplyResult{
		Applied:     true,
		ExtraSystem: []string{"ROLE", "CONTRACT"},
		Binding:     domain.SessionRoleBinding{RoleID: "implementor", ResolvedHarness: domain.HarnessCodex},
		Policy:      domain.RoleExecutionPolicy{WorkspaceWrites: true, CanSpawn: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "base\n\nROLE\n\nCONTRACT") {
		t.Fatalf("role sections must follow base, got %q", got)
	}
	if !strings.Contains(got, "AUTHORITATIVE ROLE FOOTER") {
		t.Fatalf("missing footer: %q", got)
	}
}

func TestMergeAgentConfig_AppliesModelAndClearsEmpty(t *testing.T) {
	base := ports.AgentConfig{
		Model:       "legacy-model",
		Permissions: domain.PermissionModeBypassPermissions,
	}
	got := mergeAgentConfig(base, domain.AgentConfig{Model: "role-model"}, domain.RoleExecutionPolicy{
		WorkspaceWrites: true,
		CanSpawn:        false,
	}, true)
	if got.Model != "role-model" {
		t.Fatalf("model = %q", got.Model)
	}
	// Empty role model must wipe legacy project model (provider default).
	got2 := mergeAgentConfig(base, domain.AgentConfig{Model: ""}, domain.RoleExecutionPolicy{
		WorkspaceWrites: true,
	}, true)
	if got2.Model != "" {
		t.Fatalf("empty role model should clear base, got %q", got2.Model)
	}
	// Without role, empty patch must not clear project model.
	got3 := mergeAgentConfig(base, domain.AgentConfig{}, domain.RoleExecutionPolicy{}, false)
	if got3.Model != "legacy-model" {
		t.Fatalf("legacy model lost without role: %q", got3.Model)
	}
}

func TestComposeSystemPromptWithRole_FooterAuthoritative(t *testing.T) {
	got, err := composeSystemPromptWithRole("generic worker: implement and commit", roleApplyResult{
		Applied:     true,
		ExtraSystem: []string{"## Role: Reviewer\nreview only"},
		Binding:     domain.SessionRoleBinding{RoleID: "reviewer", ResolvedHarness: domain.HarnessCodex},
		Policy:      domain.RoleExecutionPolicy{WorkspaceWrites: true, CanSpawn: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Role body and footer must come AFTER generic base.
	if i, j := strings.Index(got, "generic worker"), strings.Index(got, "Role: Reviewer"); i < 0 || j < 0 || j < i {
		t.Fatalf("role must follow base: %q", got)
	}
	if !strings.Contains(got, "AUTHORITATIVE ROLE FOOTER") {
		t.Fatal("missing authority footer")
	}
}

func TestApplyRoleMap_AppliesHarnessAndModel(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"implementor.md":  "---\nid: implementor\nname: Implementor\nroleReminder: do it\n---\n# Impl body\n",
		"orchestrator.md": "---\nid: orchestrator\nname: Orch\nroleReminder: coord\n---\n# Orch body\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	project := domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{
			RoleMap: domain.RoleMap{
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
						Model:    "role-model-id",
						Permissions: domain.RoleExecutionPolicy{
							WorkspaceWrites: true,
							CanSpawn:        false,
						},
					},
				},
			},
		},
	}

	cfg := ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		RoleID:    "implementor",
	}
	res, err := applyRoleMap(&cfg, project, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applied || cfg.Harness != domain.HarnessCodex {
		t.Fatalf("applied=%v harness=%q", res.Applied, cfg.Harness)
	}
	if res.AgentConfigPatch.Model != "role-model-id" {
		t.Fatalf("model patch = %q", res.AgentConfigPatch.Model)
	}
	if len(res.ExtraSystem) == 0 {
		t.Fatal("expected system sections")
	}
	merged := mergeAgentConfig(ports.AgentConfig{Model: "legacy"}, res.AgentConfigPatch, res.Policy, res.Applied)
	if merged.Model != "role-model-id" {
		t.Fatalf("merged model = %q", merged.Model)
	}
}

func TestApplyRoleMap_RejectsAllReadOnlyUntilAdapterModeExists(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"reviewer.md":     "---\nid: reviewer\nname: Reviewer\n---\n# R\n",
		"orchestrator.md": "---\nid: orchestrator\nname: Orch\n---\n# O\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	// Codex is the dangerous case: PermissionModeDefault maps to full bypass.
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode, domain.HarnessPi} {
		project := domain.ProjectRecord{
			ID: "mer",
			Config: domain.ProjectConfig{
				RoleMap: domain.RoleMap{
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
						"reviewer": {
							Template: "reviewer",
							Harness:  harness,
							Permissions: domain.RoleExecutionPolicy{
								WorkspaceWrites: false,
								CanSpawn:        false,
							},
						},
					},
				},
			},
		}
		cfg := ports.SpawnConfig{Kind: domain.KindWorker, RoleID: "reviewer"}
		_, err := applyRoleMap(&cfg, project, dir)
		if !errors.Is(err, ErrReadOnlyUnsupported) {
			t.Fatalf("harness %q: err = %v, want ErrReadOnlyUnsupported", harness, err)
		}
	}
}
