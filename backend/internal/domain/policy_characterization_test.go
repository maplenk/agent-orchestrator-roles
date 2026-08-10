package domain_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// This test is the Phase 1 policy seam between failover selection and any
// durable switch engine. An unfinished attempt owns its rung: recovery adopts
// that attempt, while a later allocation may only consider a different rung.
func TestPolicyCharacterization_ActiveFailoverAttemptAdoptionDoesNotSpendAnotherRung(t *testing.T) {
	roleMap := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles: map[string]domain.RoleBinding{
			"implementor": {
				Template: "implementor",
				Harness:  domain.HarnessClaudeCode,
			},
		},
		Failover: domain.FailoverConfig{
			Mode: domain.FailoverModeAutomatic,
			Roles: map[string][]domain.FailoverTarget{
				"implementor": {
					{Harness: domain.HarnessCodex},
					{Harness: domain.HarnessGrok, Model: "grok-code"},
				},
			},
		},
	}
	attempts := []domain.FailoverAttempt{{
		Seq:          1,
		State:        domain.FailoverAttemptPostStop,
		ToHarness:    domain.HarnessCodex,
		GenerationID: "target-gen-1",
	}}

	active, ok := domain.ActiveFailoverAttempt(attempts)
	if !ok || active.Seq != 1 || active.GenerationID != "target-gen-1" {
		t.Fatalf("active attempt = %+v, %v; want the existing post-stop attempt", active, ok)
	}
	if got := domain.NextFailoverSeq(attempts); got != 2 {
		t.Fatalf("next sequence = %d, want 2 only if a later allocation is explicitly authorized", got)
	}

	used := domain.UsedFailoverTargets(attempts)
	if len(used) != 1 || used[0].Harness != domain.HarnessCodex {
		t.Fatalf("used targets = %+v, want exactly the active attempt's rung", used)
	}
	next, index, err := domain.NextFailoverRung(
		roleMap,
		"implementor",
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode},
		used,
	)
	if err != nil {
		t.Fatalf("next failover rung: %v", err)
	}
	if index != 1 || next.Harness != domain.HarnessGrok || next.Model != "grok-code" {
		t.Fatalf("next rung = %+v at %d, want the unspent second rung", next, index)
	}
}
