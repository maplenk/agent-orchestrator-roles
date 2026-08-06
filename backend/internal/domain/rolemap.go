package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RoleMapSchemaVersion is the wire format version for RoleMap documents.
const RoleMapSchemaVersion = 1

// FailoverMode controls how usage-limit continuation advances the ladder.
type FailoverMode string

const (
	// FailoverModeManual requires an operator (or explicit command) to continue
	// on the next rung. This is the default even for Target B.
	FailoverModeManual FailoverMode = "manual"
	// FailoverModeAutomatic advances the ladder without waiting (opt-in).
	FailoverModeAutomatic FailoverMode = "automatic"
)

// RoleExecutionPolicy is host-enforced, not prompt decoration.
type RoleExecutionPolicy struct {
	// WorkspaceWrites is false for orchestrator/reviewer-style roles that must
	// not mutate the worktree. Binding is rejected unless the harness reports
	// read_only_enforced (capability matrix).
	WorkspaceWrites bool `json:"workspaceWrites"`
	// CanSpawn is true only for roles allowed to call daemon spawn (typically
	// the orchestrator). Workers must be false.
	CanSpawn bool `json:"canSpawn"`
}

// RoleBinding maps a semantic role id to template + execution target.
type RoleBinding struct {
	// Template is the profile id (e.g. "implementor") loaded from profiles/.
	Template string `json:"template"`
	// Harness is the AO agent harness (claude-code, codex, pi, …).
	Harness AgentHarness `json:"harness"`
	// Model is optional; empty/null means provider default. Never the literal
	// string "default".
	Model string `json:"model,omitempty"`
	// Permissions is host policy for this role.
	Permissions RoleExecutionPolicy `json:"permissions"`
	// When lists keyword hints for orchestrator role selection (non-authoritative).
	When []string `json:"when,omitempty"`
}

// FailoverTarget is one concrete execution alternative for a semantic role.
// Failover never changes role_id — only harness/model.
type FailoverTarget struct {
	Harness AgentHarness `json:"harness"`
	Model   string       `json:"model,omitempty"`
}

// FailoverConfig is deterministic per-role ladders. Rungs are alternatives
// *after* the current target (first incident must not re-select current).
type FailoverConfig struct {
	Mode  FailoverMode                `json:"mode,omitempty"`
	Roles map[string][]FailoverTarget `json:"roles,omitempty"`
}

// RoleMap is the project-owned multi-sub routing document.
type RoleMap struct {
	// SchemaVersion is the wire format (RoleMapSchemaVersion). Distinct from
	// content revision / sha256.
	SchemaVersion int `json:"role_map_schema_version"`
	// StrictDelegation enables host-authoritative spawn --role only.
	StrictDelegation bool `json:"strictDelegation,omitempty"`
	// OrchestratorRole is the role id used for orchestrator sessions (default "orchestrator").
	OrchestratorRole string `json:"orchestratorRole,omitempty"`
	// Roles is the catalog keyed by role id (implementor, ui, reviewer, …).
	Roles map[string]RoleBinding `json:"roles,omitempty"`
	// Failover is optional per-role concrete ladders.
	Failover FailoverConfig `json:"failover,omitempty"`
}

// IsZero reports an empty role map (legacy projects).
func (m RoleMap) IsZero() bool {
	return m.SchemaVersion == 0 && !m.StrictDelegation && len(m.Roles) == 0 &&
		m.OrchestratorRole == "" && m.Failover.Mode == "" && len(m.Failover.Roles) == 0
}

// WithDefaults fills schema version and orchestrator role when unset but map is used.
func (m RoleMap) WithDefaults() RoleMap {
	if m.IsZero() {
		return m
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = RoleMapSchemaVersion
	}
	if strings.TrimSpace(m.OrchestratorRole) == "" {
		m.OrchestratorRole = "orchestrator"
	}
	if m.Failover.Mode == "" && len(m.Failover.Roles) > 0 {
		m.Failover.Mode = FailoverModeManual
	}
	return m
}

// SHA256 returns a stable content hash of the role map (canonical JSON).
func (m RoleMap) SHA256() (string, error) {
	// Normalize for stable hashing: sort role keys via json.Marshal of maps is
	// randomized in Go — re-encode through sorted structure.
	type wire struct {
		SchemaVersion    int                    `json:"role_map_schema_version"`
		StrictDelegation bool                   `json:"strictDelegation,omitempty"`
		OrchestratorRole string                 `json:"orchestratorRole,omitempty"`
		Roles            map[string]RoleBinding `json:"roles,omitempty"`
		Failover         FailoverConfig         `json:"failover,omitempty"`
	}
	// Field-for-field identical to RoleMap, so a conversion is exact and cannot
	// silently drop a field the way a literal does when RoleMap gains one.
	w := wire(m)
	// Encode roles deterministically by rebuilding with sorted keys in JSON via
	// a slice form for hashing only.
	type roleKV struct {
		ID      string      `json:"id"`
		Binding RoleBinding `json:"binding"`
	}
	type failKV struct {
		ID      string           `json:"id"`
		Targets []FailoverTarget `json:"targets"`
	}
	type hashDoc struct {
		SchemaVersion    int      `json:"role_map_schema_version"`
		StrictDelegation bool     `json:"strictDelegation"`
		OrchestratorRole string   `json:"orchestratorRole"`
		Roles            []roleKV `json:"roles"`
		FailoverMode     string   `json:"failoverMode"`
		Failover         []failKV `json:"failover"`
	}
	_ = w
	doc := hashDoc{
		SchemaVersion:    m.SchemaVersion,
		StrictDelegation: m.StrictDelegation,
		OrchestratorRole: m.OrchestratorRole,
		FailoverMode:     string(m.Failover.Mode),
	}
	roleIDs := make([]string, 0, len(m.Roles))
	for id := range m.Roles {
		roleIDs = append(roleIDs, id)
	}
	sort.Strings(roleIDs)
	for _, id := range roleIDs {
		doc.Roles = append(doc.Roles, roleKV{ID: id, Binding: m.Roles[id]})
	}
	failIDs := make([]string, 0, len(m.Failover.Roles))
	for id := range m.Failover.Roles {
		failIDs = append(failIDs, id)
	}
	sort.Strings(failIDs)
	for _, id := range failIDs {
		doc.Failover = append(doc.Failover, failKV{ID: id, Targets: m.Failover.Roles[id]})
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Validate checks vocabulary and internal consistency. Capability matrix
// (spawn_supported / read_only_enforced) is applied by the roles package with
// a HarnessCapability view — not here — so pure domain tests stay free of adapters.
func (m RoleMap) Validate() error {
	if m.IsZero() {
		return nil
	}
	if m.SchemaVersion != 0 && m.SchemaVersion != RoleMapSchemaVersion {
		return fmt.Errorf("role_map_schema_version: unsupported %d (want %d)", m.SchemaVersion, RoleMapSchemaVersion)
	}
	if len(m.Roles) == 0 {
		return fmt.Errorf("roles: must not be empty when role map is set")
	}
	orch := strings.TrimSpace(m.OrchestratorRole)
	if orch == "" {
		orch = "orchestrator"
	}
	if _, ok := m.Roles[orch]; !ok {
		return fmt.Errorf("orchestratorRole %q: not present in roles", orch)
	}
	for id, b := range m.Roles {
		if err := validateRoleID(id); err != nil {
			return err
		}
		tmpl := strings.TrimSpace(b.Template)
		if tmpl == "" {
			return fmt.Errorf("roles[%s].template: required", id)
		}
		// A profile id, not a filename. The loader appends the extension, so
		// "implementor.md" resolved to "implementor.md.md" and failed at SPAWN
		// time with a path nobody wrote — long after the config was accepted.
		// Refuse it where it is authored instead.
		if strings.HasSuffix(strings.ToLower(tmpl), ".md") {
			return fmt.Errorf("roles[%s].template: %q is a profile id, not a filename — drop the .md", id, b.Template)
		}
		if strings.ContainsAny(tmpl, `/\`) {
			return fmt.Errorf("roles[%s].template: %q is a profile id, not a path", id, b.Template)
		}
		if b.Harness == "" || !b.Harness.IsKnown() {
			return fmt.Errorf("roles[%s].harness: unknown harness %q", id, b.Harness)
		}
		if strings.EqualFold(strings.TrimSpace(b.Model), "default") {
			return fmt.Errorf("roles[%s].model: use empty string for provider default, not %q", id, b.Model)
		}
	}
	// Orchestrator must be able to spawn and must not write under strict maps.
	ob := m.Roles[orch]
	if m.StrictDelegation {
		if !ob.Permissions.CanSpawn {
			return fmt.Errorf("roles[%s].permissions.canSpawn: must be true for orchestrator under strictDelegation", orch)
		}
		if ob.Permissions.WorkspaceWrites {
			return fmt.Errorf("roles[%s].permissions.workspaceWrites: must be false for orchestrator under strictDelegation", orch)
		}
	}
	switch m.Failover.Mode {
	case "", FailoverModeManual, FailoverModeAutomatic:
	default:
		return fmt.Errorf("failover.mode: invalid %q (want manual|automatic)", m.Failover.Mode)
	}
	for roleID, rungs := range m.Failover.Roles {
		if _, ok := m.Roles[roleID]; !ok {
			return fmt.Errorf("failover.roles[%s]: role not defined", roleID)
		}
		for i, t := range rungs {
			if t.Harness == "" || !t.Harness.IsKnown() {
				return fmt.Errorf("failover.roles[%s][%d].harness: unknown %q", roleID, i, t.Harness)
			}
			if strings.EqualFold(strings.TrimSpace(t.Model), "default") {
				return fmt.Errorf("failover.roles[%s][%d].model: use empty, not %q", roleID, i, t.Model)
			}
		}
	}
	return nil
}

// Get returns a role binding or false.
func (m RoleMap) Get(roleID string) (RoleBinding, bool) {
	if m.Roles == nil {
		return RoleBinding{}, false
	}
	b, ok := m.Roles[strings.TrimSpace(roleID)]
	return b, ok
}

func validateRoleID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("role id: empty")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("role id %q: must be lowercase alphanumeric, hyphen, or underscore", id)
	}
	return nil
}

// SessionRoleBinding is the durable role identity pinned on a session at spawn.
type SessionRoleBinding struct {
	RoleID               string              `json:"roleId,omitempty"`
	RoleMapSchemaVersion int                 `json:"roleMapSchemaVersion,omitempty"`
	RoleMapSHA256        string              `json:"roleMapSha256,omitempty"`
	RoleConfigRevision   int64               `json:"roleConfigRevision,omitempty"`
	TemplateArtifactID   string              `json:"templateArtifactId,omitempty"`
	TemplateSHA256       string              `json:"templateSha256,omitempty"`
	ResolvedHarness      AgentHarness        `json:"resolvedHarness,omitempty"`
	ResolvedModel        string              `json:"resolvedModel,omitempty"`
	ResolvedPermissions  RoleExecutionPolicy `json:"resolvedPermissions,omitempty"`
}
