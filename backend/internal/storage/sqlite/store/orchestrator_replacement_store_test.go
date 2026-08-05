package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestOrchestratorReplacementIntentRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if got, err := st.ListOrchestratorReplacementIntents(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh db: %v %v", got, err)
	}
	if err := st.PutOrchestratorReplacementIntent(ctx, domain.OrchestratorReplacementIntent{
		ProjectID: "mer", RetiredSessionID: "mer-1", RequestedAt: now,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Upsert must not fail and must preserve bookkeeping.
	if err := st.RecordOrchestratorReplacementAttempt(ctx, "mer", now, "spawn failed"); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if err := st.PutOrchestratorReplacementIntent(ctx, domain.OrchestratorReplacementIntent{
		ProjectID: "mer", RetiredSessionID: "mer-2", RequestedAt: now,
	}); err != nil {
		t.Fatalf("re-put: %v", err)
	}
	got, err := st.ListOrchestratorReplacementIntents(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v %v", got, err)
	}
	if got[0].RetiredSessionID != "mer-2" {
		t.Errorf("retired id not refreshed: %+v", got[0])
	}
	if got[0].AttemptCount != 1 || got[0].LastError != "spawn failed" {
		t.Errorf("upsert reset recovery bookkeeping: %+v", got[0])
	}
	if got[0].LastAttemptAt == nil {
		t.Error("last attempt not recorded")
	}
	if err := st.DeleteOrchestratorReplacementIntent(ctx, "mer"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, _ := st.ListOrchestratorReplacementIntents(ctx); len(got) != 0 {
		t.Fatalf("delete left %v", got)
	}
}
