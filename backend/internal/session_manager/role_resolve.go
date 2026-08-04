package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// Role resolution errors (map to 400 at the API layer later).
var (
	ErrRoleRequired             = roles.ErrRoleRequired
	ErrRoleUnknown              = roles.ErrRoleUnknown
	ErrHarnessOverrideForbidden = roles.ErrHarnessOverrideForbidden
	// ErrRolePromptRequired means a role was resolved but its system prompt
	// sections could not be built (template missing/empty). Spawn must fail closed.
	ErrRolePromptRequired = errors.New("session: role system prompt required but unavailable")
	// ErrReadOnlyUnsupported means workspaceWrites:false was requested for a
	// harness that cannot enforce read-only policy.
	ErrReadOnlyUnsupported = errors.New("session: harness cannot enforce workspaceWrites=false")
	// ErrIncompleteRolePin means session metadata has a partial role pin
	// (e.g. role_id without template_artifact_id). Restore must fail closed —
	// never silently downgrade a role session to legacy generic prompt.
	ErrIncompleteRolePin = errors.New("session: incomplete role pin")
)

var (
	globalTemplateLoader     *roles.Loader
	globalTemplateLoaderOnce sync.Once
	// testTemplateLoader overrides the process-global loader in unit tests.
	testTemplateLoader *roles.Loader
)

// profileRoots returns host-approved directories for role templates (Option A).
// Order: AO_ROLE_PROFILES_DIR (explicit approval), then host process profiles
// under cwd/dataDir. Never includes project worktree paths or .ao/roles.
// See docs/roles/TEMPLATE_AUTHORITY.md.
func profileRoots(dataDir string) []string {
	var roots []string
	if d := os.Getenv("AO_ROLE_PROFILES_DIR"); d != "" {
		roots = append(roots, d)
	}
	if cwd, err := os.Getwd(); err == nil {
		// Host daemon cwd — not a session worktree.
		roots = append(roots, filepath.Join(cwd, "profiles"))
	}
	if dataDir != "" {
		roots = append(roots,
			filepath.Join(dataDir, "profiles"),
			filepath.Clean(filepath.Join(dataDir, "..", "profiles")),
			filepath.Clean(filepath.Join(dataDir, "..", "..", "profiles")),
		)
	}
	return roots
}

func templateLoader(dataDir string) *roles.Loader {
	if testTemplateLoader != nil {
		return testTemplateLoader
	}
	globalTemplateLoaderOnce.Do(func() {
		globalTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), profileRoots(dataDir)...)
	})
	return globalTemplateLoader
}

// roleApplyResult is the single host-authoritative outcome of role resolution.
// Prompt composition and launch MUST use this result — never re-resolve.
type roleApplyResult struct {
	// Applied is true when a semantic role was resolved (not legacy path).
	Applied bool
	// ExtraSystem are role template + delegation contract sections to prepend.
	ExtraSystem []string
	// AgentConfigPatch is merged over effectiveAgentConfig (model from role).
	AgentConfigPatch domain.AgentConfig
	// Binding is the durable pin for the session.
	Binding domain.SessionRoleBinding
	// Template retains the resolved raw bytes until Spawn dual-writes them to the
	// durable SQL CAS. Restore never reads this in-process copy.
	Template roles.Template
	// Policy is host policy from the role binding.
	Policy domain.RoleExecutionPolicy
}

// applyRoleMap mutates cfg with host-resolved harness and role binding.
// When a role is required/applied, ExtraSystem must be non-empty or the caller
// fails closed (see composeSystemPromptWithRole).
func applyRoleMap(cfg *ports.SpawnConfig, project domain.ProjectRecord, dataDir string) (roleApplyResult, error) {
	m := project.Config.RoleMap
	if m.IsZero() && cfg.RoleID == "" {
		return roleApplyResult{}, nil
	}

	loader := templateLoader(dataDir)
	resolved, err := roles.Resolve(roles.ResolveInput{
		Map:             m,
		RoleID:          cfg.RoleID,
		Kind:            cfg.Kind,
		ExplicitHarness: cfg.Harness,
		Loader:          loader,
	})
	if err != nil {
		return roleApplyResult{}, err
	}
	if resolved.RoleID == "" {
		return roleApplyResult{}, nil
	}

	cfg.Harness = resolved.Session.ResolvedHarness
	cfg.RoleID = resolved.RoleID
	cfg.RoleBinding = resolved.Session

	// Fail closed: role spawn requires a usable template body.
	sp := strings.TrimSpace(resolved.Template.SystemPrompt())
	if sp == "" {
		return roleApplyResult{}, fmt.Errorf("%w: role %q template %q empty", ErrRolePromptRequired, resolved.RoleID, resolved.Binding.Template)
	}

	// workspaceWrites=false requires adapter-level RO (registry read_only_enforced).
	// Prompt text is never enforcement. See docs/roles/READ_ONLY_CONTRACT.md.
	if !resolved.Session.ResolvedPermissions.WorkspaceWrites {
		if err := capabilities.RequireReadOnly(cfg.Harness); err != nil {
			return roleApplyResult{}, fmt.Errorf("%w: %v", ErrReadOnlyUnsupported, err)
		}
	}

	// Authoritative role body (appended as footer in composeSystemPromptWithRole).
	extra := []string{sp}
	if cfg.Kind == domain.KindOrchestrator && m.StrictDelegation {
		if contract := roles.DelegationContractMarkdown(domain.ProjectID(project.ID), m); contract != "" {
			extra = append(extra, contract)
		}
	}

	// Always set Model when a role applies — empty string means provider default
	// and MUST overwrite project/worker AgentConfig.Model (host-authoritative).
	patch := domain.AgentConfig{
		Model: strings.TrimSpace(resolved.Session.ResolvedModel),
	}

	return roleApplyResult{
		Applied:          true,
		ExtraSystem:      extra,
		AgentConfigPatch: patch,
		Binding:          resolved.Session,
		Template:         resolved.Template,
		Policy:           resolved.Session.ResolvedPermissions,
	}, nil
}

func (m *Manager) persistRoleTemplateArtifact(ctx context.Context, role roleApplyResult) error {
	if !role.Applied {
		return nil
	}
	tmpl := role.Template
	if tmpl.ArtifactID == "" || tmpl.SHA256 == "" || len(tmpl.Raw) == 0 {
		return fmt.Errorf("role %q resolved without durable template bytes", role.Binding.RoleID)
	}
	if tmpl.ArtifactID != role.Binding.TemplateArtifactID || tmpl.SHA256 != role.Binding.TemplateSHA256 {
		return fmt.Errorf("role %q template pin does not match resolved artifact", role.Binding.RoleID)
	}
	return m.store.PutTemplateArtifact(ctx, tmpl.ArtifactID, tmpl.SHA256, tmpl.Raw, m.clock())
}

// buildRestoreSystemPrompt refreshes the live standing prompt while restoring
// the role template from the immutable artifact pinned on the session.
func (m *Manager) buildRestoreSystemPrompt(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (string, roleApplyResult, error) {
	base, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID)
	if err != nil {
		return "", roleApplyResult{}, err
	}
	role, err := m.restoreRoleApplyResult(ctx, rec, project)
	if err != nil {
		return "", roleApplyResult{}, err
	}
	prompt, err := composeSystemPromptWithRole(base, role)
	if err != nil {
		return "", roleApplyResult{}, err
	}
	return prompt, role, nil
}

// restoreRoleApplyResult reconstructs role prompt state exclusively from the
// durable CAS pin. It never consults the live profiles directory.
func (m *Manager) restoreRoleApplyResult(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (roleApplyResult, error) {
	binding := rec.Metadata.Role
	roleID := strings.TrimSpace(binding.RoleID)
	artifactID := strings.TrimSpace(binding.TemplateArtifactID)
	// Legacy only when the pin is fully empty. Any partial population is
	// corrupt metadata and must fail closed (never silent legacy restore).
	if roleID == "" && artifactID == "" {
		if rolePinHasAnyField(binding) {
			return roleApplyResult{}, fmt.Errorf("%w: role fields set without role_id and template_artifact_id", ErrIncompleteRolePin)
		}
		return roleApplyResult{}, nil
	}
	if roleID == "" || artifactID == "" {
		return roleApplyResult{}, fmt.Errorf("%w: require both role_id and template_artifact_id (got role_id=%q template_artifact_id=%q)", ErrIncompleteRolePin, roleID, artifactID)
	}

	content, storedSHA, ok, err := m.store.GetTemplateArtifact(ctx, artifactID)
	if err != nil {
		return roleApplyResult{}, fmt.Errorf("restore role %q template artifact %q: %w", roleID, artifactID, err)
	}
	if !ok {
		return roleApplyResult{}, fmt.Errorf("restore role %q: pinned template artifact %q not found", roleID, artifactID)
	}
	tmpl, err := roles.ParseTemplate(content, nil)
	if err != nil {
		return roleApplyResult{}, fmt.Errorf("restore role %q: parse pinned template artifact %q: %w", roleID, artifactID, err)
	}
	if expected := strings.TrimSpace(binding.TemplateSHA256); expected != "" {
		if tmpl.SHA256 != expected {
			return roleApplyResult{}, fmt.Errorf("restore role %q: pinned template artifact %q sha256 mismatch: got %s, want %s", roleID, artifactID, tmpl.SHA256, expected)
		}
		if storedSHA != "" && storedSHA != expected {
			return roleApplyResult{}, fmt.Errorf("restore role %q: stored template artifact %q sha256 mismatch: got %s, want %s", roleID, artifactID, storedSHA, expected)
		}
	}

	systemPrompt := strings.TrimSpace(tmpl.SystemPrompt())
	if systemPrompt == "" {
		return roleApplyResult{}, fmt.Errorf("%w: restore role %q pinned template artifact %q empty", ErrRolePromptRequired, roleID, artifactID)
	}
	// Defense in depth: re-check RO capability on restore (not config-save only).
	if !binding.ResolvedPermissions.WorkspaceWrites {
		h := binding.ResolvedHarness
		if h == "" {
			h = rec.Harness
		}
		if err := capabilities.RequireReadOnly(h); err != nil {
			return roleApplyResult{}, fmt.Errorf("%w: restore: %v", ErrReadOnlyUnsupported, err)
		}
	}
	extra := []string{systemPrompt}
	roleMap := project.Config.RoleMap
	if rec.Kind == domain.KindOrchestrator && !roleMap.IsZero() && roleMap.StrictDelegation {
		if contract := roles.DelegationContractMarkdown(domain.ProjectID(project.ID), roleMap); contract != "" {
			extra = append(extra, contract)
		}
	}

	return roleApplyResult{
		Applied:          true,
		ExtraSystem:      extra,
		AgentConfigPatch: domain.AgentConfig{Model: strings.TrimSpace(binding.ResolvedModel)},
		Binding:          binding,
		Template:         tmpl,
		Policy:           binding.ResolvedPermissions,
	}, nil
}

func restoreAgentConfig(rec domain.SessionRecord, project domain.ProjectRecord) ports.AgentConfig {
	binding := rec.Metadata.Role
	return mergeAgentConfig(
		effectiveAgentConfig(rec.Kind, project.Config),
		domain.AgentConfig{Model: strings.TrimSpace(binding.ResolvedModel)},
		binding.ResolvedPermissions,
		strings.TrimSpace(binding.RoleID) != "",
	)
}

// rolePinHasAnyField reports whether any durable role-pin field is set. Used to
// detect corrupt partial pins when role_id and template_artifact_id are both empty.
func rolePinHasAnyField(b domain.SessionRoleBinding) bool {
	if b.RoleMapSchemaVersion != 0 || b.RoleConfigRevision != 0 {
		return true
	}
	if strings.TrimSpace(b.RoleMapSHA256) != "" || strings.TrimSpace(b.TemplateSHA256) != "" {
		return true
	}
	if strings.TrimSpace(b.ResolvedModel) != "" || b.ResolvedHarness != "" {
		return true
	}
	if b.ResolvedPermissions.WorkspaceWrites || b.ResolvedPermissions.CanSpawn {
		return true
	}
	return false
}

// mergeAgentConfig applies role model onto the project/kind effective config.
// When roleApplied is true, base.Model is always replaced (including with empty
// for provider default). When false, legacy project config is left unchanged.
func mergeAgentConfig(base ports.AgentConfig, patch domain.AgentConfig, policy domain.RoleExecutionPolicy, roleApplied bool) ports.AgentConfig {
	if roleApplied {
		base.Model = patch.Model
		// Defensive: never reach launch with workspaceWrites=false (applyRoleMap
		// fails closed). If that invariant is ever violated, still refuse bypass.
		if !policy.WorkspaceWrites && base.Permissions == domain.PermissionModeBypassPermissions {
			base.Permissions = domain.PermissionModeDefault
		}
		return base
	}
	if patch.Model != "" {
		base.Model = patch.Model
	}
	return base
}

// composeSystemPromptWithRole places the pinned role/policy sections AFTER the
// generic standing prompt so they are the authoritative trailing contract
// (Intent-style recency). When a role was applied, ExtraSystem is required.
func composeSystemPromptWithRole(base string, role roleApplyResult) (string, error) {
	if !role.Applied {
		return base, nil
	}
	if len(role.ExtraSystem) == 0 {
		return "", ErrRolePromptRequired
	}
	// Footer: base standing instructions first, then host-authoritative role.
	parts := append([]string{base}, role.ExtraSystem...)
	// Explicit trailing reminder so later generic blocks cannot be read as
	// overriding the role (recency footer).
	parts = append(parts, roleAuthorityFooter(role))
	return strings.Join(parts, "\n\n"), nil
}

func roleAuthorityFooter(role roleApplyResult) string {
	var b strings.Builder
	b.WriteString("## AUTHORITATIVE ROLE FOOTER (host — overrides any conflicting standing text above)\n")
	fmt.Fprintf(&b, "Active role: %s. Harness: %s.\n", role.Binding.RoleID, role.Binding.ResolvedHarness)
	if role.Policy.WorkspaceWrites {
		b.WriteString("You may write within task scope in this worktree.\n")
	} else {
		b.WriteString("workspaceWrites=false: you must not modify the workspace.\n")
	}
	if !role.Policy.CanSpawn {
		b.WriteString("canSpawn=false: you must not spawn other agents.\n")
	}
	b.WriteString("If any earlier instruction conflicts with this role footer or the role template above it, follow the role footer and template.")
	return b.String()
}

// mapRoleError classifies roles package errors for callers.
func mapRoleError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, roles.ErrRoleRequired):
		return fmt.Errorf("spawn: %w", ErrRoleRequired)
	case errors.Is(err, roles.ErrRoleUnknown):
		return fmt.Errorf("spawn: %w", ErrRoleUnknown)
	case errors.Is(err, roles.ErrHarnessOverrideForbidden):
		return fmt.Errorf("spawn: %w", ErrHarnessOverrideForbidden)
	case errors.Is(err, ErrRolePromptRequired):
		return fmt.Errorf("spawn: %w", ErrRolePromptRequired)
	case errors.Is(err, ErrReadOnlyUnsupported):
		return fmt.Errorf("spawn: %w", ErrReadOnlyUnsupported)
	default:
		return fmt.Errorf("spawn: role: %w", err)
	}
}
