package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Against the REAL store, not the manager's in-memory fake. Migration 0049 adds
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
	pause := &domain.SessionPause{
		IncidentID:   "incident-7",
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		Harness:      domain.HarnessCodex,
		EvidenceJSON: `{"kind":"usage_limit","resetsAt":"2026-08-06T18:00:00Z"}`,
		RetryAfter:   &retry,
		PausedAt:     time.Now().UTC().Truncate(time.Second),
	}
	rec.Metadata.Pause = pause
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("persist pause: %v", err)
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

	// Clearing is a plain update; it must actually clear.
	got.Metadata.Pause = nil
	if err := s.UpdateSession(ctx, got); err != nil {
		t.Fatalf("clear pause: %v", err)
	}
	after, _, err := s.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("read after clear: %v", err)
	}
	if after.Metadata.Pause != nil {
		t.Fatal("pause survived an explicit clear")
	}
}

// A session created already paused must persist the pin on INSERT, not only on
// UPDATE. The insert and update parameter lists are built by separate
// functions, so covering one proves nothing about the other.
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
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: "hand-rolled",
		Reason:     domain.PauseReasonUsageLimit,
		DetectedBy: domain.PauseDetectionOperator, // not a structured envelope
		PausedAt:   time.Now().UTC(),
	}
	if err := s.UpdateSession(ctx, rec); err == nil {
		t.Fatal("the store accepted a usage_limit pause with no structured evidence")
	}
}
