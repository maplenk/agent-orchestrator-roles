package store_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func failoverAttempt(sess domain.SessionID, incident string, seq int, gen string) domain.FailoverAttempt {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	return domain.FailoverAttempt{
		ID:                 string(sess) + ":" + incident + ":" + strconv.Itoa(seq),
		SessionID:          sess,
		ProjectID:          "mer",
		IncidentID:         incident,
		Seq:                seq,
		RoleID:             "implementor",
		FromHarness:        domain.HarnessClaudeCode,
		FromModel:          "opus",
		ToHarness:          domain.HarnessCodex,
		ToModel:            "",
		RungIndex:          0,
		GenerationID:       gen,
		SourceGenerationID: "source-gen-1",
		State:              domain.FailoverAttemptRequested,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func failoverLedger(sess domain.SessionID, incident, id string) domain.LifecycleLedgerRecord {
	return domain.LifecycleLedgerRecord{
		ID: id, SessionID: sess, ProjectID: "mer",
		Kind: domain.LifecycleKindFailover, Phase: domain.LifecyclePhaseRequested,
		GenerationID: "gen-1",
		FromHarness:  domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RoleID:    "implementor",
		CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
	}
}

func newFailoverSession(t *testing.T, s *sqlite.Store) domain.SessionID {
	t.Helper()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(context.Background(), sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess.ID
}

func TestFailoverAttempt_AppendWithLedgerWritesBothRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)

	att := failoverAttempt(id, "inc-1", 1, "gen-1")
	if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, failoverLedger(id, "inc-1", "led-1")); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.ListSessionFailoverAttemptsByIncident(ctx, id, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("attempts = %d, want 1", len(got))
	}
	if got[0].GenerationID != "gen-1" {
		t.Fatalf("generation = %q, want gen-1", got[0].GenerationID)
	}
	if got[0].SourceGenerationID != "source-gen-1" {
		t.Fatalf("source generation = %q, want source-gen-1", got[0].SourceGenerationID)
	}
	if got[0].State != domain.FailoverAttemptRequested {
		t.Fatalf("state = %q, want requested", got[0].State)
	}
	if got[0].RungIndex != 0 || got[0].RoleID != "implementor" {
		t.Fatalf("attempt round-trip lost fields: %+v", got[0])
	}

	events, err := s.ListLifecycleLedger(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "led-1" || events[0].Kind != domain.LifecycleKindFailover {
		t.Fatalf("ledger = %+v, want one failover row", events)
	}
}

// Contract section 6 rule 1: the ledger row and the attempt row are ONE
// transaction, so a failure of the second insert must leave NEITHER behind.
//
// The failure is forced the way it would actually happen in production rather
// than by injection: a retry that recomputed the same seq collides with
// UNIQUE(session_id, incident_id, seq). Under the pre-amendment two-write
// shape that collision would have left the second ledger row committed --
// an incident that looks continued twice while only one rung was spent.
func TestFailoverAttempt_LedgerRolledBackWhenAttemptInsertFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)

	att := failoverAttempt(id, "inc-1", 1, "gen-1")
	if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, failoverLedger(id, "inc-1", "led-1")); err != nil {
		t.Fatalf("first append: %v", err)
	}

	// Same seq, DIFFERENT ledger id: the ledger insert would succeed on its own,
	// so anything left behind is the transaction failing to roll back rather
	// than a second constraint quietly saving us.
	dup := failoverAttempt(id, "inc-1", 1, "gen-2")
	err := s.AppendSessionFailoverAttemptWithLedger(ctx, dup, failoverLedger(id, "inc-1", "led-2"))
	if err == nil {
		t.Fatal("duplicate seq: expected an error")
	}

	attempts, listErr := s.ListSessionFailoverAttemptsByIncident(ctx, id, "inc-1")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (the rollback left a second row)", len(attempts))
	}
	if attempts[0].GenerationID != "gen-1" {
		t.Fatalf("surviving attempt generation = %q, want gen-1", attempts[0].GenerationID)
	}

	events, evErr := s.ListLifecycleLedger(ctx, id)
	if evErr != nil {
		t.Fatal(evErr)
	}
	if len(events) != 1 {
		t.Fatalf("ledger rows = %d, want 1: the rolled-back attempt left an orphan ledger row %+v", len(events), events)
	}
	if events[0].ID != "led-1" {
		t.Fatalf("surviving ledger row = %q, want led-1", events[0].ID)
	}
}

func TestFailoverAttempt_ConcurrentSameIncidentSequenceHasOneAtomicWinner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)
	start := make(chan struct{})
	errs := make(chan error, 2)

	for i := 1; i <= 2; i++ {
		go func() {
			<-start
			att := failoverAttempt(id, "inc-1", 1, "gen-"+strconv.Itoa(i))
			ledger := failoverLedger(id, "inc-1", "led-"+strconv.Itoa(i))
			ledger.GenerationID = att.GenerationID
			errs <- s.AppendSessionFailoverAttemptWithLedger(
				ctx, att, ledger,
			)
		}()
	}
	close(start)

	succeeded := 0
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful concurrent appends = %d, want exactly 1", succeeded)
	}

	attempts, err := s.ListSessionFailoverAttemptsByIncident(ctx, id, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want one winner", len(attempts))
	}
	events, err := s.ListLifecycleLedger(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("ledger rows = %d, want one row from the same atomic winner", len(events))
	}
	if events[0].GenerationID != attempts[0].GenerationID {
		t.Fatalf("ledger generation %q != attempt generation %q", events[0].GenerationID, attempts[0].GenerationID)
	}
}

func TestFailoverAttempt_UpdateStateIsCompareAndSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)
	now := time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC)

	att := failoverAttempt(id, "inc-1", 1, "gen-1")
	if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, failoverLedger(id, "inc-1", "led-1")); err != nil {
		t.Fatalf("append: %v", err)
	}

	// requested -> post_stop, the state the CHECK constraint had to learn.
	ok, err := s.UpdateSessionFailoverAttemptState(ctx, att.ID,
		domain.FailoverAttemptRequested, domain.FailoverAttemptPostStop, now)
	if err != nil {
		t.Fatalf("to post_stop: %v", err)
	}
	if !ok {
		t.Fatal("to post_stop: ok=false, want the CAS to win")
	}

	// The loser of a post_stop race: boot recovery and an operator Continue both
	// try to complete it, and the second must be told no rather than error.
	ok, err = s.UpdateSessionFailoverAttemptState(ctx, att.ID,
		domain.FailoverAttemptRequested, domain.FailoverAttemptFailed, now)
	if err != nil {
		t.Fatalf("stale CAS returned an error, want ok=false: %v", err)
	}
	if ok {
		t.Fatal("stale CAS on `requested` succeeded; post_stop was overwritten")
	}

	ok, err = s.UpdateSessionFailoverAttemptState(ctx, att.ID,
		domain.FailoverAttemptPostStop, domain.FailoverAttemptAcked, now)
	if err != nil || !ok {
		t.Fatalf("post_stop -> acked: ok=%v err=%v", ok, err)
	}

	got, err := s.ListSessionFailoverAttemptsByIncident(ctx, id, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].State != domain.FailoverAttemptAcked {
		t.Fatalf("state = %q, want acked", got[0].State)
	}
	// Section 6b: nothing may rewrite the generation.
	if got[0].GenerationID != "gen-1" {
		t.Fatalf("generation = %q, want gen-1 unchanged across state moves", got[0].GenerationID)
	}
}

// The SQLite CHECK must accept exactly domain.FailoverAttemptState.Valid()'s
// set. A state Go accepts but SQLite rejects is a feature that cannot run --
// and post_stop is the one the pre-amendment CHECK was missing.
func TestFailoverAttempt_CheckConstraintMatchesDomainStates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)

	for i, st := range []domain.FailoverAttemptState{
		domain.FailoverAttemptRequested,
		domain.FailoverAttemptPostStop,
		domain.FailoverAttemptAcked,
		domain.FailoverAttemptFailed,
	} {
		if !st.Valid() {
			t.Fatalf("%q is not Valid() in domain", st)
		}
		att := failoverAttempt(id, "inc-states", i+1, "gen-"+strconv.Itoa(i))
		att.State = st
		led := failoverLedger(id, "inc-states", "led-state-"+strconv.Itoa(i))
		if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, led); err != nil {
			t.Fatalf("state %q rejected by storage: %v", st, err)
		}
	}
}

// Section 6b again, at the storage boundary: an empty generation is the shape
// that made crash recovery a guess, so it is refused rather than stored.
func TestFailoverAttempt_RejectsEmptyGeneration(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)

	att := failoverAttempt(id, "inc-1", 1, "")
	if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, failoverLedger(id, "inc-1", "led-1")); err == nil {
		t.Fatal("empty generation_id was accepted")
	}
	events, err := s.ListLifecycleLedger(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("refused attempt still wrote %d ledger rows", len(events))
	}
}

func TestFailoverAttempt_ListBySessionSpansIncidents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	id := newFailoverSession(t, s)

	for i, inc := range []string{"inc-1", "inc-2"} {
		att := failoverAttempt(id, inc, 1, "gen-"+strconv.Itoa(i))
		led := failoverLedger(id, inc, "led-"+strconv.Itoa(i))
		if err := s.AppendSessionFailoverAttemptWithLedger(ctx, att, led); err != nil {
			t.Fatalf("append %s: %v", inc, err)
		}
	}

	all, err := s.ListSessionFailoverAttemptsBySession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("attempts = %d, want 2 across incidents", len(all))
	}
	one, err := s.ListSessionFailoverAttemptsByIncident(ctx, id, "inc-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].IncidentID != "inc-2" {
		t.Fatalf("per-incident read leaked other incidents: %+v", one)
	}
}
