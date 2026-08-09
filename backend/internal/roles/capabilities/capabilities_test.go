package capabilities

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestFor_Phase1Cells(t *testing.T) {
	claude := For(domain.HarnessClaudeCode)
	if !claude.SpawnSupported {
		t.Fatalf("claude-code must support spawn: %+v", claude)
	}
	if claude.ReadOnlyEnforced {
		t.Fatalf("claude-code must not claim read_only_enforced until dontAsk/OS sandbox: %+v", claude)
	}
	if !claude.SwitchSupported {
		t.Fatalf("claude-code switch_supported must be true after Phase 2A promotion: %+v", claude)
	}
	if claude.LimitDetectionSupported {
		t.Fatalf("claude-code limit_detection must stay false until Phase 3: %+v", claude)
	}

	codex := For(domain.HarnessCodex)
	if !codex.SpawnSupported || !codex.ReadOnlyEnforced {
		t.Fatalf("codex: %+v", codex)
	}
	if !codex.SwitchSupported {
		t.Fatalf("codex switch_supported must be true after Phase 2A promotion: %+v", codex)
	}
	if codex.LimitDetectionSupported {
		t.Fatalf("codex limit_detection must stay false until Phase 3: %+v", codex)
	}

	pi := For(domain.HarnessPi)
	if !pi.SpawnSupported || pi.ReadOnlyEnforced {
		t.Fatalf("pi must spawn but not RO: %+v", pi)
	}
	if pi.SwitchSupported {
		t.Fatalf("pi switch_supported must stay false until a dedicated promote: %+v", pi)
	}
}

// Muse arrived with a working adapter but no registry entry, so For() fell to the
// zero-value default and role maps saw spawn_supported=false for a harness the
// ordinary spawn path launches. Registering it must fix exactly that cell and
// promote nothing else — a wrong `true` here is a guarantee AO cannot keep.
func TestFor_MuseSpawnOnly(t *testing.T) {
	muse := For(domain.HarnessMuse)
	for _, tc := range []struct {
		cell string
		got  bool
		want bool
		why  string
	}{
		{"spawn_supported", muse.SpawnSupported, true,
			"the adapter builds argv, injects the developer prompt and installs managed hooks"},
		{"switch_supported", muse.SwitchSupported, false,
			"no switch saga has ever run against Muse, as source or target"},
		{"read_only_enforced", muse.ReadOnlyEnforced, false,
			"Muse's flags only widen approval; it has no workspace write-denial flag"},
		{"limit_detection_supported", muse.LimitDetectionSupported, false,
			"limit detection is Phase 3 and unexercised for every production harness"},
	} {
		if tc.got != tc.want {
			t.Fatalf("muse %s = %v, want %v — %s (caps: %+v)", tc.cell, tc.got, tc.want, tc.why, muse)
		}
	}
	if muse.Notes == "" {
		t.Fatalf("muse caps must record what is and is not proven: %+v", muse)
	}
}

func TestValidateRoleMap_AllowsMuseForWritableRole(t *testing.T) {
	// The whole point of registering Muse: a writable role may bind it, because
	// the ordinary spawn path already reaches the adapter.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessMuse,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatalf("writable muse role must validate: %v", err)
	}
}

func TestValidateRoleMap_RejectsMuseReadOnly(t *testing.T) {
	// Muse has no write-denial flag, so workspaceWrites=false must fail at
	// config-save on the same read_only_enforced path Claude/Pi take — this is
	// what surfaces as READ_ONLY_UNSUPPORTED rather than a prompt-only pretence.
	m := domain.RoleMap{
		SchemaVersion:    domain.RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]domain.RoleBinding{
			"orchestrator": {
				Template: "orchestrator", Harness: domain.HarnessMuse,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: false, CanSpawn: true},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "read_only_enforced") {
		t.Fatalf("err = %v, want read_only_enforced reject", err)
	}
	// applyRoleMap/switch wrap exactly this call into ErrReadOnlyUnsupported, so
	// the launch-time refusal and the config-save refusal cannot drift apart.
	if err := RequireReadOnly(domain.HarnessMuse); err == nil {
		t.Fatal("RequireReadOnly(muse) succeeded; launch would allow a read-only Muse role")
	}
}

func TestValidateRoleMap_MuseCannotBeFailoverSource(t *testing.T) {
	// Same shape as the Pi source case: Muse spawns fine, so a ladder on a
	// Muse-primary role passes every other gate and would only fail at runtime
	// with ErrSwitchNotSupported.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"ui": {
				Template: "ui-implementor", Harness: domain.HarnessMuse,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"ui": {{Harness: domain.HarnessCodex}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "switch_supported") {
		t.Fatalf("err=%v want switch_supported reject on muse failover source", err)
	}
	if !strings.Contains(err.Error(), "roles[ui]") {
		t.Fatalf("err=%v must attribute the reject to the primary binding, not a rung", err)
	}
}

func TestValidateRoleMap_MuseCannotBeFailoverRung(t *testing.T) {
	// The target side of the same claim: a switch-incapable rung is unreachable.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {{Harness: domain.HarnessMuse}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "switch_supported") {
		t.Fatalf("err=%v want switch_supported reject on muse rung", err)
	}
}

func TestValidateRoleMap_RejectsClaudeAndPiReadOnly(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessPi} {
		m := domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			StrictDelegation: true,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {
					Template: "orchestrator",
					Harness:  harness,
					Permissions: domain.RoleExecutionPolicy{
						WorkspaceWrites: false,
						CanSpawn:        true,
					},
				},
			},
		}
		err := ValidateRoleMap(m)
		if err == nil || !strings.Contains(err.Error(), "read_only_enforced") {
			t.Fatalf("harness %q: err = %v, want read_only_enforced reject", harness, err)
		}
	}
}

func TestValidateRoleMap_AllowsStrictWritableClaudeOrchestratorAndCodexRung(t *testing.T) {
	m := domain.RoleMap{
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
		},
		Failover: domain.FailoverConfig{
			Mode: domain.FailoverModeManual,
			Roles: map[string][]domain.FailoverTarget{
				"orchestrator": {{Harness: domain.HarnessCodex}},
			},
		},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("domain validation rejected strict writable orchestrator: %v", err)
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatalf("capability validation rejected Claude/Codex writable ladder: %v", err)
	}
	if For(domain.HarnessClaudeCode).ReadOnlyEnforced {
		t.Fatal("strict writable orchestration must not promote Claude read_only_enforced")
	}
}

func TestValidateRoleMap_AllowsCodexReadOnly(t *testing.T) {
	m := domain.RoleMap{
		SchemaVersion:    domain.RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]domain.RoleBinding{
			"orchestrator": {
				Template: "orchestrator",
				Harness:  domain.HarnessCodex,
				Permissions: domain.RoleExecutionPolicy{
					WorkspaceWrites: false,
					CanSpawn:        true,
				},
			},
			"implementor": {
				Template: "implementor",
				Harness:  domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{
					WorkspaceWrites: true,
					CanSpawn:        false,
				},
			},
		},
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRoleMap_FailoverRungsInheritRO(t *testing.T) {
	// RO orchestrator failover to Claude (no RO) must fail at config-save.
	m := domain.RoleMap{
		SchemaVersion:    domain.RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]domain.RoleBinding{
			"orchestrator": {
				Template: "orchestrator",
				Harness:  domain.HarnessCodex,
				Permissions: domain.RoleExecutionPolicy{
					WorkspaceWrites: false,
					CanSpawn:        true,
				},
			},
		},
		Failover: domain.FailoverConfig{
			Mode: domain.FailoverModeManual,
			Roles: map[string][]domain.FailoverTarget{
				"orchestrator": {{Harness: domain.HarnessClaudeCode}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "read_only_enforced") {
		t.Fatalf("err=%v want failover RO reject", err)
	}
}

func TestValidateRoleMap_FailoverRungsRequireSpawn(t *testing.T) {
	// Unknown harness has spawn_supported=false.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {{Harness: domain.AgentHarness("not-a-real-harness")}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "spawn_supported") {
		t.Fatalf("err=%v want spawn reject on failover", err)
	}
}

func TestValidateRoleMap_FailoverAllowsCodexForWriterRole(t *testing.T) {
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {{Harness: domain.HarnessCodex}},
			},
		},
	}
	// Claude/Codex both advertise switch_supported after Phase 2A promotion.
	if err := ValidateRoleMap(m); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRoleMap_FailoverRungsRequireSwitch(t *testing.T) {
	// After promotion, failover rungs must advertise SwitchSupported.
	// Pi spawns but has switch_supported=false.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {{Harness: domain.HarnessPi}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "switch_supported") {
		t.Fatalf("err=%v want switch_supported reject on failover", err)
	}
}

func TestValidateRoleMap_FailoverSourceRequiresSwitch(t *testing.T) {
	// Inverse of TestValidateRoleMap_FailoverRungsRequireSwitch: the rung is
	// switch-capable but the primary is not. Pi cannot originate a switch, so a
	// ladder on a Pi-primary role must be rejected at config-save rather than
	// deferring to ErrSwitchNotSupported at runtime.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"ui": {
				Template: "ui-implementor", Harness: domain.HarnessPi,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{
				"ui": {{Harness: domain.HarnessCodex}},
			},
		},
	}
	err := ValidateRoleMap(m)
	if err == nil || !strings.Contains(err.Error(), "switch_supported") {
		t.Fatalf("err=%v want switch_supported reject on failover source", err)
	}
	if !strings.Contains(err.Error(), "roles[ui]") {
		t.Fatalf("err=%v must attribute the reject to the primary binding, not a rung", err)
	}
}

func TestValidateRoleMap_PiPrimaryWithoutLadderAllowed(t *testing.T) {
	// Guard against over-rejection: a switch-incapable harness is still a valid
	// spawn-only primary. Only a configured ladder makes it a switch source.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"ui": {
				Template: "ui-implementor", Harness: domain.HarnessPi,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatalf("pi primary without a ladder must remain valid: %v", err)
	}
}

func TestValidateRoleMap_EmptyLadderDoesNotRequireSwitch(t *testing.T) {
	// An explicitly empty ladder is not a switch source.
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"ui": {
				Template: "ui-implementor", Harness: domain.HarnessPi,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Roles: map[string][]domain.FailoverTarget{"ui": {}},
		},
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatalf("empty ladder must not require switch_supported: %v", err)
	}
}

// TestValidateRoleMap_ShippedStrictExampleValidates keeps the documented
// copy-pasteable role map usable end to end. It reproduces the real set-config
// path in order: strict decode (PUT /projects/{id}/config uses
// DisallowUnknownFields, so an annotation key 400s before validation is even
// reached), then domain validation, then the capability registry. Capability
// promotion changes what a valid ladder may contain, so without this guard the
// example silently becomes a config the daemon rejects.
func TestValidateRoleMap_ShippedStrictExampleValidates(t *testing.T) {
	const rel = "../../../../docs/roles/examples/role-map.strict.example.json"
	raw, err := os.ReadFile(rel)
	if err != nil {
		// Deliberately fatal rather than a skip: docs/ is part of this monorepo
		// checkout, so renaming or deleting the canonical example must break this
		// guard instead of silently disabling it.
		t.Fatalf("canonical example unreadable (%s): %v", rel, err)
	}
	var m domain.RoleMap
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("shipped example fails strict decode (set-config would 400 before "+
			"validation; keep annotations in examples/README.md, not the JSON): %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("shipped example fails domain validation: %v", err)
	}
	if err := ValidateRoleMap(m); err != nil {
		t.Fatalf("shipped example fails capability validation: %v", err)
	}
}

func TestSwitchSupportedPromoted(t *testing.T) {
	if !switchSupportedPromoted() {
		t.Fatal("switchSupportedPromoted must be true once Claude/Codex cells are on")
	}
}

func automaticRoleMapForCapabilityTest() domain.RoleMap {
	return domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor", Harness: domain.HarnessClaudeCode,
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
		Failover: domain.FailoverConfig{
			Mode: domain.FailoverModeAutomatic,
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {{Harness: domain.HarnessCodex}},
			},
		},
	}
}

func TestValidateRoleMap_AutomaticRequiresPromotedSourceLimitDetection(t *testing.T) {
	err := ValidateRoleMap(automaticRoleMapForCapabilityTest())
	if err == nil || !strings.Contains(err.Error(), "roles[implementor]") ||
		!strings.Contains(err.Error(), "limit_detection_supported=false") {
		t.Fatalf("err = %v, want source limit-detection refusal", err)
	}
}

func TestValidateRoleMap_AutomaticRequiresPromotedEveryRungLimitDetection(t *testing.T) {
	err := validateRoleMapWithCaps(automaticRoleMapForCapabilityTest(), func(h domain.AgentHarness) Caps {
		c := For(h)
		c.LimitDetectionSupported = h == domain.HarnessClaudeCode
		return c
	})
	if err == nil || !strings.Contains(err.Error(), "failover.roles[implementor][0]") ||
		!strings.Contains(err.Error(), "limit_detection_supported=false") {
		t.Fatalf("err = %v, want rung limit-detection refusal", err)
	}
}

func TestValidateRoleMap_AutomaticPassesOnlyWithInjectedReviewedCells(t *testing.T) {
	err := validateRoleMapWithCaps(automaticRoleMapForCapabilityTest(), func(h domain.AgentHarness) Caps {
		c := For(h)
		if h == domain.HarnessClaudeCode || h == domain.HarnessCodex {
			c.LimitDetectionSupported = true
		}
		return c
	})
	if err != nil {
		t.Fatalf("reviewed source+rung cells should validate in the test-only promoted matrix: %v", err)
	}
}

func TestEveryShippedHarnessKeepsLimitDetectionFalse(t *testing.T) {
	for h, c := range AllDocumented() {
		if c.LimitDetectionSupported {
			t.Fatalf("%s promoted limit_detection_supported in the implementation slice", h)
		}
	}
	if For(domain.HarnessClaudeCode).ReadOnlyEnforced {
		t.Fatal("Claude read_only_enforced changed while implementing automatic failover")
	}
}
