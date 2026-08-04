// Package readonly applies host-authoritative launch constraints for
// workspaceWrites=false roles. See docs/roles/READ_ONLY_CONTRACT.md.
//
// Only harnesses with capabilities.ReadOnlyEnforced=true may reach these
// helpers for production RO roles. Claude is currently false (auto mode is not
// deny-by-default); Codex uses OS sandbox.
package readonly

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ApplyLaunch mutates a launch config for a role with workspaceWrites=false.
// Call only after capabilities.RequireReadOnly succeeds for the harness.
func ApplyLaunch(harness domain.AgentHarness, cfg *ports.LaunchConfig) {
	if cfg == nil {
		return
	}
	cfg.ReadOnly = true
	switch harness {
	case domain.HarnessCodex:
		// Codex sandbox is applied in the adapter when ReadOnly is set.
		// Avoid bypass/default which map to --dangerously-bypass-approvals-and-sandbox.
		cfg.Permissions = ports.PermissionModeAcceptEdits
		cfg.Config.Permissions = ports.PermissionModeAcceptEdits
	case domain.HarnessFake:
		// Unit tests only — not a production RO claim.
		cfg.Permissions = ports.PermissionModeAcceptEdits
		cfg.Config.Permissions = ports.PermissionModeAcceptEdits
	case domain.HarnessClaudeCode:
		// Should not be reached while ReadOnlyEnforced=false for Claude.
		// Intentionally do not emit tool lists that claim enforcement under auto.
		cfg.ReadOnly = false
	}
}

// ApplyRestore mutates restore config for workspaceWrites=false.
func ApplyRestore(harness domain.AgentHarness, cfg *ports.RestoreConfig) {
	if cfg == nil {
		return
	}
	cfg.ReadOnly = true
	switch harness {
	case domain.HarnessCodex:
		cfg.Permissions = ports.PermissionModeAcceptEdits
		cfg.Config.Permissions = ports.PermissionModeAcceptEdits
	case domain.HarnessFake:
		cfg.Permissions = ports.PermissionModeAcceptEdits
		cfg.Config.Permissions = ports.PermissionModeAcceptEdits
	case domain.HarnessClaudeCode:
		cfg.ReadOnly = false
	}
}
