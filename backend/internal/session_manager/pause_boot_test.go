package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Boot is where "zero automatic send/restart" is won or lost. sessionguard
// stops AO writing INTO a session; it says nothing about AO starting the agent
// back up. A pause that survives only until the next daemon restart is not a
// pause, it is a quiet period — and a usage limit outlives a restart easily.
//
// Three distinct paths reach a relaunch, and none of them goes through the
// guard: post-stop switch recovery, the live pass (whose save-and-teardown
// mints the very marker RestoreAll consumes, so it relaunches in the SAME
// boot), and RestoreAll itself on a session paused before a clean shutdown.

func pausePin(incident string) *domain.SessionPause {
	return &domain.SessionPause{
		IncidentID:   incident,
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		EvidenceJSON: `{"version":1,"kind":"usage_limit"}`,
		PausedAt:     time.Now().UTC(),
	}
}

func savedSession(id domain.SessionID, kind domain.SessionKind, branch string, pause *domain.SessionPause) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: kind, Harness: domain.HarnessClaudeCode,
		IsTerminated: true,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/" + string(id), Branch: branch,
			AgentSessionID: "agent-" + string(id), Pause: pause,
		},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
}

func marker(id domain.SessionID) []domain.SessionWorktreeRecord {
	return []domain.SessionWorktreeRecord{{SessionID: id, RepoName: "__root__", PreservedRef: "", State: "removed"}}
}

// A paused WORKER saved by a clean shutdown must not be relaunched at boot.
func TestRestoreAll_SkipsPausedWorker(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-1"] = savedSession("mer-1", domain.KindWorker, "ao/mer-1/root", pausePin("inc-1"))
	st.worktrees["mer-1"] = marker("mer-1")

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime.Create called %d times; boot relaunched a paused worker", rt.created)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("paused worker was brought live")
	}
	// The marker must SURVIVE: it is what makes the session restorable once a
	// human resumes it. Consuming or neutralizing it would make the pause
	// permanent.
	if len(st.worktrees["mer-1"]) == 0 {
		t.Error("the restore marker was destroyed; the session can never be restored after a resume")
	}
}

// Control: the same session unpaused IS restored, so the test above is
// measuring the pause and not a broken fixture.
func TestRestoreAll_RestoresTheSameWorkerWhenNotPaused(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-1"] = savedSession("mer-1", domain.KindWorker, "ao/mer-1/root", nil)
	st.worktrees["mer-1"] = marker("mer-1")

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create called %d times, want 1", rt.created)
	}
}

// A paused ORCHESTRATOR must not win the survivor election — and, critically,
// must not be treated as a LOSER either: losers have their markers neutralized,
// which would permanently destroy the restorability a later resume needs.
func TestRestoreAll_SkipsPausedOrchestratorWithoutNeutralizingIt(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-2"] = savedSession("mer-2", domain.KindOrchestrator, "ao/mer-orchestrator", pausePin("inc-1"))
	st.worktrees["mer-2"] = marker("mer-2")

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime.Create called %d times; boot relaunched a paused orchestrator", rt.created)
	}
	if !st.sessions["mer-2"].IsTerminated {
		t.Error("paused orchestrator was brought live")
	}
	if len(st.worktrees["mer-2"]) == 0 {
		t.Error("the paused orchestrator's marker was neutralized; it can never be restored")
	}
}

// With one paused and one live candidate, the election must run normally over
// the remaining candidate and leave the paused one entirely alone.
func TestRestoreAll_PausedOrchestratorDoesNotBlockAnEligibleOne(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	now := time.Now().UTC()

	paused := savedSession("mer-2", domain.KindOrchestrator, "ao/mer-orchestrator", pausePin("inc-1"))
	paused.CreatedAt = now // newest, so it would win the election if it took part
	st.sessions["mer-2"] = paused
	st.worktrees["mer-2"] = marker("mer-2")

	eligible := savedSession("mer-3", domain.KindOrchestrator, "ao/mer-orchestrator", nil)
	eligible.CreatedAt = now.Add(-time.Hour)
	st.sessions["mer-3"] = eligible
	st.worktrees["mer-3"] = marker("mer-3")

	if err := m.RestoreAll(ctx); err != nil {
		t.Fatalf("RestoreAll err = %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create called %d times, want exactly 1 (the eligible orchestrator)", rt.created)
	}
	if st.sessions["mer-3"].IsTerminated {
		t.Error("the eligible orchestrator was not restored")
	}
	if !st.sessions["mer-2"].IsTerminated {
		t.Error("the paused orchestrator was restored")
	}
	if len(st.worktrees["mer-2"]) == 0 {
		t.Error("the paused orchestrator's marker was neutralized by an election it never entered")
	}
}

// The same-boot path: reconcileLive finds a dead runtime, tears the session
// down WITH a marker, and RestoreAll relaunches from that marker moments later.
// Nothing here is a "restore" from the user's point of view — the session was
// live when the daemon started — which is exactly why it is easy to miss.
func TestReconcile_DoesNotTearDownAndRelaunchAPausedSession(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1/root",
			RuntimeHandleID: "tmux-dead", AgentSessionID: "agent-w",
			Pause: pausePin("inc-1"),
		},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	// aliveByHandle has no entry, so the probe reports the agent dead.
	_ = rt

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile err = %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime.Create called %d times; boot restarted a paused session", rt.created)
	}
	if st.sessions["mer-1"].Metadata.Pause == nil {
		t.Error("reconcile cleared the pause pin")
	}
	// No marker was minted, so there is nothing for a later boot to restore
	// from either.
	if len(st.worktrees["mer-1"]) != 0 {
		t.Errorf("a shutdown-saved marker was written for a paused session: %+v", st.worktrees["mer-1"])
	}
}

// Post-stop switch recovery LAUNCHES the target. On a paused session that is an
// automatic restart, so the incomplete saga waits for an explicit resume. The
// handoff is durable, so nothing is lost by waiting.
//
// Built on the same harness as the recovery tests in launch_adoption_test.go —
// a fixture that does not genuinely reach a relaunch would make this pass
// whether the guard existed or not.
func TestReconcile_SkipsPostStopRecoveryForAPausedSession(t *testing.T) {
	st, m := postStopRecoveryHarness(t)
	rec := st.sessions["mer-1"]
	rec.Metadata.Pause = pausePin("inc-1")
	st.sessions["mer-1"] = rec

	// That harness is rigged so a relaunch leaves an unresolved runtime and
	// Reconcile returns ErrLaunchCleanupUnresolved. Paused, recovery must never
	// run, so boot comes back clean.
	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile = %v; recovery relaunched a paused session and left a runtime behind", err)
	}
	// The saga is left intact so an explicit resume can still complete it.
	if st.sessions["mer-1"].Metadata.SwitchPending == nil {
		t.Error("the pending switch was discarded; it can no longer be recovered after a resume")
	}
	if st.sessions["mer-1"].Metadata.Pause == nil {
		t.Error("reconcile cleared the pause pin")
	}
	for _, e := range st.ledger {
		if e.Phase == domain.LifecyclePhaseTargetAck {
			t.Fatal("recovery acked a target for a paused session")
		}
	}
}

// Control: the identical harness WITHOUT the pause does drive recovery, which
// is what makes the assertion above meaningful.
func TestReconcile_RecoversPostStopWhenNotPaused(t *testing.T) {
	_, m := postStopRecoveryHarness(t)
	if err := m.Reconcile(ctx); err == nil {
		t.Fatal("the unpaused harness did not reach a relaunch; the paused test above proves nothing")
	}
}

// Skipping the teardown must not also skip the OBSERVATION. Boot proved the
// process is gone; if the pre-crash activity survives, the row serializes as
// paused AND working and the UI cannot know to offer "Restart agent" instead of
// "Resume" (PHASE3A_PAUSE_CONTRACT §2). The earlier version of this code
// asserted "it stays active with a dead agent, the ordinary exited state" in a
// comment without ever recording it.
func TestReconcile_RecordsConfirmedExitForAPausedSession(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1/root",
			RuntimeHandleID: "tmux-dead", AgentSessionID: "agent-w",
			Pause: pausePin("inc-1"),
		},
		// Working when the daemon died — the state that would otherwise persist.
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile err = %v", err)
	}
	got := st.sessions["mer-1"]
	if got.Activity.State != domain.ActivityExited {
		t.Fatalf("activity = %q, want exited: the read model would report paused AND working, "+
			"so the UI could not tell it needs a restart", got.Activity.State)
	}
	// And it is an observation, NOT a teardown: nothing may have been relaunched,
	// terminated, or made restore-eligible.
	if rt.created != 0 {
		t.Errorf("runtime.Create called %d times", rt.created)
	}
	if got.IsTerminated {
		t.Error("recording the exit terminated the session")
	}
	if got.Metadata.Pause == nil {
		t.Error("recording the exit cleared the pause pin")
	}
	if len(st.worktrees["mer-1"]) != 0 {
		t.Errorf("a restore marker was written: %+v", st.worktrees["mer-1"])
	}
}

// A paused session whose runtime is ALIVE keeps its activity untouched — the
// exit must be recorded only when boot actually proved death.
func TestReconcile_LeavesALivePausedSessionAlone(t *testing.T) {
	m, st, rt, _ := newLifecycleManager()
	rt.aliveByHandle = map[string]bool{"tmux-live": true}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1/root",
			RuntimeHandleID: "tmux-live", AgentSessionID: "agent-w",
			Pause: pausePin("inc-1"),
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile err = %v", err)
	}
	if got := st.sessions["mer-1"].Activity.State; got != domain.ActivityActive {
		t.Fatalf("activity = %q, want active: a live paused agent was reported dead", got)
	}
}

// Recording the exit can FAIL, and that failure is boot-fatal.
//
// The surviving state is actively misleading rather than merely incomplete:
// boot proved the process is gone, but the row still says working. Serving that
// shows a human "Resume" for a session that actually needs restarting. Two
// things have to hold for this to work — the error must be an ErrBootUnsafe
// child, AND the live pass must carry it out of Reconcile, which it previously
// could not because that loop logged everything it caught.
func TestReconcile_FailedExitObservationIsBootFatal(t *testing.T) {
	m, st, _, _ := newLifecycleManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1/root",
			RuntimeHandleID: "tmux-dead", AgentSessionID: "agent-w",
			Pause: pausePin("inc-1"),
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.updateFailAfter = 1
	st.updateErr = errors.New("database is locked")

	err := m.Reconcile(ctx)
	if !errors.Is(err, ErrPausedLivenessUnresolved) {
		t.Fatalf("Reconcile = %v, want ErrPausedLivenessUnresolved", err)
	}
	// The daemon gates on the PARENT, so the child must satisfy it or boot
	// serves regardless of how specific the child is.
	if !errors.Is(err, ErrBootUnsafe) {
		t.Fatal("the failure is not an ErrBootUnsafe child; daemon.Run would log it and serve")
	}
	// And the misleading state is exactly what it refuses to serve.
	if got := st.sessions["mer-1"]; got.Activity.State != domain.ActivityActive || got.Metadata.Pause == nil {
		t.Fatalf("fixture drifted: activity=%q paused=%v; the point is that the row still reads paused+working",
			got.Activity.State, got.Metadata.Pause != nil)
	}
}

// Switch errors carry their CAUSE now (%w rather than %v), which makes the
// inner error matchable by errors.Is. That is the point — but it is also the
// risk, because Reconcile and the daemon route on error identity. A cause that
// newly satisfied ErrBootUnsafe would silently turn a logged switch failure
// into a refusal to boot.
//
// Driven through the PRODUCTION wrapper — a real SwitchWorker whose target_ack
// ledger write fails — rather than a hand-built error, so the assertion is
// about the code that ships and not about a string this test wrote itself.
func TestSwitchErrorsCarryTheirCauseWithoutBecomingBootUnsafe(t *testing.T) {
	st := newFakeStore()
	ws := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(ws, 0o750)
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	cause := errors.New("database is locked")
	st.failLedgerPhase = domain.LifecyclePhaseTargetAck
	st.appendLedgerErr = cause

	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "gen-1" },
	})
	m.switchCapsOverride = testSwitchCaps

	_, err := m.SwitchWorker(ctx, SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
		Semantic: domain.SemanticHandoffV1{Objective: "keep going"},
	})
	if err == nil {
		t.Fatal("switch succeeded despite a failing target_ack ledger write; this test proves nothing")
	}
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v; the classification sentinel no longer matches, and toAPIError and recovery both route on it", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v; the cause is not matchable, which is the whole reason for %%w", err)
	}
	if errors.Is(err, ErrBootUnsafe) {
		t.Fatal("a switch failure became boot-unsafe; the daemon would refuse to serve on an ordinary store error")
	}
	if errors.Is(err, ErrLaunchCleanupUnresolved) {
		t.Fatal("a switch failure became a launch-cleanup failure; Reconcile would abort boot on it")
	}
}
