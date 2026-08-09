package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func createRecoveryGuardSwitch(t *testing.T, s *sqlite.Store) domain.AgentSwitch {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "switch-recovery-guard")
	session, err := s.CreateSession(ctx, sampleRecord("switch-recovery-guard"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	sw := domain.AgentSwitch{
		ID:                 "switch-recovery-guard-1",
		SessionID:          session.ID,
		IdempotencyKey:     "recovery-guard-request",
		RequestFingerprint: domain.ComputeAgentSwitchRequestFingerprint(session.ID, domain.HarnessCodex, ""),
		FromHarness:        domain.HarnessClaudeCode,
		TargetHarness:      domain.HarnessCodex,
		State:              domain.AgentSwitchPreparingHandoff,
		TargetStartMode:    domain.AgentSwitchTargetStartPending,
		AgentHandoffStatus: domain.AgentHandoffNotAttempted,
		SourceGenerationID: "source-generation",
		RequestedAt:        now,
		UpdatedAt:          now,
	}
	if _, created, err := s.CreateAgentSwitch(ctx, sw); err != nil || !created {
		t.Fatalf("create agent switch: created=%v err=%v", created, err)
	}
	return sw
}

func TestHasNonterminalAgentSwitch(t *testing.T) {
	t.Run("empty table", func(t *testing.T) {
		s := newTestStore(t)
		if active, err := s.HasNonterminalAgentSwitch(context.Background()); err != nil || active {
			t.Fatalf("active=%v err=%v, want false nil", active, err)
		}
	})

	t.Run("terminal only", func(t *testing.T) {
		s := newTestStore(t)
		sw := createRecoveryGuardSwitch(t, s)
		sw.State = domain.AgentSwitchFailed
		sw.ErrorCode = domain.AgentSwitchErrorRequestCancelled
		sw.UpdatedAt = sw.UpdatedAt.Add(time.Second)
		if changed, err := s.UpdateAgentSwitch(context.Background(), sw, domain.AgentSwitchPreparingHandoff, sw.SourceGenerationID, ""); err != nil || !changed {
			t.Fatalf("close switch: changed=%v err=%v", changed, err)
		}
		if active, err := s.HasNonterminalAgentSwitch(context.Background()); err != nil || active {
			t.Fatalf("active=%v err=%v, want false nil", active, err)
		}
	})

	t.Run("nonterminal", func(t *testing.T) {
		s := newTestStore(t)
		createRecoveryGuardSwitch(t, s)
		if active, err := s.HasNonterminalAgentSwitch(context.Background()); err != nil || !active {
			t.Fatalf("active=%v err=%v, want true nil", active, err)
		}
	})
}
