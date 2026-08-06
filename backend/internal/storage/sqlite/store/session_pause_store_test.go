package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func storePause(incident string) *domain.SessionPause {
	return &domain.SessionPause{
		IncidentID:   incident,
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		Harness:      domain.HarnessCodex,
		EvidenceJSON: `{"version":1,"kind":"usage_limit","sourceKey":"win-1","resetsAt":"2026-08-06T18:00:00Z"}`,
		PausedAt:     time.Now().UTC().Truncate(time.Second),
	}
}

// Against the REAL store, not the manager's in-memory fake. Migration 0060 adds
// a column, and a column the queries do not carry reads back empty — which for
// this pin means "not paused", i.e. the feature silently does nothing while
// every unit test passes. That is precisely how 2B-1 shipped broken.
func TestSessionPausePersistsThroughRealStore(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if rec.Metadata.Pause != nil {
		t.Fatal("a freshly created session is paused")
	}

	retry := time.Now().UTC().Add(90 * time.Minute).Truncate(time.Second)
	pause := storePause("incident-7")
	pause.RetryAfter = &retry
	ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, pause, domain.PauseGuard{}, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("persist pause: ok=%v err=%v", ok, err)
	}

	got, ok, err := s.GetSession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if got.Metadata.Pause == nil {
		t.Fatal("pause did not survive the round trip: the session reads as writable, so every automatic path is open again")
	}
	if got.Metadata.Pause.IncidentID != "incident-7" {
		t.Errorf("incident = %q, want incident-7", got.Metadata.Pause.IncidentID)
	}
	if got.Metadata.Pause.EvidenceJSON != pause.EvidenceJSON {
		t.Errorf("evidence lost: %q", got.Metadata.Pause.EvidenceJSON)
	}
	if got.Metadata.Pause.RetryAfter == nil || !got.Metadata.Pause.RetryAfter.Equal(retry) {
		t.Errorf("retryAfter = %v, want %v", got.Metadata.Pause.RetryAfter, retry)
	}

	// And it must come back through the LIST paths too — reconcile and restore
	// read sessions that way, so a pin visible only to GetSession would be
	// invisible to exactly the boot paths that must not act on a paused session.
	listed, err := s.ListSessions(ctx, "mer")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found bool
	for _, l := range listed {
		if l.ID != rec.ID {
			continue
		}
		found = true
		if l.Metadata.Pause == nil {
			t.Error("ListSessions dropped the pause pin")
		}
	}
	if !found {
		t.Fatal("session missing from project listing")
	}
}

// THE race, against the real store. Every other writer in the manager does a
// read-modify-write of the whole row: read a record, mutate one field, call
// UpdateSession. If that statement carries pause_json, a writer whose snapshot
// predates the pause silently clears it — and a cleared pin re-opens every
// automatic send and every boot relaunch, with nothing in the ledger to say a
// pause ever ended.
func TestGenericUpdatePreservesThePausePin(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// An unrelated writer reads the row BEFORE the pause exists.
	stale, _, err := s.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}

	if ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("incident-7"), domain.PauseGuard{}, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("pause: ok=%v err=%v", ok, err)
	}

	// Now the stale writer flushes. Its record still has Pause == nil.
	stale.Metadata.Prompt = "a field that writer legitimately owns"
	stale.UpdatedAt = time.Now().UTC()
	if err := s.UpdateSession(ctx, stale); err != nil {
		t.Fatalf("stale update: %v", err)
	}

	after, _, err := s.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if after.Metadata.Pause == nil {
		t.Fatal("a stale full-row update cleared the pause pin")
	}
	if after.Metadata.Prompt != "a field that writer legitimately owns" {
		t.Error("the unrelated write was lost; column ownership must not block other writers")
	}
}

// Compare-and-set, not last-write-wins: a second detector reporting a different
// incident must not take the pin from the first. Losing the original incident
// id would break 3B's per-incident failover cap, which counts against it.
func TestSetPauseIfAbsentIsCompareAndSet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("first"), domain.PauseGuard{}, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("first pause: ok=%v err=%v", ok, err)
	}
	ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("second"), domain.PauseGuard{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("second pause: %v", err)
	}
	if ok {
		t.Fatal("the second incident overwrote the first")
	}
	got, _, _ := s.GetSession(ctx, rec.ID)
	if got.Metadata.Pause.IncidentID != "first" {
		t.Fatalf("holding incident = %q, want first", got.Metadata.Pause.IncidentID)
	}
}

// A terminated session cannot be pinned: there is no runtime to protect, and a
// pin there would only obstruct cleanup.
func TestSetPauseIfAbsentRefusesTerminated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("inc"), domain.PauseGuard{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("set pause: %v", err)
	}
	if ok {
		t.Fatal("pinned a terminated session")
	}
}

// Resume is conditional on the incident, so a caller acting on a stale read
// cannot lift a newer pause it never saw.
func TestClearPauseIfIncident(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("current"), domain.PauseGuard{}, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("pause: ok=%v err=%v", ok, err)
	}

	ok, err := s.ClearSessionPauseIfIncident(ctx, rec.ID, "stale", time.Now().UTC())
	if err != nil {
		t.Fatalf("clear stale: %v", err)
	}
	if ok {
		t.Fatal("a resume naming a stale incident lifted the current pause")
	}
	if got, _, _ := s.GetSession(ctx, rec.ID); got.Metadata.Pause == nil {
		t.Fatal("the current pause was cleared by a stale resume")
	}

	ok, err = s.ClearSessionPauseIfIncident(ctx, rec.ID, "current", time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("clear current: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if got.Metadata.Pause != nil {
		t.Fatal("pause survived its own resume")
	}
}

// A session created already paused must persist the pin on INSERT, not only
// through the column-owned write. The insert parameter list is built by a
// separate function, so covering one proves nothing about the other.
func TestSessionPausePersistsOnInsert(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	rec := sampleRecord("mer")
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: "born-paused",
		Reason:     domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator,
		PausedAt:   time.Now().UTC().Truncate(time.Second),
	}
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("read back: ok=%v err=%v", ok, err)
	}
	if got.Metadata.Pause == nil || got.Metadata.Pause.IncidentID != "born-paused" {
		t.Fatalf("insert dropped the pause pin: %+v", got.Metadata.Pause)
	}
}

// The storage boundary refuses invalid pause state rather than writing it.
// Without this, an unvalidated pin could be persisted and then fail every
// subsequent read — locking the session with no path to resume, because resume
// has to read the row first.
func TestSessionPauseRejectsUnstructuredUsageLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, tc := range []struct {
		name  string
		pause *domain.SessionPause
	}{
		{"operator evidence for a usage limit", &domain.SessionPause{
			IncidentID: "a", Reason: domain.PauseReasonUsageLimit,
			DetectedBy: domain.PauseDetectionOperator, PausedAt: time.Now().UTC(),
		}},
		{"prose as the envelope", &domain.SessionPause{
			IncidentID: "b", Reason: domain.PauseReasonUsageLimit,
			DetectedBy: domain.PauseDetectionStructured, EvidenceJSON: "I hit a limit",
			PausedAt: time.Now().UTC(),
		}},
		{"unversioned envelope", &domain.SessionPause{
			IncidentID: "c", Reason: domain.PauseReasonUsageLimit,
			DetectedBy: domain.PauseDetectionStructured, EvidenceJSON: `{"kind":"usage_limit"}`,
			PausedAt: time.Now().UTC(),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, tc.pause, domain.PauseGuard{}, time.Now().UTC()); err == nil {
				t.Fatal("the store accepted a usage_limit pause with no structured envelope")
			}
			if got, _, _ := s.GetSession(ctx, rec.ID); got.Metadata.Pause != nil {
				t.Fatalf("a rejected pause was persisted: %+v", got.Metadata.Pause)
			}
		})
	}
}

// The ownership conditions are in the STATEMENT, not read-then-checked in Go.
// That is the whole point: a switch or relaunch landing between a check and the
// write cannot slip through, because there is no gap to land in.
func TestSetPauseIfAbsentEnforcesOwnershipAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	rec.Harness = domain.HarnessCodex
	rec.Metadata.RuntimeLaunchID = "gen-1"
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	for _, tc := range []struct {
		name  string
		guard domain.PauseGuard
		want  bool
	}{
		{"matching owner", domain.PauseGuard{
			ExpectHarness: domain.HarnessCodex, ExpectRuntimeLaunchID: "gen-1", RequireNoSwitchPending: true}, true},
		{"wrong harness", domain.PauseGuard{
			ExpectHarness: domain.HarnessClaudeCode, ExpectRuntimeLaunchID: "gen-1"}, false},
		{"stale generation", domain.PauseGuard{
			ExpectHarness: domain.HarnessCodex, ExpectRuntimeLaunchID: "gen-0"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case starts unpaused.
			if _, err := s.ClearSessionPauseIfIncident(ctx, rec.ID, "own-test", time.Now().UTC()); err != nil {
				t.Fatalf("reset: %v", err)
			}
			ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("own-test"), tc.guard, time.Now().UTC())
			if err != nil {
				t.Fatalf("set: %v", err)
			}
			if ok != tc.want {
				t.Fatalf("ok = %v, want %v", ok, tc.want)
			}
		})
	}

	// And a pending switch refuses, which is the ownership-in-transfer case.
	if _, err := s.ClearSessionPauseIfIncident(ctx, rec.ID, "own-test", time.Now().UTC()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	cur, _, _ := s.GetSession(ctx, rec.ID)
	cur.Metadata.SwitchPending = &domain.SwitchPending{GenerationID: "sw-1", ToHarness: domain.HarnessClaudeCode}
	if err := s.UpdateSession(ctx, cur); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	ok, err := s.SetSessionPauseIfAbsent(ctx, rec.ID, storePause("own-test"), domain.PauseGuard{
		ExpectHarness: domain.HarnessCodex, ExpectRuntimeLaunchID: "gen-1", RequireNoSwitchPending: true,
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("set with pending: %v", err)
	}
	if ok {
		t.Fatal("pinned a session a switch saga owns")
	}
}
