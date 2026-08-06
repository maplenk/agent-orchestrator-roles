package sessionmanager

import (
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
