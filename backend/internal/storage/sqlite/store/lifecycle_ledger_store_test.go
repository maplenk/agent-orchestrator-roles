package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestLifecycleLedger_AppendAndListOldestFirst(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	for i, rec := range []domain.LifecycleLedgerRecord{
		{
			ID: "evt-1", SessionID: sess.ID, ProjectID: "mer",
			Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhaseRequested,
			GenerationID: "g1", FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
			RoleID: "implementor", PayloadJSON: `{"step":1}`, CreatedAt: t0,
		},
		{
			ID: "evt-2", SessionID: sess.ID, ProjectID: "mer",
			Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhaseTargetAck,
			GenerationID: "g2", FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
			RoleID: "implementor", PayloadJSON: `{"step":2}`, CreatedAt: t1,
		},
	} {
		if err := s.AppendLifecycleLedger(ctx, rec); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := s.ListLifecycleLedger(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "evt-1" || got[1].ID != "evt-2" {
		t.Fatalf("order = %q, %q", got[0].ID, got[1].ID)
	}
	if got[0].Phase != domain.LifecyclePhaseRequested || got[1].Phase != domain.LifecyclePhaseTargetAck {
		t.Fatalf("phases = %q, %q", got[0].Phase, got[1].Phase)
	}
	if got[0].FromHarness != domain.HarnessClaudeCode || got[1].ToHarness != domain.HarnessCodex {
		t.Fatalf("harnesses unexpected: %+v %+v", got[0], got[1])
	}
}

func TestLifecycleLedger_RejectsInvalidKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	err = s.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
		ID: "bad", SessionID: sess.ID, ProjectID: "mer",
		Kind: "chat_turn", // not a lifecycle kind
	})
	if err == nil {
		t.Fatal("expected invalid kind error")
	}
}
