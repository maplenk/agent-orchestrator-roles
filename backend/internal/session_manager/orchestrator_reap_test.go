package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// reapHarness builds a Manager wired for reap-queue tests, with a recording
// shell closer so shell-drain outcomes are observable.
func reapHarness(t *testing.T) (*Manager, *fakeStore, *fakeRuntime, *recordingShellCloser) {
	t.Helper()
	st := newFakeStore()
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	shells := &recordingShellCloser{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetShellTerminalCloser(shells)
	return m, st, rt, shells
}

func reapEntry(id domain.SessionID, handle string) domain.OrchestratorReapEntry {
	return domain.OrchestratorReapEntry{
		SessionID: id, ProjectID: "mer",
		RuntimeHandleID: handle, RuntimeLaunchID: "launch-" + string(id),
		WorkspacePath: "/ws/mer/orchestrator/mer-orchestrator",
		QueuedAt:      time.Now().Add(-time.Hour),
	}
}

// TestDrainReapQueue_EmptyQueueIsClean keeps a healthy database from paying any
// cost.
func TestDrainReapQueue_EmptyQueueIsClean(t *testing.T) {
	m, _, rt, _ := reapHarness(t)
	if err := m.DrainOrchestratorReapQueue(context.Background()); err != nil {
		t.Fatalf("empty queue must drain cleanly: %v", err)
	}
	if rt.destroyed != 0 {
		t.Errorf("nothing should have been destroyed, got %d", rt.destroyed)
	}
}

// TestDrainReapQueue_MissingTableAbortsBoot is the fail-closed contract for the
// schema itself: "the table is not there" must never be read as "nothing is
// owed", because that is indistinguishable from a database that owes everything.
func TestDrainReapQueue_MissingTableAbortsBoot(t *testing.T) {
	m, st, _, _ := reapHarness(t)
	st.reapQueueErr = errors.New("no such table: orchestrator_reap_queue")

	err := m.DrainOrchestratorReapQueue(context.Background())
	if err == nil {
		t.Fatal("a missing queue table must abort boot, not drain silently")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("err = %v, want the underlying schema error surfaced", err)
	}
}

// TestDrainReapQueue_ConfirmedDeathDischarges is the happy path: a superseded
// orchestrator whose runtime is confirmed dead and whose shells close is
// removed from the queue.
func TestDrainReapQueue_ConfirmedDeathDischarges(t *testing.T) {
	m, st, rt, shells := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}
	rt.aliveByHandle["tmux-mer-1"] = false // confirmed dead

	if err := m.DrainOrchestratorReapQueue(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(st.reapQueue) != 0 {
		t.Fatalf("queue = %+v, want the discharged obligation removed", st.reapQueue)
	}
	if !shells.didDrain("mer-1") {
		t.Error("scoped shells must be closed: they run inside the survivor's workspace")
	}
}

// TestDrainReapQueue_LiveRuntimeFailsClosed pins the core requirement: a
// superseded orchestrator still executing must keep its obligation AND abort
// boot.
func TestDrainReapQueue_LiveRuntimeFailsClosed(t *testing.T) {
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}
	rt.aliveByHandle["tmux-mer-1"] = true // survives the destroy attempt

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want ErrReapUnconfirmed", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("an unconfirmed obligation must be retained, not discharged")
	}
	if len(st.reapAttempts) != 1 || st.reapAttempts[0] != "mer-1" {
		t.Errorf("attempts = %v, want the failure stamped for visibility", st.reapAttempts)
	}
}

// TestDrainReapQueue_UncertainProbeFailsClosed covers the third probe outcome.
// A probe that cannot establish reality is NOT death — collapsing it to "dead"
// is the mistake reconcileReap's own comment warns against.
func TestDrainReapQueue_UncertainProbeFailsClosed(t *testing.T) {
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}
	rt.aliveErr = errors.New("tmux server unreachable")

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want ErrReapUnconfirmed", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("an inconclusive probe must retain the obligation")
	}
}

// TestDrainReapQueue_UnclosableShellFailsClosed: shells live inside the
// survivor's canonical workspace, so one that cannot be confirmed closed is as
// disqualifying as a live runtime. This is where the reaper's contract diverges
// from drainScopedShells, which is best-effort by design.
func TestDrainReapQueue_UnclosableShellFailsClosed(t *testing.T) {
	m, st, rt, shells := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}
	rt.aliveByHandle["tmux-mer-1"] = false // runtime IS dead
	shells.err = errors.New("shell terminal still open")

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want ErrReapUnconfirmed even though the runtime died", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("an unclosable shell must retain the obligation")
	}
}

// TestDrainReapQueue_EmptyHandleProbesSessionIDFallback guards the trap:
// destroyRuntimeProbed short-circuits an EMPTY handle to "confirmed dead"
// without probing at all. That is safe inside the switch saga but wrong here,
// so the reaper must fall back explicitly and still fail closed.
func TestDrainReapQueue_EmptyHandleProbesSessionIDFallback(t *testing.T) {
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "")}
	// A runtime named after the session outlived the cleared handle field.
	rt.aliveByHandle["mer-1"] = true

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v: an empty handle must not be read as already dead", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("obligation must be retained")
	}
	// And the fallback targeted the session id, not nothing at all.
	if got := reapProbeHandle(reapEntry("mer-1", "")).ID; got != "mer-1" {
		t.Fatalf("fallback probe handle = %q, want the session id", got)
	}
	if got := reapProbeHandle(reapEntry("mer-1", "tmux-mer-1")).ID; got != "tmux-mer-1" {
		t.Fatalf("recorded handle must win when present, got %q", got)
	}
}

// TestDrainReapQueue_PartialDrainIsIdempotent pins restart behaviour: entries
// whose death was confirmed are discharged even when a sibling fails, and a
// re-run after the blocker clears drains the rest without re-probing the ones
// already gone.
func TestDrainReapQueue_PartialDrainIsIdempotent(t *testing.T) {
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{
		reapEntry("mer-1", "tmux-mer-1"),
		reapEntry("mer-2", "tmux-mer-2"),
	}
	rt.aliveByHandle["tmux-mer-1"] = false // dead
	rt.aliveByHandle["tmux-mer-2"] = true  // still alive

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want the whole drain to fail while one is outstanding", err)
	}
	if len(st.reapQueue) != 1 || st.reapQueue[0].SessionID != "mer-2" {
		t.Fatalf("queue = %+v, want only the unconfirmed obligation retained", st.reapQueue)
	}

	// Next boot: the survivor's blocker is gone.
	rt.aliveByHandle["tmux-mer-2"] = false
	rt.destroyedIDs = nil
	if err := m.DrainOrchestratorReapQueue(context.Background()); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	if len(st.reapQueue) != 0 {
		t.Fatalf("queue = %+v, want fully drained", st.reapQueue)
	}
	for _, id := range rt.destroyedIDs {
		if id == "tmux-mer-1" {
			t.Error("already-discharged obligation must not be probed again")
		}
	}
}

// TestDrainReapQueue_NeverTouchesTheWorkspace: the canonical workspace belongs
// to the survivor. Only execution surfaces are reaped.
func TestDrainReapQueue_NeverTouchesTheWorkspace(t *testing.T) {
	st := newFakeStore()
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	ws := &pathRecordingWorkspace{}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": false}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	m.SetShellTerminalCloser(&recordingShellCloser{})
	entry := reapEntry("mer-1", "tmux-mer-1")
	st.reapQueue = []domain.OrchestratorReapEntry{entry}

	if err := m.DrainOrchestratorReapQueue(context.Background()); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if ws.touched(entry.WorkspacePath) {
		t.Fatalf("reaper removed %q, which the surviving orchestrator owns", entry.WorkspacePath)
	}
}

// TestDrainReapQueue_UndischargeableEntryFailsClosed: death was confirmed but
// the obligation could not be cleared. Keeping it costs one redundant probe;
// dropping it would lose a durable record that cannot be rebuilt.
func TestDrainReapQueue_UndischargeableEntryFailsClosed(t *testing.T) {
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}
	rt.aliveByHandle["tmux-mer-1"] = false
	st.reapDeleteErr = errors.New("disk full")

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want the drain to fail when an entry cannot be cleared", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("the entry must be retained when it could not be deleted")
	}
}
