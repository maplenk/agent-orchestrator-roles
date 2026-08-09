package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// An acknowledgement is a tuple, not merely a matching switch id or target
// generation. A hook from another AO session must not be able to complete the
// target's delivery even when it guesses the current generation exactly.
func TestAgentSwitchAcknowledgementRequiresExactSessionGenerationTuple(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "switch-ack-tuple")
	session, err := s.CreateSession(ctx, sampleRecord("switch-ack-tuple"))
	if err != nil {
		t.Fatalf("create AO session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	target := domain.AgentNativeSession{
		ID: "ack-tuple-target", AOSessionID: session.ID, Harness: domain.HarnessCodex,
		NativeSessionID:  "codex-ack-tuple",
		LastGenerationID: "target-generation", CreatedAt: now, LastUsedAt: now,
	}
	if _, _, err := s.CreateAgentNativeSession(ctx, target); err != nil {
		t.Fatalf("create target native session: %v", err)
	}
	targetRef := target.ID
	sw := domain.AgentSwitch{
		ID: "switch-ack-tuple", SessionID: session.ID, IdempotencyKey: "switch-ack-tuple",
		RequestFingerprint: domain.ComputeAgentSwitchRequestFingerprint(session.ID, domain.HarnessCodex, ""),
		FromHarness:        domain.HarnessClaudeCode, TargetHarness: domain.HarnessCodex,
		State: domain.AgentSwitchPreparingHandoff, AgentHandoffStatus: domain.AgentHandoffNotAttempted,
		SourceGenerationID: "source-generation", RequestedAt: now, UpdatedAt: now,
	}
	stored, created, err := s.CreateAgentSwitch(ctx, sw)
	if err != nil || !created {
		t.Fatalf("create switch: created=%v err=%v", created, err)
	}
	for step, state := range []domain.AgentSwitchState{
		domain.AgentSwitchStoppingSource,
		domain.AgentSwitchSourceStopped,
		domain.AgentSwitchStartingTarget,
		domain.AgentSwitchTargetReady,
		domain.AgentSwitchDelivering,
	} {
		if state == domain.AgentSwitchStoppingSource {
			advanceAgentSwitchFixtureWithMutation(ctx, t, s, &stored, state, now.Add(time.Duration(step+1)*time.Second), func(next *domain.AgentSwitch) {
				next.TargetNativeSessionRef = &targetRef
				next.TargetStartMode = domain.AgentSwitchTargetStartFresh
				next.TargetGenerationID = "target-generation"
			})
			continue
		}
		advanceAgentSwitchFixture(ctx, t, s, &stored, state, now.Add(time.Duration(step+1)*time.Second))
	}

	acknowledgedAt := stored.UpdatedAt.Add(time.Second)
	for _, tc := range []struct {
		name       string
		sessionID  domain.SessionID
		generation domain.AgentGenerationID
	}{
		{name: "wrong session exact generation", sessionID: "another-ao-session", generation: stored.TargetGenerationID},
		{name: "exact session stale generation", sessionID: stored.SessionID, generation: "stale-target-generation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ok, err := s.AcknowledgeAgentSwitchTarget(ctx, stored.ID, tc.sessionID, tc.generation, acknowledgedAt); err != nil || ok {
				t.Fatalf("mismatched acknowledgement: ok=%v err=%v", ok, err)
			}
			current, ok, err := s.GetAgentSwitch(ctx, stored.ID)
			if err != nil || !ok {
				t.Fatalf("reload switch: ok=%v err=%v", ok, err)
			}
			if current.TargetAcknowledgedAt != nil || current.State != domain.AgentSwitchDelivering {
				t.Fatalf("mismatched acknowledgement mutated delivery: %+v", current)
			}
		})
	}

	if ok, err := s.AcknowledgeAgentSwitchTarget(ctx, stored.ID, stored.SessionID, stored.TargetGenerationID, acknowledgedAt); err != nil || !ok {
		t.Fatalf("exact tuple acknowledgement: ok=%v err=%v", ok, err)
	}
}
