package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Migration 0046's partial unique index is the database backstop for "one
// active orchestrator per project". SQLite reports it as a bare
// "UNIQUE constraint failed: sessions.project_id (2067)" — no index name, no
// typed shape — which reaches the API as an opaque 500 unless it is mapped.
// Worse for the caller: by the time this fires they have usually already
// created a runtime, and only a recognizable error tells them to reap it.

func newOrchestrator(project string) domain.SessionRecord {
	now := time.Now().UTC()
	return domain.SessionRecord{
		ProjectID: domain.ProjectID(project),
		Kind:      domain.KindOrchestrator,
		Harness:   domain.HarnessCodex,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestCreateSession_SecondActiveOrchestratorIsTypedConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "mer")

	if _, err := st.CreateSession(ctx, newOrchestrator("mer")); err != nil {
		t.Fatalf("first orchestrator: %v", err)
	}

	_, err := st.CreateSession(ctx, newOrchestrator("mer"))
	if !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v, want domain.ErrActiveOrchestratorExists", err)
	}
	// The raw driver text must not be what callers match on.
	if strings.Contains(err.Error(), "2067") {
		t.Errorf("err = %v, want the driver detail replaced by the sentinel", err)
	}
}

// TestUpdateSession_ReactivatingOrchestratorIsTypedConflict is the path that
// matters most: MarkSpawned clears is_terminated, which re-enters the partial
// index. Restore and boot RestoreAll both reach it, and both have a live
// runtime in hand when they do.
func TestUpdateSession_ReactivatingOrchestratorIsTypedConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "mer")

	first, err := st.CreateSession(ctx, newOrchestrator("mer"))
	if err != nil {
		t.Fatalf("first orchestrator: %v", err)
	}
	first.IsTerminated = true
	if err := st.UpdateSession(ctx, first); err != nil {
		t.Fatalf("retire first: %v", err)
	}
	if _, err := st.CreateSession(ctx, newOrchestrator("mer")); err != nil {
		t.Fatalf("successor orchestrator: %v", err)
	}

	// The retired one tries to come back — exactly what MarkSpawned does.
	first.IsTerminated = false
	err = st.UpdateSession(ctx, first)
	if !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v, want domain.ErrActiveOrchestratorExists", err)
	}
	if !strings.Contains(err.Error(), string(first.ID)) {
		t.Errorf("err = %v, want the losing session named", err)
	}
}

// TestSessionWrites_UnaffectedByOrchestratorIndex guards the blast radius: the
// index is partial, so workers and other projects must be untouched. A mapping
// that caught too much would turn ordinary conflicts into a misleading
// "retire your orchestrator" message.
func TestSessionWrites_UnaffectedByOrchestratorIndex(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "mer")
	seedProject(t, st, "other")

	if _, err := st.CreateSession(ctx, newOrchestrator("mer")); err != nil {
		t.Fatalf("orchestrator: %v", err)
	}
	for i := 0; i < 3; i++ {
		w := newOrchestrator("mer")
		w.Kind = domain.KindWorker
		if _, err := st.CreateSession(ctx, w); err != nil {
			t.Fatalf("worker %d in the same project must be unaffected: %v", i, err)
		}
	}
	if _, err := st.CreateSession(ctx, newOrchestrator("other")); err != nil {
		t.Fatalf("orchestrator in a different project must be unaffected: %v", err)
	}
	// A terminated second orchestrator is outside the partial index entirely.
	dup := newOrchestrator("mer")
	dup.IsTerminated = true
	if _, err := st.CreateSession(ctx, dup); err != nil {
		t.Fatalf("terminated orchestrator must be allowed: %v", err)
	}
}

// TestReviewRunDuplicateKeepsItsOwnSentinel checks the neighbouring mapping
// still wins end to end: review_run has carried its own unique index and
// sentinel since 0020. The proof that the orchestrator predicate is
// column-scoped rather than "any unique violation" lives in
// orchestrator_conflict_internal_test.go — this only pins that the two
// mappings coexist.
func TestReviewRunDuplicateKeepsItsOwnSentinel(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedProject(t, st, "mer")
	sess, err := st.CreateSession(ctx, newOrchestrator("mer"))
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Harness: domain.ReviewerClaudeCode, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	run := domain.ReviewRun{
		ID: "run-1", ReviewID: "rev-1", SessionID: sess.ID, Harness: domain.ReviewerClaudeCode,
		PRURL: "https://example.test/pr/1", TargetSHA: "abc123",
		Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone, CreatedAt: now,
	}
	if err := st.InsertReviewRun(ctx, run); err != nil {
		t.Fatalf("first review run: %v", err)
	}
	run.ID = "run-2"
	err = st.InsertReviewRun(ctx, run)
	if !errors.Is(err, domain.ErrDuplicateReviewRun) {
		t.Fatalf("err = %v, want ErrDuplicateReviewRun", err)
	}
	if errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatal("a review-run collision was misreported as an orchestrator conflict")
	}
}
