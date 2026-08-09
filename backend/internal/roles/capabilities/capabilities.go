// Package capabilities is the machine-readable harness capability registry.
// CAPABILITY_MATRIX.md is generated/mirror documentation only — validation
// and launch/restore must consult this package, not the markdown file.
package capabilities

import (
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Caps describes what AO may claim for a harness at config-save, launch, and restore.
// LimitDetectionSupported stays false until Phase 3 promotes it.
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
			SwitchSupported:  true, // Phase 2A dogfood + datadirlock close-out accepted
			ReadOnlyEnforced: false,
			Notes:            "spawn + switch supported; RO deferred (dontAsk/OS sandbox)",
		}
	case domain.HarnessCodex:
		return Caps{
			SpawnSupported:   true,
			SwitchSupported:  true, // Phase 2A dogfood + datadirlock close-out accepted
			ReadOnlyEnforced: true,
			Notes:            "RO via --sandbox read-only; switch supported (Phase 2A)",
		}
	case domain.HarnessMuse:
		// Muse landed with a working ordinary spawn path but no entry here, so a
		// role map read it as spawn_supported=false and refused a harness the
		// daemon launches fine. Only spawn is evidenced: the adapter builds argv,
		// injects the developer prompt, and installs managed hooks. Its approval
		// flags only *widen* approval (--approval-mode never / --yolo) — there is
		// no write-denial flag — and neither switch nor limit detection has ever
		// been exercised against it.
		return Caps{
			SpawnSupported:          true,
			SwitchSupported:         false,
			LimitDetectionSupported: false,
			ReadOnlyEnforced:        false,
			Notes: "spawn proven (argv + developer-prompt env + managed hooks); " +
				"no write-denial flag, so RO unenforceable; switch/limit detection unproven",
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
// required. Once production harnesses advertise SwitchSupported, switch_supported
// is required on every failover rung *and* on the primary binding of any role
// with a non-empty ladder — the primary is the switch source, not just a spawn
// target.
func ValidateRoleMap(m domain.RoleMap) error {
	return validateRoleMapWithCaps(m, For)
}

func validateRoleMapWithCaps(m domain.RoleMap, lookup func(domain.AgentHarness) Caps) error {
	if m.IsZero() {
		return nil
	}
	for id, b := range m.Roles {
		if err := validateExecutableTarget(id, b.Harness, b.Permissions, false); err != nil {
			return err
		}
		// A configured ladder makes this role's primary the switch *source*. A
		// harness that cannot originate a switch (Pi) would otherwise pass
		// config-save and only fail later at runtime with ErrSwitchNotSupported —
		// exactly the silent degrade DoD invariant 9 forbids.
		if len(m.Failover.Roles[id]) > 0 && switchSupportedPromoted() && !For(b.Harness).SwitchSupported {
			return fmt.Errorf("roles[%s]: harness %q has a failover ladder but does not support switch "+
				"(switch_supported=false); remove the ladder or bind a switch-capable harness", id, b.Harness)
		}
		if m.Failover.Mode == domain.FailoverModeAutomatic && len(m.Failover.Roles[id]) > 0 &&
			!lookup(b.Harness).LimitDetectionSupported {
			return fmt.Errorf("roles[%s]: harness %q has automatic failover but does not support structured limit detection "+
				"(limit_detection_supported=false); use manual mode until the detector is promoted", id, b.Harness)
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
			if m.Failover.Mode == domain.FailoverModeAutomatic &&
				!lookup(t.Harness).LimitDetectionSupported {
				return fmt.Errorf("failover.roles[%s][%d]: harness %q has automatic failover but does not support "+
					"structured limit detection (limit_detection_supported=false); use manual mode until the detector is promoted",
					roleID, i, t.Harness)
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
