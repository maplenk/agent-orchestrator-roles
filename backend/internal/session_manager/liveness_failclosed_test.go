package sessionmanager

import (
	"errors"
	"fmt"
	"testing"

	tmuxruntime "github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func defaultSocketServerAbsent(t *testing.T) error {
	t.Helper()
	defaultDir := t.TempDir()
	if socket := tmuxruntime.SocketForDataDir(defaultDir, defaultDir); socket != "" {
		t.Fatalf("default SocketForDataDir = %q, want empty", socket)
	}
	return fmt.Errorf("tmux runtime: probe session mer-1: %w: no server running on default socket", ports.ErrRuntimeServerAbsent)
}

func namespacedSocketServerAbsent(t *testing.T) error {
	t.Helper()
	defaultDir := t.TempDir()
	if socket := tmuxruntime.SocketForDataDir(t.TempDir(), defaultDir); socket == "" {
		t.Fatal("isolated data dir must produce a namespaced socket")
	}
	return fmt.Errorf("tmux runtime: probe session mer-1: %w: no server running on namespaced socket", ports.ErrRuntimeServerAbsent)
}

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

func TestRestartAgent_DefaultSocketServerAbsentLaunchesReplacement(t *testing.T) {
	const assignment = "continue the saved task"
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", RuntimeHandleID: "tmux-mer-1",
			Prompt: assignment,
		},
	}
	rt := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{aliveErr: defaultSocketServerAbsent(t)}}
	agent := &restartAssignmentAgent{recordingAgent: &recordingAgent{}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	result, err := m.ResumeAgentWithMode(t.Context(), "mer-1")
	if err != nil {
		t.Fatalf("Restart Agent: %v", err)
	}
	if result.Mode != RestoreModeSavedPrompt {
		t.Fatalf("restart mode = %q, want %q", result.Mode, RestoreModeSavedPrompt)
	}
	if rt.created != 1 || rt.restarted != 0 || rt.destroyed != 0 {
		t.Fatalf("runtime effects create/restart/destroy = %d/%d/%d, want 1/0/0",
			rt.created, rt.restarted, rt.destroyed)
	}
	got := st.sessions["mer-1"]
	if got.Activity.State != domain.ActivityIdle || got.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("restarted session = %+v, want idle on replacement handle h1", got)
	}
}

func TestReconcileLive_RuntimeUnavailableIsNotDeath(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "error connecting", err: fmt.Errorf("tmux error connecting: %w", ports.ErrRuntimeUnavailable)},
		{name: "permission denied", err: fmt.Errorf("tmux error connecting (Permission denied): %w", ports.ErrRuntimeUnavailable)},
		{name: "stale socket", err: fmt.Errorf("tmux error connecting (No such file or directory): %w", ports.ErrRuntimeUnavailable)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			rt := &fakeRuntime{aliveErr: tc.err}
			ws := &fakeWorkspace{}
			lcm := &fakeLCM{store: st}
			m := New(Deps{
				Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
				Messenger: &fakeMessenger{}, Lifecycle: lcm,
				LookPath: func(string) (string, error) { return "/bin/true", nil },
			})
			rec := domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
				Metadata: domain.SessionMetadata{
					Branch: "ao/mer-1/root", WorkspacePath: "/wt/mer-1", RuntimeHandleID: "mer-1",
				},
				Activity: domain.Activity{State: domain.ActivityActive},
			}

			err := m.reconcileLive(t.Context(), rec)
			if !errors.Is(err, ports.ErrRuntimeUnavailable) || errors.Is(err, ports.ErrRuntimeServerAbsent) {
				t.Fatalf("reconcileLive error = %v, want unavailable but not server absent", err)
			}
			assertNoLivenessProbeMutation(t, st, rt, ws, lcm, rec)
		})
	}
}

func TestReconcile_DefaultSocketServerAbsentSavesAndRestoresOnSameBoot(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	rt.aliveErr = defaultSocketServerAbsent(t)
	ws.stashRef = "refs/ao/preserved/mer-1"
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Metadata: domain.SessionMetadata{
			Branch: "ao/mer-1/root", WorkspacePath: "/ws/mer-1", RuntimeHandleID: "tmux-mer-1",
			Prompt: "continue",
		},
		Activity: domain.Activity{State: domain.ActivityActive},
	}

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("boot reconcile with normally absent default server: %v", err)
	}
	if ws.stashCalls != 1 {
		t.Fatalf("StashUncommitted calls = %d, want 1", ws.stashCalls)
	}
	if rt.created != 1 || rt.destroyed != 0 {
		t.Fatalf("runtime create/destroy = %d/%d, want 1/0", rt.created, rt.destroyed)
	}
	if got := st.sessions["mer-1"]; got.IsTerminated || got.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("boot-restored session = %+v, want active replacement handle h1", got)
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Fatalf("restore marker was not consumed: %+v", rows)
	}
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
