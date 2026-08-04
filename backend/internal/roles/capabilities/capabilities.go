// Package capabilities is the machine-readable harness capability registry.
// CAPABILITY_MATRIX.md is generated/mirror documentation only — validation
// and launch/restore must consult this package, not the markdown file.
package capabilities

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Caps describes what AO may claim for a harness at config-save, launch, and restore.
// Phase 1 populates SpawnSupported and ReadOnlyEnforced only.
// SwitchSupported and LimitDetectionSupported stay false until Phase 2 / 3 promote them.
type Caps struct {
	SpawnSupported          bool
	SwitchSupported         bool
	LimitDetectionSupported bool
	ReadOnlyEnforced        bool
	// Notes is human-readable; not parsed by validation.
	Notes string
}

// For returns capabilities for a harness. Unknown harnesses get zeros (fail closed).
// Platform / binary-version variants can be added later without changing call sites.
func For(h domain.AgentHarness) Caps {
	switch h {
	case domain.HarnessClaudeCode:
		// auto is classifier-based auto-approval, not deny-by-default (dontAsk).
		// Tool allow/deny under auto cannot support read_only_enforced=true until
		// we use dontAsk + no write-capable Bash + non-shell spawn (or an OS sandbox).
		// See docs/roles/READ_ONLY_CONTRACT.md and Claude permissions docs.
		return Caps{
			SpawnSupported:   true,
			SwitchSupported:  false, // promote only after ownership fence + dogfood
			ReadOnlyEnforced: false,
			Notes:            "spawn supported; switch_supported=false until Phase 2A gates; RO deferred",
		}
	case domain.HarnessCodex:
		return Caps{
			SpawnSupported:   true,
			SwitchSupported:  false, // promote only after ownership fence + dogfood
			ReadOnlyEnforced: true,
			Notes:            "RO via --sandbox read-only; switch_supported=false until Phase 2A gates",
		}
	case domain.HarnessPi:
		return Caps{
			SpawnSupported:   true,
			ReadOnlyEnforced: false,
			Notes:            "Pi emits no permission/sandbox flags; external sandbox required for RO",
		}
	case domain.HarnessGrok, domain.HarnessOpenCode, domain.HarnessAider,
		domain.HarnessDroid, domain.HarnessAmp, domain.HarnessAgy, domain.HarnessCrush,
		domain.HarnessCursor, domain.HarnessQwen, domain.HarnessCopilot, domain.HarnessGoose,
		domain.HarnessAuggie, domain.HarnessContinue, domain.HarnessDevin, domain.HarnessCline,
		domain.HarnessKimi, domain.HarnessKiro, domain.HarnessKilocode, domain.HarnessVibe,
		domain.HarnessAutohand:
		return Caps{
			SpawnSupported:   true,
			ReadOnlyEnforced: false,
			Notes:            "spawn supported; read_only_enforced not implemented",
		}
	case domain.HarnessFake:
		// Test harness: spawnable, RO, switchable for unit tests.
		return Caps{
			SpawnSupported:   true,
			SwitchSupported:  true,
			ReadOnlyEnforced: true,
			Notes:            "test harness only",
		}
	default:
		return Caps{}
	}
}

// ValidateRoleMap rejects role bindings that claim unsupported capabilities.
// Call at config-save (and any path that accepts a RoleMap).
//
// Failover rungs are executable switch/failover targets and inherit the owning
// role's permissions for read-only enforcement. spawn_supported is always
// required. switch_supported is enforced on failover rungs only after production
// harnesses advertise SwitchSupported (same flip as capability promotion).
func ValidateRoleMap(m domain.RoleMap) error {
	if m.IsZero() {
		return nil
	}
	for id, b := range m.Roles {
		if err := validateExecutableTarget(id, b.Harness, b.Permissions, false); err != nil {
			return err
		}
	}
	for roleID, targets := range m.Failover.Roles {
		owner, ok := m.Roles[roleID]
		if !ok {
			// domain.RoleMap.Validate already rejects unknown failover roles;
			// fail closed here if called in isolation.
			return fmt.Errorf("failover.roles[%s]: role not present in roles", roleID)
		}
		for i, t := range targets {
			if err := validateExecutableTarget(
				fmt.Sprintf("failover.roles[%s][%d]", roleID, i),
				t.Harness, owner.Permissions, true,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateExecutableTarget checks spawn and RO. When failoverRung is true and
// production switch has been promoted, also require SwitchSupported.
func validateExecutableTarget(label string, h domain.AgentHarness, perms domain.RoleExecutionPolicy, failoverRung bool) error {
	caps := For(h)
	if !caps.SpawnSupported {
		return fmt.Errorf("%s: harness %q does not support spawn (spawn_supported=false)", label, h)
	}
	if !perms.WorkspaceWrites && !caps.ReadOnlyEnforced {
		return fmt.Errorf("%s: workspaceWrites=false requires harness %q with read_only_enforced (got false)", label, h)
	}
	if failoverRung && switchSupportedPromoted() && !caps.SwitchSupported {
		return fmt.Errorf("%s: harness %q does not support switch (switch_supported=false)", label, h)
	}
	return nil
}

// switchSupportedPromoted is true once any production switch matrix cell is on.
// Until then, config-save allows authoring failover ladders while runtime still
// refuses switch with SWITCH_NOT_SUPPORTED. Promotion flips For() cells and
// this gate together.
func switchSupportedPromoted() bool {
	return For(domain.HarnessClaudeCode).SwitchSupported || For(domain.HarnessCodex).SwitchSupported
}

// RequireReadOnly reports whether harness may launch with workspaceWrites=false.
func RequireReadOnly(h domain.AgentHarness) error {
	caps := For(h)
	if !caps.ReadOnlyEnforced {
		return fmt.Errorf("harness %q cannot enforce workspaceWrites=false (read_only_enforced=false)", h)
	}
	return nil
}

// AllDocumented returns caps for every domain harness (for matrix docs / tests).
func AllDocumented() map[domain.AgentHarness]Caps {
	out := make(map[domain.AgentHarness]Caps, len(domain.AllHarnesses)+1)
	for _, h := range domain.AllHarnesses {
		out[h] = For(h)
	}
	out[domain.HarnessFake] = For(domain.HarnessFake)
	return out
}
