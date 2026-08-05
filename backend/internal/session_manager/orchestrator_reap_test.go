package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// reapHarness builds a Manager wired for reap-queue tests, with a recording
// shell closer so shell-drain outcomes are observable.
func reapHarness(t *testing.T) (*Manager, *fakeStore, *fakeRuntime, *recordingShellCloser) {
	t.Helper()
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m, st, shells := reapHarnessWithRuntime(t, rt)
	return m, st, rt, shells
}

// reapHarnessWithRuntime is reapHarness over a caller-supplied runtime, so a
// test can choose whether the adapter can derive a handle from a session id.
func reapHarnessWithRuntime(t *testing.T, rt runtimeController) (*Manager, *fakeStore, *recordingShellCloser) {
	t.Helper()
	m, st := reapHarnessNoShells(t, rt)
	shells := &recordingShellCloser{}
	m.SetShellTerminalCloser(shells)
	return m, st, shells
}

// reapHarnessNoShells deliberately leaves the shell closer unwired, which is
// the boot-ordering bug the reaper must refuse to paper over.
func reapHarnessNoShells(t *testing.T, rt runtimeController) (*Manager, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st
}

// tmuxNamingRuntime derives handles through the real tmux adapter, so the
// empty-handle fallback is exercised against actual sanitisation instead of a
// stand-in that happens to be the identity function.
type tmuxNamingRuntime struct {
	*fakeRuntime
}

func (r *tmuxNamingRuntime) SessionHandle(id domain.SessionID) (ports.RuntimeHandle, error) {
	return tmux.New(tmux.Options{}).SessionHandle(id)
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

// TestDrainReapQueue_EmptyHandleProbesAdapterDerivedHandle guards the trap:
// destroyRuntimeProbed short-circuits an EMPTY handle to "confirmed dead"
// without probing at all. That is safe inside the switch saga but wrong here,
// so the reaper must fall back explicitly and still fail closed.
func TestDrainReapQueue_EmptyHandleProbesAdapterDerivedHandle(t *testing.T) {
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m, st, _ := reapHarnessWithRuntime(t, &tmuxNamingRuntime{fakeRuntime: rt})
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

	// And the derivation, not the raw id, decides what gets probed.
	got, resolveErr := m.reapProbeHandle(reapEntry("mer-1", "tmux-mer-1"))
	if resolveErr != nil || got.ID != "tmux-mer-1" {
		t.Fatalf("recorded handle must win when present, got %q (%v)", got.ID, resolveErr)
	}
}

// TestDrainReapQueue_EmptyHandleFallbackIsAdapterSanitized is the regression
// for probing the raw session id. tmux registers sessions under a sanitized,
// hash-suffixed name when the id is too long or holds characters it rejects,
// and its IsAlive validates whatever handle it is handed — so the raw id
// either probes a runtime that never existed (reading "dead" for something
// that may be alive) or is rejected outright, wedging boot forever on a name
// tmux could not have created. The fallback must ask the adapter.
func TestDrainReapQueue_EmptyHandleFallbackIsAdapterSanitized(t *testing.T) {
	// A session id tmux cannot use verbatim: illegal characters and too long.
	const rawID = domain.SessionID("mer/orchestrator:2026-06-10T09:30:00Z@canonical-workspace")
	want := tmux.SessionName(string(rawID))
	if want == string(rawID) {
		t.Fatalf("fixture is not exercising sanitisation: %q survived unchanged", rawID)
	}

	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m, st, _ := reapHarnessWithRuntime(t, &tmuxNamingRuntime{fakeRuntime: rt})
	entry := reapEntry(rawID, "")
	st.reapQueue = []domain.OrchestratorReapEntry{entry}
	// The runtime is alive under the name tmux actually registered.
	rt.aliveByHandle[want] = true

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want the sanitized handle probed and found alive", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("obligation must be retained")
	}
	got, resolveErr := m.reapProbeHandle(entry)
	if resolveErr != nil {
		t.Fatalf("resolve fallback handle: %v", resolveErr)
	}
	if got.ID != want {
		t.Fatalf("fallback probe handle = %q, want the adapter-derived %q", got.ID, want)
	}
	for _, id := range rt.destroyedIDs {
		if id == string(rawID) {
			t.Fatalf("probed the raw session id %q, which tmux never registers", rawID)
		}
	}
}

// TestDrainReapQueue_UnresolvableHandleFailsClosed: a runtime that cannot say
// which handle a session id maps to leaves us unable to probe anything, and
// "cannot probe" is not "dead".
func TestDrainReapQueue_UnresolvableHandleFailsClosed(t *testing.T) {
	// The bare fakeRuntime deliberately does NOT implement the resolver.
	m, st, rt, _ := reapHarness(t)
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "")}

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want the drain to fail when no handle can be derived", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("obligation must be retained")
	}
	if rt.destroyed != 0 {
		t.Errorf("nothing should have been destroyed on a guessed handle, got %d", rt.destroyed)
	}
}

// TestDrainReapQueue_NoShellCloserFailsClosed pins the boot-ordering contract
// from the other side. beginShellTerminalTeardown answers "no closer wired"
// with success — correct for ordinary teardown, fatal here, because it would
// discharge a durable obligation while checking nothing at all. Wiring the
// closer before the drain is a boot-order requirement, and violating it must
// surface as a failure rather than a silently skipped check.
func TestDrainReapQueue_NoShellCloserFailsClosed(t *testing.T) {
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": false}}
	m, st := reapHarnessNoShells(t, rt) // SetShellTerminalCloser never called
	st.reapQueue = []domain.OrchestratorReapEntry{reapEntry("mer-1", "tmux-mer-1")}

	err := m.DrainOrchestratorReapQueue(context.Background())
	if !errors.Is(err, ErrReapUnconfirmed) {
		t.Fatalf("err = %v, want ErrReapUnconfirmed even though the runtime died", err)
	}
	if !strings.Contains(err.Error(), "shell terminal closer not wired") {
		t.Fatalf("err = %v, want the wiring gap named", err)
	}
	if len(st.reapQueue) != 1 {
		t.Fatal("an unchecked obligation must be retained, not discharged")
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
