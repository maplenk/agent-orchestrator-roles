package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func pauseManager(st *fakeStore) *Manager {
	return New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "incident-fixed" },
	})
}

func pausedWorker(t *testing.T) (*Manager, *fakeStore, domain.SessionID) {
	t.Helper()
	st := newFakeStore()
	ws := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(ws, 0o750)
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	return pauseManager(st), st, id
}

func limitPause() PauseRequest {
	return PauseRequest{
		// Caller-supplied and stable: detection derives it from the envelope so
		// a retry re-derives the same value. See PauseRequest.IncidentID.
		IncidentID:   "incident-1",
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		EvidenceJSON: `{"version":1,"kind":"usage_limit","scope":"account"}`,
	}
}

func TestPauseSession_DurableAndLedgered(t *testing.T) {
	m, st, id := pausedWorker(t)

	rec, err := m.PauseSession(ctx, id, limitPause())
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if rec.Metadata.Pause == nil {
		t.Fatal("returned record is not paused")
	}
	// Durable, not just returned: everything that enforces the pause re-reads
	// the session, so a pin that lives only in the return value fences nothing.
	stored := st.sessions[id]
	if stored.Metadata.Pause == nil {
		t.Fatal("pause was not persisted; the guard re-reads the store and would see a writable session")
	}
	if stored.Metadata.Pause.Harness != domain.HarnessCodex {
		t.Errorf("harness = %q, want the harness that hit the limit", stored.Metadata.Pause.Harness)
	}

	var pauses int
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindPause {
			pauses++
			if e.GenerationID != stored.Metadata.Pause.IncidentID {
				t.Errorf("ledger incident %q != pin incident %q", e.GenerationID, stored.Metadata.Pause.IncidentID)
			}
		}
	}
	if pauses != 1 {
		t.Fatalf("pause ledger rows = %d, want 1", pauses)
	}
}

// The runtime is deliberately left alone. Pause stops AO acting; it does not
// destroy the pane, the process, or the transcript the user may want to read
// before deciding what to do.
func TestPauseSession_DoesNotTouchTheRuntime(t *testing.T) {
	m, st, id := pausedWorker(t)
	before := st.sessions[id]

	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	after := st.sessions[id]

	if after.Metadata.RuntimeHandleID != before.Metadata.RuntimeHandleID {
		t.Error("pause changed the runtime handle")
	}
	if after.Metadata.RuntimeLaunchID != before.Metadata.RuntimeLaunchID {
		t.Error("pause rotated the runtime generation")
	}
	if after.IsTerminated {
		t.Error("pause terminated the session")
	}
}

// A detector that reports the same limit twice must not produce two pauses or
// two ledger rows — structured detectors poll, so repeats are the normal case.
func TestPauseSession_IdempotentForTheSameIncident(t *testing.T) {
	m, st, id := pausedWorker(t)

	first, err := m.PauseSession(ctx, id, limitPause())
	if err != nil {
		t.Fatalf("first pause: %v", err)
	}
	incident := first.Metadata.Pause.IncidentID

	req := limitPause()
	req.IncidentID = incident
	if _, err := m.PauseSession(ctx, id, req); err != nil {
		t.Fatalf("re-pause same incident: %v", err)
	}
	// And a third report of the same incident, which is what a polling
	// detector produces.
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("third report of the same incident: %v", err)
	}

	var pauses int
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindPause {
			pauses++
		}
	}
	if pauses != 1 {
		t.Fatalf("pause ledger rows = %d after three pauses, want 1", pauses)
	}
	if st.sessions[id].Metadata.Pause.IncidentID != incident {
		t.Error("a repeat pause overwrote the original incident")
	}
}

// A genuinely different incident on an already-paused session is a conflict,
// not a silent overwrite: losing the first incident id would break 3B's
// per-incident failover cap, which counts against it.
func TestPauseSession_RefusesADifferentIncident(t *testing.T) {
	m, _, id := pausedWorker(t)
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	req := limitPause()
	req.IncidentID = "a-different-incident"
	_, err := m.PauseSession(ctx, id, req)
	if !errors.Is(err, ErrAlreadyPaused) {
		t.Fatalf("err = %v, want ErrAlreadyPaused", err)
	}
}

// Structured evidence is required for a usage limit at THIS boundary too, not
// only in the store. A caller reaching the manager with a free-text-grade claim
// must be refused before anything durable happens.
func TestPauseSession_RejectsUnstructuredUsageLimit(t *testing.T) {
	m, st, id := pausedWorker(t)

	_, err := m.PauseSession(ctx, id, PauseRequest{
		Reason:     domain.PauseReasonUsageLimit,
		DetectedBy: domain.PauseDetectionOperator,
	})
	if err == nil {
		t.Fatal("accepted a usage_limit pause with no structured envelope")
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Error("a rejected pause was still persisted")
	}
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindPause {
			t.Error("a rejected pause still wrote a ledger row")
		}
	}
}

func TestResumeSession_ClearsAndLedgers(t *testing.T) {
	m, st, id := pausedWorker(t)
	paused, err := m.PauseSession(ctx, id, limitPause())
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	incident := paused.Metadata.Pause.IncidentID

	rec, err := m.ResumeSession(ctx, id, incident)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rec.Metadata.Pause != nil {
		t.Fatal("returned record still paused")
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("pause not cleared durably")
	}

	var resumes int
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindResume {
			resumes++
			if e.GenerationID != incident {
				t.Errorf("resume ledger incident %q != pause incident %q; the pair cannot be correlated", e.GenerationID, incident)
			}
		}
	}
	if resumes != 1 {
		t.Fatalf("resume ledger rows = %d, want 1", resumes)
	}
}

// Resuming restores permission to write, not an obligation to. A send here
// would be the automatic send pause exists to prevent, arriving one step later.
func TestResumeSession_SendsNothing(t *testing.T) {
	st := newFakeStore()
	ws := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(ws, 0o750)
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)

	msg := &fakeMessenger{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: msg, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "incident-fixed" },
	})

	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	before := len(msg.msgs)
	if _, err := m.ResumeSession(ctx, id, "incident-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := len(msg.msgs); got != before {
		t.Fatalf("resume sent %d message(s); manual continue is Phase 3B and must be an explicit act", got-before)
	}
}

func TestResumeSession_RefusesWhenNotPaused(t *testing.T) {
	m, _, id := pausedWorker(t)
	if _, err := m.ResumeSession(ctx, id, "incident-1"); !errors.Is(err, ErrNotPaused) {
		t.Fatalf("err = %v, want ErrNotPaused", err)
	}
}

func TestPauseSession_RefusesTerminated(t *testing.T) {
	m, st, id := pausedWorker(t)
	rec := st.sessions[id]
	rec.IsTerminated = true
	st.sessions[id] = rec

	if _, err := m.PauseSession(ctx, id, limitPause()); !errors.Is(err, ErrTerminated) {
		t.Fatalf("err = %v, want ErrTerminated", err)
	}
}

// A ledger failure must abort the pause rather than pin a session with no
// record of why. A pause nothing recorded is a session a human cannot diagnose.
func TestPauseSession_LedgerFailureLeavesNoPin(t *testing.T) {
	m, st, id := pausedWorker(t)
	// The fake keys injected failures on saga phase, and pause rows carry no
	// phase — so failing "the empty phase" is exactly how a pause append is
	// made to fail.
	st.failLedgerPhase = ""
	st.appendLedgerErr = errors.New("disk full")
	st.ledgerAlwaysErr = true

	if _, err := m.PauseSession(ctx, id, limitPause()); err == nil {
		t.Fatal("pause succeeded despite a failed ledger append")
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("session was pinned as paused with no ledger row explaining it")
	}
}

// RetryAfter is recorded, and that is all. Nothing schedules against it: a
// timer that lifted the pause would be an automatic restart wearing a
// different name.
func TestPauseSession_RetryAfterIsAdvisoryOnly(t *testing.T) {
	m, st, id := pausedWorker(t)
	retry := time.Now().UTC().Add(-time.Hour) // already elapsed

	req := limitPause()
	req.RetryAfter = &retry
	if _, err := m.PauseSession(ctx, id, req); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("an elapsed retryAfter un-paused the session")
	}
	if got := st.sessions[id].Metadata.Pause.RetryAfter; got == nil || !got.Equal(retry) {
		t.Errorf("retryAfter = %v, want it recorded verbatim", got)
	}
}

// The send-confirm re-nudge is the subtle one. It uses Deliver's activity
// policy, so it LOOKS like a user send, but AO alone decides to press Enter
// again — and "zero automatic send" has to cover it. Nothing else pins the
// call site: the guard-level test proves DeliverAuto is fenced, and this proves
// the re-send actually calls it.
//
// The session goes paused after the initial send, exactly as a limit detected
// mid-turn would leave it.
func TestSendConfirm_ReNudgeStopsAtAPause(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{Pause: &domain.SessionPause{
			IncidentID: "inc-1",
			Reason:     domain.PauseReasonUsageLimit,
			DetectedBy: domain.PauseDetectionStructured,
			PausedAt:   time.Now().UTC(),
		}},
	}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	// A user send still goes through — pause does not lock the user out.
	if err := m.Send(context.Background(), "s1", "do the thing"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) == 0 {
		t.Fatal("the user's own message was suppressed; pause fences AO, not the user")
	}
	// ...but every follow-up Enter is AO's initiative and must be refused. The
	// session never goes active, so an unfenced loop would spend its whole
	// budget here.
	if len(msg.msgs) != 1 {
		t.Fatalf("messages = %d, want exactly 1 (the user's); the rest are automatic re-sends into a paused session",
			len(msg.msgs))
	}
}

// Control: the identical setup WITHOUT the pause does re-nudge, so the test
// above is measuring the pause and not some unrelated early exit.
func TestSendConfirm_ReNudgesWhenNotPaused(t *testing.T) {
	st := newFakeStore()
	st.sessions["s1"] = domain.SessionRecord{
		ID: "s1", Harness: "claude-code",
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	msg := &fakeMessenger{}
	m := newSendTestManager(t, signalingAgent{}, msg, st)

	if err := m.Send(context.Background(), "s1", "do the thing"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msg.msgs) < 2 {
		t.Fatalf("messages = %d, want >1: an unpaused stuck session must still be re-nudged", len(msg.msgs))
	}
}

// The orphaned-ledger case. Ledger is written before the pin, so a pin failure
// leaves a pause event with no pause. The fix is that the incident id is the
// CALLER's: a retry re-derives it, the ledger append dedupes on it, and the pin
// is simply written. With a manager-minted id the caller could never learn what
// was recorded, and every retry would open a fresh incident for one real limit.
func TestPauseSession_RetryAfterAFailedPinIsIdempotent(t *testing.T) {
	m, st, id := pausedWorker(t)

	st.pauseWriteErr = errors.New("disk full")
	if _, err := m.PauseSession(ctx, id, limitPause()); err == nil {
		t.Fatal("pause reported success despite a failed pin write")
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("a failed pin write left the session paused")
	}
	// The ledger row IS there — that is the orphan.
	if got := countLedger(st, domain.LifecycleKindPause); got != 1 {
		t.Fatalf("pause ledger rows after the failure = %d, want 1 (the orphan)", got)
	}

	// Retry with the same, caller-derived incident.
	st.pauseWriteErr = nil
	rec, err := m.PauseSession(ctx, id, limitPause())
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if rec.Metadata.Pause == nil {
		t.Fatal("retry did not pin the pause")
	}
	if got := countLedger(st, domain.LifecycleKindPause); got != 1 {
		t.Fatalf("pause ledger rows after the retry = %d, want 1: the orphan was adopted, not duplicated", got)
	}
	if rec.Metadata.Pause.IncidentID != "incident-1" {
		t.Fatalf("incident = %q, want the caller's", rec.Metadata.Pause.IncidentID)
	}
}

func TestPauseSession_RequiresAnIncident(t *testing.T) {
	m, st, id := pausedWorker(t)
	req := limitPause()
	req.IncidentID = "   "
	if _, err := m.PauseSession(ctx, id, req); !errors.Is(err, ErrIncidentRequired) {
		t.Fatalf("err = %v, want ErrIncidentRequired", err)
	}
	if countLedger(st, domain.LifecycleKindPause) != 0 {
		t.Error("a rejected pause still wrote a ledger row")
	}
}

func countLedger(st *fakeStore, kind domain.LifecycleLedgerKind) int {
	var n int
	for _, e := range st.ledger {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// The read-modify-write race, as a test. An unrelated writer holding a snapshot
// taken BEFORE the pause writes it back afterwards; the pin must survive. This
// is the shape of every lifecycle and switch update in the manager.
func TestPauseSurvivesAConcurrentFullRowUpdate(t *testing.T) {
	m, st, id := pausedWorker(t)

	// A writer reads the session...
	stale := st.sessions[id]
	// ...then a limit is detected and pinned...
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// ...and only then does the writer flush its pre-pause snapshot.
	stale.Metadata.Prompt = "an unrelated field the other writer owns"
	if err := st.UpdateSession(ctx, stale); err != nil {
		t.Fatalf("stale update: %v", err)
	}

	after := st.sessions[id]
	if after.Metadata.Pause == nil {
		t.Fatal("a stale full-row write cleared the pause pin; every automatic path is open again")
	}
	if after.Metadata.Prompt != "an unrelated field the other writer owns" {
		t.Error("the unrelated write was lost; pause must not block other writers")
	}
}

// And the mirror, THROUGH THE MANAGER. The earlier version of this test called
// ClearSessionPauseIfIncident directly, which proved the store was safe while
// leaving the manager contract — "resume whatever is currently pinned" —
// untested and unsafe. An operator or UI action raised for incident A can land
// after A was resumed and a fresh limit B pinned the session; resuming B there
// releases a session on evidence nobody looked at.
func TestResumeSession_RefusesAStaleIncident(t *testing.T) {
	m, st, id := pausedWorker(t)
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := m.ResumeSession(ctx, id, "incident-1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	// A new limit pins the session while the stale request is still in flight.
	second := limitPause()
	second.IncidentID = "incident-2"
	if _, err := m.PauseSession(ctx, id, second); err != nil {
		t.Fatalf("second pause: %v", err)
	}

	// The stale request finally arrives, still naming incident-1.
	_, err := m.ResumeSession(ctx, id, "incident-1")
	if !errors.Is(err, ErrIncidentMismatch) {
		t.Fatalf("err = %v, want ErrIncidentMismatch", err)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("a stale resume lifted a newer pause")
	}
	if st.sessions[id].Metadata.Pause.IncidentID != "incident-2" {
		t.Fatalf("holding incident = %q, want incident-2", st.sessions[id].Metadata.Pause.IncidentID)
	}
	// And the append-only ledger must not carry a resume for an incident that
	// was never resumed.
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindResume && e.GenerationID == "incident-2" {
			t.Fatal("a refused resume still wrote a ledger row for incident-2")
		}
	}
	if countLedger(st, domain.LifecycleKindResume) != 1 {
		t.Fatalf("resume ledger rows = %d, want 1 (only the genuine resume)", countLedger(st, domain.LifecycleKindResume))
	}
}

func TestResumeSession_RequiresAnExpectedIncident(t *testing.T) {
	m, st, id := pausedWorker(t)
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := m.ResumeSession(ctx, id, "  "); !errors.Is(err, ErrIncidentRequired) {
		t.Fatalf("err = %v, want ErrIncidentRequired", err)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("an incident-less resume lifted the pause")
	}
	if countLedger(st, domain.LifecycleKindResume) != 0 {
		t.Error("a refused resume wrote a ledger row")
	}
}

// The genuine race: another detector pins the session between this caller's
// read and its compare-and-set. The CAS is what makes that safe — but only if
// the loser then reports what actually happened instead of claiming success or
// inventing a failure. A silent "ok" here would tell a caller its incident
// holds the session when a different one does, which is what 3B's per-incident
// failover cap would then be counted against.
func TestPauseSession_LosingTheCASReportsTheHoldingIncident(t *testing.T) {
	m, st, id := pausedWorker(t)

	st.beforePauseCAS = func() {
		rec := st.sessions[id]
		rec.Metadata.Pause = &domain.SessionPause{
			IncidentID:   "the-other-detector",
			Reason:       domain.PauseReasonUsageLimit,
			DetectedBy:   domain.PauseDetectionStructured,
			EvidenceJSON: `{"version":1,"kind":"usage_limit"}`,
			PausedAt:     time.Now().UTC(),
		}
		st.sessions[id] = rec
	}

	_, err := m.PauseSession(ctx, id, limitPause())
	if !errors.Is(err, ErrAlreadyPaused) {
		t.Fatalf("err = %v, want ErrAlreadyPaused naming the winner", err)
	}
	if st.sessions[id].Metadata.Pause.IncidentID != "the-other-detector" {
		t.Fatal("the losing caller overwrote the winning incident")
	}
}

// And the converged case: the racing writer reported the SAME incident, so
// there is nothing wrong — both callers were right, and this one must not
// surface a spurious conflict to a detector that simply polled twice.
func TestPauseSession_LosingTheCASToTheSameIncidentSucceeds(t *testing.T) {
	m, st, id := pausedWorker(t)

	st.beforePauseCAS = func() {
		rec := st.sessions[id]
		rec.Metadata.Pause = &domain.SessionPause{
			IncidentID:   "incident-1", // the same one this caller is reporting
			Reason:       domain.PauseReasonUsageLimit,
			DetectedBy:   domain.PauseDetectionStructured,
			EvidenceJSON: `{"version":1,"kind":"usage_limit"}`,
			PausedAt:     time.Now().UTC(),
		}
		st.sessions[id] = rec
	}

	rec, err := m.PauseSession(ctx, id, limitPause())
	if err != nil {
		t.Fatalf("a concurrent report of the SAME incident was surfaced as a conflict: %v", err)
	}
	if rec.Metadata.Pause == nil || rec.Metadata.Pause.IncidentID != "incident-1" {
		t.Fatalf("returned record = %+v, want the converged pause", rec.Metadata.Pause)
	}
}

// The ledger is append-only, so a resume event for an incident that was never
// resumed is a permanent false entry in the audit trail. The mismatch must
// therefore be caught BEFORE the append, not merely before the pin write.
//
// The expected incident here has no prior ledger row of its own — with one, the
// append's own idempotency would dedupe the false entry away and hide the
// ordering bug entirely.
func TestResumeSession_MismatchWritesNoLedgerRow(t *testing.T) {
	m, st, id := pausedWorker(t)
	if _, err := m.PauseSession(ctx, id, limitPause()); err != nil {
		t.Fatalf("pause: %v", err)
	}

	_, err := m.ResumeSession(ctx, id, "an-incident-that-never-held-this-session")
	if !errors.Is(err, ErrIncidentMismatch) {
		t.Fatalf("err = %v, want ErrIncidentMismatch", err)
	}
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindResume {
			t.Fatalf("a refused resume appended %q to the append-only ledger; "+
				"the mismatch must be caught before the ledger write", e.ID)
		}
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("the pause was lifted by a mismatched resume")
	}
}
