package readonly

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestApplyLaunch_CodexSetsReadOnlyFlag(t *testing.T) {
	cfg := ports.LaunchConfig{Permissions: ports.PermissionModeDefault}
	ApplyLaunch(domain.HarnessCodex, &cfg)
	if !cfg.ReadOnly {
		t.Fatal("ReadOnly not set")
	}
	if cfg.Permissions == ports.PermissionModeDefault || cfg.Permissions == ports.PermissionModeBypassPermissions {
		t.Fatalf("codex RO must not leave bypass/default: %q", cfg.Permissions)
	}
}

func TestApplyRestore_CodexReadOnly(t *testing.T) {
	cfg := ports.RestoreConfig{Permissions: ports.PermissionModeDefault}
	ApplyRestore(domain.HarnessCodex, &cfg)
	if !cfg.ReadOnly {
		t.Fatal("restore ReadOnly not set")
	}
}

func TestApplyLaunch_ClaudeDoesNotClaimRO(t *testing.T) {
	// Claude ReadOnlyEnforced=false: helper must not mark ReadOnly or tool policy
	// that would overstate enforcement under auto mode.
	cfg := ports.LaunchConfig{Permissions: ports.PermissionModeBypassPermissions}
	ApplyLaunch(domain.HarnessClaudeCode, &cfg)
	if cfg.ReadOnly {
		t.Fatal("Claude must not claim LaunchConfig.ReadOnly while unenforced")
	}
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		t.Fatalf("Claude must not emit RO tool lists: allowed=%v denied=%v", cfg.AllowedTools, cfg.DisallowedTools)
	}
}
