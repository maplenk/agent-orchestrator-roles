package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// MarkSpawned is the single write that adopts a launch: it clears
// is_terminated and records RuntimeHandleID/RuntimeLaunchID together. The tests
// below cover the window where the runtime already exists but that write has
// failed — the one place AO can hold a process no row names.
//
// The motivating failure is migration 0046's one-active-orchestrator index
// rejecting the activation, which is why domain.ErrActiveOrchestratorExists is
// the injected error throughout; the cleanup contract is the same for any
// MarkSpawned failure.

// resumeHarness builds an ACTIVE session whose agent has exited — the state
// ResumeAgentWithMode requires — with a workspace-path recording workspace so
// tests can prove teardown never touched it.
func resumeHarness(t *testing.T, kind domain.SessionKind, rt runtimeController) (*Manager, *fakeStore, *pathRecordingWorkspace) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      kind,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/ws/mer/orchestrator/mer-orchestrator",
			Branch:          "ao/mer-orchestrator",
			RuntimeHandleID: "tmux-mer-1",
			RuntimeLaunchID: "launch-old",
			AgentSessionID:  "agent-x",
			Prompt:          "continue the task",
		},
	}
	ws := &pathRecordingWorkspace{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle:   &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	return m, st, ws
}

// TestResumeAgent_ConstraintLossLeavesNoPhantomLiveOrchestrator is the primary
// adversarial case.
//
// Before this fix, a MarkSpawned failure on the resume path ran a bare
// `_ = m.runtime.Destroy(ctx, handle)` and returned. The row was never touched,
// so it stayed ACTIVE while still naming the OLD RuntimeHandleID that destroy
// had just killed: a session that reads as live with no process behind it. For
// an orchestrator that row also holds the project's single active slot under
// migration 0046, so no replacement could be spawned — the project wedged with
// no way out.
func TestResumeAgent_ConstraintLossLeavesNoPhantomLiveOrchestrator(t *testing.T) {
	// The runtime is confirmed dead once destroyed (absent from aliveByHandle).
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	m, st, ws := resumeHarness(t, domain.KindOrchestrator, rt)
	m.lcm.(*fakeLCM).markSpawnedErr = domain.ErrActiveOrchestratorExists

	_, err := m.ResumeAgentWithMode(context.Background(), "mer-1")
	if !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v, want the constraint loss surfaced to the caller", err)
	}

	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Error("session is still ACTIVE with no agent process: a phantom-live orchestrator " +
			"that holds the project's only active slot forever")
	}
	if got.Metadata.RuntimeHandleID != "" || got.Metadata.RuntimeLaunchID != "" {
		t.Errorf("row still names a destroyed runtime: handle=%q launch=%q",
			got.Metadata.RuntimeHandleID, got.Metadata.RuntimeLaunchID)
	}
	// Recovery is Restore, which rebuilds from these — they must survive.
	if got.Metadata.WorkspacePath == "" || got.Metadata.Branch == "" {
		t.Errorf("restore inputs were cleared: %+v", got.Metadata)
	}
	if ws.touched("/ws/mer/orchestrator/mer-orchestrator") {
		t.Error("relaunch rollback removed the canonical orchestrator worktree, which it does not own")
	}
}

// TestResumeAgent_ConstraintLossRecordsSurvivingRuntime covers the other half:
// the runtime OUTLIVES the teardown. Marking the session terminated without
// recording what is running would leave a process executing inside the
// workspace with nothing in the database naming it — and reconcile, Kill and
// the boot reaper all work from the recorded handle, so it would be
// unreachable, not merely leaked.
func TestResumeAgent_ConstraintLossRecordsSurvivingRuntime(t *testing.T) {
	// The old handle is already dead, so the pre-launch restart falls through to
	// Create ("h1"); only the NEW runtime refuses to die.
	rt := &stubbornRuntime{
		fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"h1": true}},
		stubbornID:  "h1",
	}
	m, st, _ := resumeHarness(t, domain.KindOrchestrator, rt)
	m.lcm.(*fakeLCM).markSpawnedErr = domain.ErrActiveOrchestratorExists

	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v", err)
	}

	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Error("session must not stay active for a launch that was never adopted")
	}
	if got.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("row names %q, but the surviving runtime is \"h1\": nothing can find it to reap it",
			got.Metadata.RuntimeHandleID)
	}
	if got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Errorf("launch id = %q, want the new generation so fenced observations match",
			got.Metadata.RuntimeLaunchID)
	}
}

// TestResumeAgent_UncertainProbeRecordsSurvivingRuntime: a probe that cannot
// establish reality is not death. It must be treated exactly like "alive" —
// the identity is recorded rather than assumed gone.
func TestResumeAgent_UncertainProbeRecordsSurvivingRuntime(t *testing.T) {
	rt := &stubbornRuntime{
		fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{}},
		probeErrID:  "h1", // only the post-launch probe is inconclusive
	}
	m, st, _ := resumeHarness(t, domain.KindOrchestrator, rt)
	m.lcm.(*fakeLCM).markSpawnedErr = domain.ErrActiveOrchestratorExists

	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v", err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.RuntimeHandleID != "h1" || got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("an inconclusive probe must record the runtime, not assume it died: %+v", got.Metadata)
	}
}

// TestSwitchWorker_LaunchFailureKeepsSessionRecoverable is the counterweight to
// the terminate-on-failure rule above.
//
// The switch saga stops the source BEFORE launching the target, and owns that
// window under ErrSwitchPostStop. RecoverSwitchFromPostStop explicitly refuses a
// terminated session, so terminating in the launch rollback would replace a
// recoverable state with a dead end. The runtime is still reaped
// probe-authoritatively — only the row's terminal state is left to the saga.
func TestSwitchWorker_LaunchFailureKeepsSessionRecoverable(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")

	_, err := m.SwitchWorker(context.Background(), SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v, want ErrSwitchPostStop", err)
	}
	got := st.sessions[id]
	if got.IsTerminated {
		t.Fatal("switch launch rollback terminated the session; RecoverSwitchFromPostStop refuses " +
			"terminated sessions, so the documented recovery path is now unreachable")
	}
	// The target runtime is still reaped — only the row's state is the saga's.
	if rt.destroyed == 0 {
		t.Error("the target runtime was left running")
	}
}

// stubbornRuntime narrows fakeRuntime's global destroyErr/aliveErr to a single
// handle, so a test can let the pre-launch restart probe succeed and fail only
// the post-launch reap — the window this file is about.
type stubbornRuntime struct {
	*fakeRuntime
	stubbornID string // refuses to die, and stays alive
	probeErrID string // liveness cannot be established
}

func (r *stubbornRuntime) Destroy(ctx context.Context, h ports.RuntimeHandle) error {
	if h.ID == r.stubbornID {
		r.destroyed++
		r.destroyedIDs = append(r.destroyedIDs, h.ID)
		return errors.New("tmux: kill-session refused")
	}
	return r.fakeRuntime.Destroy(ctx, h)
}

func (r *stubbornRuntime) IsAlive(ctx context.Context, h ports.RuntimeHandle) (bool, error) {
	if h.ID == r.probeErrID {
		return false, errors.New("tmux server unreachable")
	}
	return r.fakeRuntime.IsAlive(ctx, h)
}

// TestResumeAgent_ConstraintLossOnWorkerAlsoParksCleanly: the contract is not
// orchestrator-specific. A worker resume that loses at MarkSpawned must not
// leave a live-looking row either.
func TestResumeAgent_ConstraintLossOnWorkerAlsoParksCleanly(t *testing.T) {
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	m, st, _ := resumeHarness(t, domain.KindWorker, rt)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")

	if _, err := m.ResumeAgentWithMode(context.Background(), "mer-1"); err == nil {
		t.Fatal("want the MarkSpawned failure surfaced")
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated || got.Metadata.RuntimeHandleID != "" {
		t.Fatalf("worker resume left a live-looking row: terminated=%v meta=%+v", got.IsTerminated, got.Metadata)
	}
}

// TestSpawn_MarkSpawnedFailureKeepsWorkspaceWhenRuntimeSurvives pins the spawn
// half. The old rollback derived "destroyed" from `Destroy(...) == nil`, so a
// destroy that reported success while the pane survived removed the worktree
// out from under a live agent. Confirmation now comes from a probe, and an
// unconfirmed runtime blocks the workspace teardown outright.
func TestSpawn_MarkSpawnedFailureKeepsWorkspaceWhenRuntimeSurvives(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	// "h1" is what fakeRuntime.Create returns; it stays alive through Destroy.
	rt := &fakeRuntime{aliveByHandle: map[string]bool{"h1": true}, destroyErr: errors.New("kill refused")}
	ws := &pathRecordingWorkspace{}
	ws.path = "/ws/mer-1"
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex"}}}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle:   &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	m.lcm.(*fakeLCM).markSpawnedErr = domain.ErrActiveOrchestratorExists

	_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "go"})
	if !errors.Is(err, domain.ErrActiveOrchestratorExists) {
		t.Fatalf("err = %v, want the constraint loss surfaced", err)
	}
	if ws.touched("/ws/mer-1") || ws.fakeWorkspace.destroyed != 0 {
		t.Errorf("workspace was destroyed while the runtime may still be executing in it (destroyed=%d)", ws.fakeWorkspace.destroyed)
	}
	// And the surviving runtime is recorded, so it is still reapable.
	for id, rec := range st.sessions {
		if rec.Metadata.RuntimeHandleID != "h1" {
			t.Errorf("session %s does not name the surviving runtime: %+v", id, rec.Metadata)
		}
	}
}
