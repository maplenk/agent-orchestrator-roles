package roles

import (
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrRoleRequired is returned when strictDelegation requires --role.
var ErrRoleRequired = fmt.Errorf("role required: pass --role <id> (strictDelegation)")

// ErrRoleUnknown is returned when the role id is not in the map.
var ErrRoleUnknown = fmt.Errorf("unknown role")

// ErrHarnessOverrideForbidden is returned when strict spawn forbids free-form harness.
var ErrHarnessOverrideForbidden = fmt.Errorf("harness override forbidden under strictDelegation; use --role only")

// Resolved is the daemon-authoritative spawn plan for a role.
type Resolved struct {
	RoleID   string
	Binding  domain.RoleBinding
	Template Template
	MapSHA   string
	Session  domain.SessionRoleBinding
	// AgentConfig carries model (and later effort) into launch.
	AgentConfig domain.AgentConfig
}

// ResolveInput is the spawn-time resolution request.
type ResolveInput struct {
	Map          domain.RoleMap
	RoleID       string
	Kind         domain.SessionKind
	// ExplicitHarness is the free-form --agent/--harness from the client.
	ExplicitHarness domain.AgentHarness
	// Loader loads templates; required when RoleID is set.
	Loader *Loader
}

// Resolve applies RoleMap rules for a spawn.
//
// Strict worker: requires RoleID; rejects ExplicitHarness when RoleID set or when
// client tried free-form only.
// Non-strict: RoleID optional; legacy harness path unchanged when RoleID empty.
func Resolve(in ResolveInput) (Resolved, error) {
	m := in.Map.WithDefaults()
	roleID := strings.TrimSpace(in.RoleID)

	if m.IsZero() {
		if roleID != "" {
			return Resolved{}, fmt.Errorf("%w: project has no roleMap", ErrRoleUnknown)
		}
		return Resolved{}, nil // legacy path
	}

	if m.StrictDelegation && in.Kind == domain.KindWorker {
		if roleID == "" {
			return Resolved{}, ErrRoleRequired
		}
		if in.ExplicitHarness != "" {
			return Resolved{}, ErrHarnessOverrideForbidden
		}
	}

	// Strict maps: auto-bind KindOrchestrator to orchestratorRole so policy is
	// never skipped. That role must be workspaceWrites=false (Validate), so
	// applyRoleMap then fails closed with ErrReadOnlyUnsupported until adapters
	// implement read-only launch — never fall back to a legacy writable orch.
	// Non-strict maps may still spawn KindOrchestrator without a RoleID.
	if roleID == "" && in.Kind == domain.KindOrchestrator && m.StrictDelegation {
		roleID = m.OrchestratorRole
		if roleID == "" {
			roleID = "orchestrator"
		}
	}

	if roleID == "" {
		// Non-strict worker without role: legacy.
		if in.ExplicitHarness != "" && m.StrictDelegation {
			return Resolved{}, ErrHarnessOverrideForbidden
		}
		return Resolved{}, nil
	}

	b, ok := m.Get(roleID)
	if !ok {
		return Resolved{}, fmt.Errorf("%w: %q", ErrRoleUnknown, roleID)
	}
	// Host-authoritative: any explicit harness/model execution field is rejected
	// when --role is supplied — even if it matches the binding (clients must not
	// make routing depend on supplied execution fields).
	if in.ExplicitHarness != "" {
		return Resolved{}, ErrHarnessOverrideForbidden
	}

	mapSHA, err := m.SHA256()
	if err != nil {
		return Resolved{}, err
	}

	var tmpl Template
	if in.Loader != nil {
		tmpl, err = in.Loader.Load(b.Template)
		if err != nil {
			return Resolved{}, fmt.Errorf("role %q template: %w", roleID, err)
		}
	}

	sess := domain.SessionRoleBinding{
		RoleID:               roleID,
		RoleMapSchemaVersion: m.SchemaVersion,
		RoleMapSHA256:        mapSHA,
		TemplateArtifactID:   tmpl.ArtifactID,
		TemplateSHA256:       tmpl.SHA256,
		ResolvedHarness:      b.Harness,
		ResolvedModel:        b.Model,
		ResolvedPermissions:  b.Permissions,
	}

	return Resolved{
		RoleID:  roleID,
		Binding: b,
		Template: tmpl,
		MapSHA:  mapSHA,
		Session: sess,
		AgentConfig: domain.AgentConfig{
			Model: b.Model,
		},
	}, nil
}

// DelegationContractMarkdown is injected into the orchestrator system prompt.
func DelegationContractMarkdown(projectID domain.ProjectID, m domain.RoleMap) string {
	m = m.WithDefaults()
	if m.IsZero() {
		return ""
	}
	var b strings.Builder
	b.WriteString("## HARD DELEGATION CONTRACT (host role map — non-negotiable)\n\n")
	b.WriteString("You are the orchestrator. You MUST NOT implement code in this session.\n\n")
	b.WriteString("### Role catalog\n")
	b.WriteString("| Role | Template | Harness | Model |\n")
	b.WriteString("|------|----------|---------|-------|\n")
	for id, rb := range m.Roles {
		model := rb.Model
		if model == "" {
			model = "(provider default)"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", id, rb.Template, rb.Harness, model)
	}
	b.WriteString("\n### Required spawn commands (copy exactly; --role required)\n")
	for id := range m.Roles {
		if id == m.OrchestratorRole {
			continue
		}
		fmt.Fprintf(&b, "ao spawn --project %s --role %s --name \"<≤20 chars>\" --prompt \"…\"\n", projectID, id)
	}
	b.WriteString("\n### Forbidden\n")
	b.WriteString("- `ao spawn --agent` / `--harness` / free-form model flags\n")
	b.WriteString("- Editing source files in this session\n")
	b.WriteString("- Implementing instead of spawning a worker\n")
	return b.String()
}
