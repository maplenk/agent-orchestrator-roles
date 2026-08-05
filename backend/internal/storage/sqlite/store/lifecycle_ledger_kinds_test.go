package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The ledger's kind column carries a SQLite CHECK constraint, and the session
// manager's in-memory fake does not. That gap already shipped a broken feature
// once: 2B-1 added LifecycleKindOrchestratorFresh in Go, every manager test
// passed, and the first real insert failed with "CHECK constraint failed" —
// before fleet observation, before the source stopped.
//
// So every kind domain.LifecycleLedgerKind.Valid() accepts must be provable
// against the REAL store. A kind that Go admits and SQLite rejects is not a
// validation mismatch, it is a feature that cannot run.
func TestLifecycleLedgerAcceptsEveryValidKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	kinds := []domain.LifecycleLedgerKind{
		domain.LifecycleKindSwitch,
		domain.LifecycleKindPause,
		domain.LifecycleKindResume,
		domain.LifecycleKindFailover,
		domain.LifecycleKindFreshConversation,
		domain.LifecycleKindOrchestratorFresh,
	}
	for i, kind := range kinds {
		if !kind.Valid() {
			t.Fatalf("%q is not accepted by the domain type; fix this list", kind)
		}
		err := s.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
			ID:        string(rec.ID) + ":" + string(kind),
			SessionID: rec.ID,
			ProjectID: rec.ProjectID,
			Kind:      kind,
			Phase:     domain.LifecyclePhaseRequested,
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Errorf("kind %q is valid in Go but REJECTED by the store: %v\n"+
				"the ledger CHECK constraint and domain.LifecycleLedgerKind have diverged, "+
				"so any saga using this kind fails at its first append", kind, err)
		}
	}

	events, err := s.ListLifecycleLedger(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list ledger: %v", err)
	}
	if len(events) != len(kinds) {
		t.Fatalf("stored %d events, want %d", len(events), len(kinds))
	}
}

// TestLifecycleLedgerRejectsUnknownKind is the negative control: the constraint
// must still be doing something, or the test above passes vacuously.
func TestLifecycleLedgerRejectsUnknownKind(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	err = s.AppendLifecycleLedger(ctx, domain.LifecycleLedgerRecord{
		ID:        "bogus",
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
		Kind:      domain.LifecycleLedgerKind("teleport"),
		Phase:     domain.LifecyclePhaseRequested,
		CreatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("the store accepted an unknown lifecycle kind; the CHECK constraint is gone")
	}
}
