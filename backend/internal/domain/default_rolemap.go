package domain

import "strings"

const (
	// DefaultOrchestratorRoleID is the starter map's project coordinator role.
	DefaultOrchestratorRoleID = "orchestrator"
	// DefaultImplementorRoleID is the starter map's general implementation role.
	DefaultImplementorRoleID = "implementor"
	// DefaultUIRoleID is the starter map's frontend implementation role.
	DefaultUIRoleID = "ui"
	// DefaultReviewerRoleID is the starter map's read-only review role.
	DefaultReviewerRoleID = "reviewer"
	// DefaultVerifierRoleID is the starter map's read-only verification role.
	DefaultVerifierRoleID = "verifier"
)

// StarterRoleMap returns AO's non-strict role catalog for projects that have
// not authored one. Non-strict is deliberate: existing worker commands keep
// their legacy free-form harness behavior, while orchestrators get a durable
// role and host-authorized Claude/Codex switch targets by default.
func StarterRoleMap(orchestrator, worker FailoverTarget) RoleMap {
	if orchestrator.Harness == "" {
		orchestrator.Harness = HarnessClaudeCode
	}
	if worker.Harness == "" {
		worker = orchestrator
	}
	orchestrator.Model = strings.TrimSpace(orchestrator.Model)
	worker.Model = strings.TrimSpace(worker.Model)

	write := RoleExecutionPolicy{WorkspaceWrites: true}
	roles := map[string]RoleBinding{
		DefaultOrchestratorRoleID: {
			Template: "orchestrator", Harness: orchestrator.Harness, Model: orchestrator.Model,
			Permissions: RoleExecutionPolicy{WorkspaceWrites: true, CanSpawn: true},
		},
		DefaultImplementorRoleID: {
			Template: "implementor", Harness: worker.Harness, Model: worker.Model,
			Permissions: write, When: []string{"implementation", "bugfix", "tests"},
		},
		DefaultUIRoleID: {
			Template: "ui-implementor", Harness: worker.Harness, Model: worker.Model,
			Permissions: write, When: []string{"frontend", "ui"},
		},
		DefaultReviewerRoleID: {
			Template: "reviewer", Harness: HarnessCodex,
			Permissions: RoleExecutionPolicy{WorkspaceWrites: false}, When: []string{"review"},
		},
		DefaultVerifierRoleID: {
			Template: "verifier", Harness: HarnessCodex,
			Permissions: RoleExecutionPolicy{WorkspaceWrites: false}, When: []string{"verification", "acceptance"},
		},
	}

	failover := FailoverConfig{}
	if alternate, ok := switchAlternate(orchestrator.Harness); ok {
		failover.Mode = FailoverModeManual
		failover.Roles = map[string][]FailoverTarget{
			DefaultOrchestratorRoleID: {{Harness: alternate}},
		}
		if worker.Harness == orchestrator.Harness {
			failover.Roles[DefaultImplementorRoleID] = []FailoverTarget{{Harness: alternate}}
			failover.Roles[DefaultUIRoleID] = []FailoverTarget{{Harness: alternate}}
		} else if workerAlternate, workerOK := switchAlternate(worker.Harness); workerOK {
			failover.Roles[DefaultImplementorRoleID] = []FailoverTarget{{Harness: workerAlternate}}
			failover.Roles[DefaultUIRoleID] = []FailoverTarget{{Harness: workerAlternate}}
		}
	}

	return RoleMap{
		SchemaVersion:    RoleMapSchemaVersion,
		OrchestratorRole: DefaultOrchestratorRoleID,
		Roles:            roles,
		Failover:         failover,
	}
}

// AdoptableOrchestratorRole returns the default orchestrator role only when an
// unpinned legacy session is already running that role's exact primary target.
// A non-empty model is intentionally not adopted: legacy rows do not persist
// their effective model, so AO cannot prove that identity without guessing.
func AdoptableOrchestratorRole(m RoleMap, currentHarness AgentHarness) (string, bool) {
	m = m.WithDefaults()
	if m.IsZero() || currentHarness == "" {
		return "", false
	}
	roleID := strings.TrimSpace(m.OrchestratorRole)
	binding, ok := m.Roles[roleID]
	if !ok || binding.Harness != currentHarness || strings.TrimSpace(binding.Model) != "" {
		return "", false
	}
	return roleID, true
}

func switchAlternate(h AgentHarness) (AgentHarness, bool) {
	switch h {
	case HarnessClaudeCode:
		return HarnessCodex, true
	case HarnessCodex:
		return HarnessClaudeCode, true
	default:
		return "", false
	}
}
