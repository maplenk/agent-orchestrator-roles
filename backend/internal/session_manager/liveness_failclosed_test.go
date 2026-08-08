package sessionmanager

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRestartRuntime_UnavailableProbeDoesNotLaunchReplacement(t *testing.T) {
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
		aliveErr: fmt.Errorf("tmux socket unreachable: %w", ports.ErrRuntimeUnavailable),
	}}
	m := New(Deps{Runtime: rt})

	_, err := m.restartRuntime(t.Context(), ports.RuntimeHandle{ID: "mer-1"}, ports.RuntimeConfig{})
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("restartRuntime error = %v, want ErrRuntimeUnavailable", err)
	}
	if rt.restarted != 0 || rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("probe uncertainty caused runtime effects: restart=%d create=%d destroy=%d", rt.restarted, rt.created, rt.destroyed)
	}
}

func TestRestartRuntime_AuthoritativeDeathLaunchesOnce(t *testing.T) {
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{}}}
	m := New(Deps{Runtime: rt})

	if _, err := m.restartRuntime(t.Context(), ports.RuntimeHandle{ID: "mer-1"}, ports.RuntimeConfig{}); err != nil {
		t.Fatalf("restartRuntime: %v", err)
	}
	if rt.created != 1 || rt.restarted != 0 || rt.destroyed != 0 {
		t.Fatalf("authoritative death effects: create=%d restart=%d destroy=%d, want 1/0/0", rt.created, rt.restarted, rt.destroyed)
	}
}

func TestReconcileLive_RuntimeUnavailableIsNotDeath(t *testing.T) {
	st := newFakeStore()
	rt := &fakeRuntime{aliveErr: fmt.Errorf("tmux error connecting: %w", ports.ErrRuntimeUnavailable)}
	ws := &fakeWorkspace{}
	lcm := &fakeLCM{store: st}
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: ws,
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: lcm,
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Metadata: domain.SessionMetadata{
			Branch: "ao/mer-1/root", WorkspacePath: "/wt/mer-1", RuntimeHandleID: "mer-1",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	err := m.reconcileLive(t.Context(), rec)
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("reconcileLive error = %v, want ErrRuntimeUnavailable", err)
	}
	assertNoLivenessProbeMutation(t, st, rt, ws, lcm, rec)
}

func TestReconcile_UnavailableBoardProbeCausesNoRuntimeOrWorkspaceEffects(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	rt.aliveErr = fmt.Errorf("shared socket permission denied: %w", ports.ErrRuntimeUnavailable)
	lcm := m.lcm.(*fakeLCM)

	const sessionCount = 4
	for i := 1; i <= sessionCount; i++ {
		id := domain.SessionID(fmt.Sprintf("mer-%d", i))
		st.sessions[id] = domain.SessionRecord{
			ID:        id,
			ProjectID: "mer",
			Kind:      domain.KindWorker,
			Metadata: domain.SessionMetadata{
				Branch: "ao/" + string(id) + "/root", WorkspacePath: "/wt/" + string(id), RuntimeHandleID: string(id),
			},
			Activity: domain.Activity{State: domain.ActivityActive},
		}
	}

	// Ordinary probe failures are logged per row so boot can continue. The
	// invariant under test is that none of those failures is converted into a
	// death verdict or a replacement launch.
	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("board probe uncertainty caused runtime effects: create=%d destroy=%d", rt.created, rt.destroyed)
	}
	if ws.stashCalls != 0 || len(ws.calls) != 0 {
		t.Fatalf("board probe uncertainty caused workspace effects: stash=%d calls=%v", ws.stashCalls, ws.calls)
	}
	if len(st.worktrees) != 0 {
		t.Fatalf("board probe uncertainty wrote restore markers: %#v", st.worktrees)
	}
	for id, rec := range st.sessions {
		if rec.IsTerminated {
			t.Fatalf("session %s terminated after unavailable probe", id)
		}
		if lcm.terminated[id] != 0 {
			t.Fatalf("MarkTerminated(%s) = %d, want 0", id, lcm.terminated[id])
		}
	}
}

func assertNoLivenessProbeMutation(
	t *testing.T,
	st *fakeStore,
	rt *fakeRuntime,
	ws *fakeWorkspace,
	lcm *fakeLCM,
	rec domain.SessionRecord,
) {
	t.Helper()
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("probe uncertainty caused runtime effects: create=%d destroy=%d", rt.created, rt.destroyed)
	}
	if ws.stashCalls != 0 || len(ws.calls) != 0 {
		t.Fatalf("probe uncertainty caused workspace effects: stash=%d calls=%v", ws.stashCalls, ws.calls)
	}
	if lcm.terminated[rec.ID] != 0 {
		t.Fatalf("MarkTerminated(%s) = %d, want 0", rec.ID, lcm.terminated[rec.ID])
	}
	if len(st.worktrees[rec.ID]) != 0 {
		t.Fatalf("probe uncertainty wrote a restore marker: %#v", st.worktrees[rec.ID])
	}
}
