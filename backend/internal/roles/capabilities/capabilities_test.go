package capabilities

import (
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
	if claude.SwitchSupported || claude.LimitDetectionSupported {
		t.Fatalf("claude-code must default switch/limit false: %+v", claude)
	}

	codex := For(domain.HarnessCodex)
	if !codex.SpawnSupported || !codex.ReadOnlyEnforced {
		t.Fatalf("codex: %+v", codex)
	}

	pi := For(domain.HarnessPi)
	if !pi.SpawnSupported || pi.ReadOnlyEnforced {
		t.Fatalf("pi must spawn but not RO: %+v", pi)
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
