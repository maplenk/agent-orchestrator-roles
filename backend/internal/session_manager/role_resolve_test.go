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
	if !errors.Is(err, ErrRolePromptRequired) {
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

func TestApplyRoleMap_ReadOnly_OnlyCodexAllowed(t *testing.T) {
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

	mkProject := func(harness domain.AgentHarness) domain.ProjectRecord {
		return domain.ProjectRecord{
			ID: "mer",
			Config: domain.ProjectConfig{
				RoleMap: domain.RoleMap{
					SchemaVersion:    domain.RoleMapSchemaVersion,
					StrictDelegation: true,
					OrchestratorRole: "orchestrator",
					Roles: map[string]domain.RoleBinding{
						"orchestrator": {
							Template: "orchestrator",
							// Codex is the only Phase 1 RO-enforced harness for WW=false orch.
							Harness: domain.HarnessCodex,
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
	}

	// Codex: real OS sandbox — allowed.
	cfg := ports.SpawnConfig{Kind: domain.KindWorker, RoleID: "reviewer"}
	res, err := applyRoleMap(&cfg, mkProject(domain.HarnessCodex), dir)
	if err != nil {
		t.Fatalf("codex: unexpected err %v", err)
	}
	if !res.Applied || res.Policy.WorkspaceWrites {
		t.Fatalf("codex: applied=%v policy=%+v", res.Applied, res.Policy)
	}

	// Claude auto mode is not fail-closed; Pi has no sandbox — reject both.
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessPi} {
		cfg := ports.SpawnConfig{Kind: domain.KindWorker, RoleID: "reviewer"}
		_, err := applyRoleMap(&cfg, mkProject(harness), dir)
		if !errors.Is(err, ErrReadOnlyUnsupported) {
			t.Fatalf("harness %q: err = %v, want ErrReadOnlyUnsupported", harness, err)
		}
	}
}

// applyRoleMap is the ONE place /sessions and delegation both reach, which is
// why the execution-override rule is enforced here and not in either caller.
// Two callers enforcing it separately is how one of them ends up not.
func TestApplyRoleMap_RejectsExecutionOverrides(t *testing.T) {
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
		Config: domain.ProjectConfig{RoleMap: domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			StrictDelegation: true,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {Template: "orchestrator", Harness: domain.HarnessCodex,
					Permissions: domain.RoleExecutionPolicy{CanSpawn: true}},
				"implementor": {Template: "implementor", Harness: domain.HarnessCodex, Model: "role-model-id",
					Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true}},
			},
		}},
	}

	for _, tc := range []struct {
		name string
		cfg  ports.SpawnConfig
		want error
	}{
		{
			// The /sessions shape: POST body carries agentConfig.model.
			name: "spawn with a model",
			cfg: ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RoleID: "implementor",
				AgentConfig: ports.AgentConfig{Model: "gpt-4"}},
			want: ErrModelOverrideForbidden,
		},
		{
			// The delegation shape: DelegateTaskRequest.model lands in the same
			// field via DelegateTask, so it is the same boundary or none at all.
			name: "delegation with a model",
			cfg: ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RoleID: "implementor",
				DisplayName: "task", Prompt: "do it",
				AgentConfig: ports.AgentConfig{Model: "gpt-4"}},
			want: ErrModelOverrideForbidden,
		},
		{
			name: "mode instead of model",
			cfg: ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RoleID: "implementor",
				AgentConfig: ports.AgentConfig{Mode: "ultra"}},
			want: ErrModelOverrideForbidden,
		},
		{
			name: "harness",
			cfg: ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RoleID: "implementor",
				Harness: domain.HarnessCursor},
			want: ErrHarnessOverrideForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			_, err := applyRoleMap(&cfg, project, dir)
			if !errors.Is(err, tc.want) {
				t.Fatalf("applyRoleMap = %v, want %v", err, tc.want)
			}
			// And it must be classified, not fall through to the default
			// branch, or the client sees a 500 for a request it can fix.
			if mapped := mapRoleError(err); !errors.Is(mapped, tc.want) {
				t.Fatalf("mapRoleError = %v, want %v", mapped, tc.want)
			}
		})
	}
}

// A missing profile is an install problem with an actionable fix, not a daemon
// bug. Unwrapped it fell to mapRoleError's default branch and surfaced as
// INTERNAL_ERROR — which is how a clean install with no profiles installed
// reported itself as broken software.
func TestApplyRoleMap_MissingTemplateIsClassified(t *testing.T) {
	dir := t.TempDir() // deliberately empty: no profiles installed
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	project := domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{RoleMap: domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {Template: "orchestrator", Harness: domain.HarnessCodex,
					Permissions: domain.RoleExecutionPolicy{CanSpawn: true}},
				"implementor": {Template: "implementor", Harness: domain.HarnessCodex,
					Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true}},
			},
		}},
	}
	cfg := ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, RoleID: "implementor"}
	_, err := applyRoleMap(&cfg, project, dir)
	if err == nil {
		t.Fatal("a role whose template does not exist resolved successfully")
	}
	if !errors.Is(mapRoleError(err), ErrRolePromptRequired) {
		t.Fatalf("mapRoleError = %v, want ErrRolePromptRequired (which maps to ROLE_TEMPLATE_UNAVAILABLE)", mapRoleError(err))
	}
}

// requireChatModeAllowed guards TWO paths, and the live dogfood found that only
// one of them was wired: relaunch had the gate, SPAWN did not, so a read-only
// role started in chat mode on the first try and only a restart would have
// refused it. The gate belongs to both, which is what this pins.
func TestRequireChatModeAllowed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		binding domain.SessionRoleBinding
		wantErr bool
		why     string
	}{
		{
			name:    "no role pin",
			binding: domain.SessionRoleBinding{},
			why:     "a session with no role has no role policy to enforce; chat is ordinary",
		},
		{
			name: "writable role",
			binding: domain.SessionRoleBinding{RoleID: "implementor",
				ResolvedPermissions: domain.RoleExecutionPolicy{WorkspaceWrites: true}},
			why: "the role may write, so chat grants nothing it does not already have",
		},
		{
			name: "read-only role",
			binding: domain.SessionRoleBinding{RoleID: "reviewer",
				ResolvedPermissions: domain.RoleExecutionPolicy{WorkspaceWrites: false}},
			wantErr: true,
			why: "read_only_enforced is a property of the TERMINAL argv; the chat " +
				"controller never receives it, and Codex chat maps ordinary permissions " +
				"to danger-full-access — so chat would hand writes to a role defined " +
				"not to have them, with the read-only claim still displayed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireChatModeAllowed(tc.binding)
			if tc.wantErr && !errors.Is(err, ErrChatModeReadOnlyUnsupported) {
				t.Fatalf("err = %v, want ErrChatModeReadOnlyUnsupported — %s", err, tc.why)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want nil — %s", err, tc.why)
			}
			// And it must be classified, or the client sees a 500 for a request
			// whose fix (use terminal mode) the message can state.
			if tc.wantErr && !errors.Is(mapRoleError(err), ErrChatModeReadOnlyUnsupported) {
				t.Fatalf("mapRoleError dropped the sentinel: %v", mapRoleError(err))
			}
		})
	}
}
