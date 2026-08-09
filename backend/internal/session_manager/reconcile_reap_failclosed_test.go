package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// selectiveReapRuntime lets one board contain an uncertain runtime and another
// runtime that boot can safely reap. Reconcile must finish the safe reap pass
// work, then stop before RestoreAll launches any saved row.
type selectiveReapRuntime struct {
	*fakeRuntime
	probeErr map[string]error
}

func (r *selectiveReapRuntime) IsAlive(_ context.Context, handle ports.RuntimeHandle) (bool, error) {
	if err := r.probeErr[handle.ID]; err != nil {
		return false, err
	}
	return r.aliveByHandle[handle.ID], nil
}

func addSavedTerminatedWorker(st *fakeStore, id domain.SessionID, handle string) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		IsTerminated: true,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id),
			RuntimeHandleID: handle, AgentSessionID: "native-" + string(id), Prompt: "continue",
		},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees[id] = []domain.SessionWorktreeRecord{{
		SessionID: id, RepoName: domain.RootWorkspaceRepoName,
		WorktreePath: "/ws/" + string(id), Branch: "ao/" + string(id), State: "removed",
	}}
}

func TestReconcile_UnavailableTerminatedRuntimeBlocksEveryRestore(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	addSavedTerminatedWorker(st, "mer-1", "tmux-uncertain")
	// This independently-dead saved row proves the gate is board-wide: it is
	// safe in isolation, but RestoreAll must not begin while any reap is unknown.
	addSavedTerminatedWorker(st, "mer-2", "tmux-dead")
	// This unmarked live leak proves the reap pass still makes safe progress
	// after collecting the first finding.
	st.sessions["mer-3"] = domain.SessionRecord{
		ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		IsTerminated: true,
		Metadata:     domain.SessionMetadata{RuntimeHandleID: "tmux-live"},
	}

	cause := fmt.Errorf("tmux error connecting to stale socket: %w", ports.ErrRuntimeUnavailable)
	base := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-live": true}}
	rt := &selectiveReapRuntime{
		fakeRuntime: base,
		probeErr:    map[string]error{"tmux-uncertain": cause},
	}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	err := m.Reconcile(context.Background())
	if !errors.Is(err, ErrRuntimeReapUnresolved) || !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("Reconcile = %v, want ErrRuntimeReapUnresolved child of ErrBootUnsafe", err)
	}
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("Reconcile = %v, want original probe classification preserved", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime.Create calls = %d, want 0 before uncertain reap is resolved", rt.created)
	}
	if ws.lastCfg.SessionID != "" || len(ws.calls) != 0 {
		t.Fatalf("RestoreAll adopted workspace state despite unresolved reap: cfg=%+v calls=%v", ws.lastCfg, ws.calls)
	}
	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		if !st.sessions[id].IsTerminated || len(st.worktrees[id]) != 1 {
			t.Fatalf("saved row %s was consumed despite unresolved reap: session=%+v markers=%+v",
				id, st.sessions[id], st.worktrees[id])
		}
	}
	if !slices.Contains(rt.destroyedIDs, "tmux-live") {
		t.Fatalf("safe reap work did not continue after uncertainty: destroyed=%v", rt.destroyedIDs)
	}
}

func TestReconcile_UncertainBoardBlocksAutomaticFailoverBeforeAnyRuntimeEffect(t *testing.T) {
	st := newFailoverStore()
	workspacePath := t.TempDir()
	artifact, sha := pinImplementorTemplate(t, st.fakeStore)
	id := domain.SessionID("mer-auto")
	workerSession(st.fakeStore, id, domain.HarnessClaudeCode, workspacePath, artifact, sha)
	failoverLadder(st, domain.FailoverTarget{Harness: domain.HarnessCodex})
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-auto")
	addSavedTerminatedWorker(st.fakeStore, "mer-uncertain", "tmux-uncertain")

	cause := fmt.Errorf("tmux error connecting to stale socket: %w", ports.ErrRuntimeUnavailable)
	base := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": true}}
	rt := &selectiveReapRuntime{
		fakeRuntime: base,
		probeErr:    map[string]error{"tmux-uncertain": cause},
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = true
		return c
	}

	err := m.Reconcile(context.Background())
	if !errors.Is(err, ErrBootUnsafe) || !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("Reconcile = %v, want board-wide boot-unsafe probe error", err)
	}
	if len(st.attempts) != 0 || rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("automatic failover ran before the safety gate: attempts=%d created=%d destroyed=%d",
			len(st.attempts), rt.created, rt.destroyed)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("boot-unsafe board cleared the automatic incident pause")
	}
}

func TestReconcile_AuthoritativeDeadRuntimePermitsRestore(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	addSavedTerminatedWorker(st, "mer-1", "tmux-dead")
	// Missing from aliveByHandle is the fake's authoritative (false, nil).
	rt.aliveByHandle = map[string]bool{}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("runtime.Create calls = %d, want one restore after authoritative death", rt.created)
	}
	if ws.lastCfg.SessionID != "mer-1" {
		t.Fatalf("restored workspace config = %+v, want mer-1", ws.lastCfg)
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("authoritatively dead saved row was not restored")
	}
	if len(st.worktrees["mer-1"]) != 0 {
		t.Fatalf("one-shot restore marker was not consumed: %+v", st.worktrees["mer-1"])
	}
}

func TestReconcile_DefaultSocketServerAbsentMeansNoLeakAndPermitsRestore(t *testing.T) {
	m, st, rt, ws := newLifecycleManager()
	addSavedTerminatedWorker(st, "mer-1", "tmux-default-absent")
	rt.aliveErr = defaultSocketServerAbsent(t)

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rt.destroyed != 0 {
		t.Fatalf("Destroy calls = %d, want 0 when the server is absent", rt.destroyed)
	}
	if rt.created != 1 || ws.lastCfg.SessionID != "mer-1" {
		t.Fatalf("restore create/session = %d/%q, want 1/mer-1", rt.created, ws.lastCfg.SessionID)
	}
	if st.sessions["mer-1"].IsTerminated || len(st.worktrees["mer-1"]) != 0 {
		t.Fatalf("saved row was not restored: session=%+v markers=%+v",
			st.sessions["mer-1"], st.worktrees["mer-1"])
	}
}

func TestReconcileReap_AllUnresolvedRuntimeFailuresAreBootUnsafe(t *testing.T) {
	probeUnavailable := fmt.Errorf("stale socket: %w", ports.ErrRuntimeUnavailable)
	probeOther := errors.New("tmux returned malformed status")
	destroyFailure := errors.New("kill-session permission denied")

	for _, tc := range []struct {
		name  string
		alive bool
		probe error
		kill  error
		cause error
	}{
		{name: "runtime unavailable", probe: probeUnavailable, cause: probeUnavailable},
		{name: "other probe error", probe: probeOther, cause: probeOther},
		{name: "known live destroy error", alive: true, kill: destroyFailure, cause: destroyFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeRuntime{
				aliveByHandle: map[string]bool{"tmux-mer-1": tc.alive},
				aliveErr:      tc.probe,
				destroyErr:    tc.kill,
			}
			m := New(Deps{Runtime: rt})
			err := m.reconcileReap(context.Background(), domain.SessionRecord{
				ID: "mer-1", IsTerminated: true,
				Metadata: domain.SessionMetadata{RuntimeHandleID: "tmux-mer-1"},
			})
			if !errors.Is(err, ErrRuntimeReapUnresolved) || !errors.Is(err, ErrBootUnsafe) {
				t.Fatalf("reconcileReap = %v, want dedicated boot-unsafe classification", err)
			}
			if !errors.Is(err, tc.cause) {
				t.Fatalf("reconcileReap = %v, want cause %v preserved", err, tc.cause)
			}
		})
	}
}
