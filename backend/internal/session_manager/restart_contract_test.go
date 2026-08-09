package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// restartAssignmentAgent makes command delivery observable in RuntimeConfig:
// the saved assignment appears as exactly one argv element when the manager
// hands it to the adapter exactly once.
type restartAssignmentAgent struct {
	*recordingAgent
}

func (a *restartAssignmentAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	a.launchCalls++
	a.lastConfig = cfg.Config
	a.lastLaunch = cfg
	return []string{"launch", "--task", cfg.Prompt}, nil
}

type supervisedRestartAssignmentAgent struct {
	*restartAssignmentAgent
}

func (supervisedRestartAssignmentAgent) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

type restartWorkloadRuntime struct {
	*fakeRestartRuntime
	workloadAlive func(call int) (bool, error)
	workloadCalls int
	lastRef       ports.SupervisedProcessRef
}

func (r *restartWorkloadRuntime) IsSupervisedProcessAlive(_ context.Context, _ ports.RuntimeHandle, ref ports.SupervisedProcessRef) (bool, error) {
	r.workloadCalls++
	r.lastRef = ref
	return r.workloadAlive(r.workloadCalls)
}

func (r *restartWorkloadRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	err := r.fakeRuntime.Destroy(ctx, handle)
	if err == nil {
		r.aliveByHandle[handle.ID] = false
	}
	return err
}

func TestRestartContract_BranchlessScratchRestoresAssignmentExactlyOnce(t *testing.T) {
	const assignment = "continue the scratch task"
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID: "scratch-1", ProjectID: "scratch", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/scratch-1", Prompt: assignment},
	}
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	agent := &restartAssignmentAgent{recordingAgent: &recordingAgent{}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: msg, Lifecycle: &fakeLCM{store: st},
		DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	result, err := m.RestoreWithMode(ctx, "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != RestoreModeSavedPrompt {
		t.Fatalf("restore mode = %q, want %q", result.Mode, RestoreModeSavedPrompt)
	}
	if got := countRestartAssignment(rt.lastCfg.Argv, assignment); got != 1 {
		t.Fatalf("assignment occurrences in argv = %d, want 1: %#v", got, rt.lastCfg.Argv)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("messenger deliveries = %#v, want none for command delivery", msg.msgs)
	}
	if got := st.sessions["scratch-1"]; got.Metadata.Branch != "" || got.IsTerminated {
		t.Fatalf("branchless restart = %+v, want live session with no branch", got)
	}
}

func TestRestartContract_WorktreeRestoresAssignmentExactlyOnce(t *testing.T) {
	const assignment = "continue the worktree task"
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", Prompt: assignment},
	}
	rt := &fakeRuntime{outputs: []string{"ready>"}}
	msg := &fakeMessenger{}
	recorder := &recordingAgent{}
	agent := readinessAgent{
		afterStartAgent: afterStartAgent{recordingAgent: recorder},
		hints:           ports.PromptReadinessHints{Patterns: []string{"ready>"}, Timeout: time.Second},
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: msg, Lifecycle: &fakeLCM{store: st},
		DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	result, err := m.RestoreWithMode(ctx, "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != RestoreModeSavedPrompt {
		t.Fatalf("restore mode = %q, want %q", result.Mode, RestoreModeSavedPrompt)
	}
	if recorder.lastLaunch.Prompt != "" {
		t.Fatalf("launch prompt = %q, want blank for after-start delivery", recorder.lastLaunch.Prompt)
	}
	if len(msg.msgs) != 1 || msg.msgs[0] != assignment {
		t.Fatalf("messenger deliveries = %#v, want assignment exactly once", msg.msgs)
	}
	if got := countRestartAssignment(rt.lastCfg.Argv, assignment); got != 0 {
		t.Fatalf("assignment occurrences in argv = %d, want 0: %#v", got, rt.lastCfg.Argv)
	}
}

func TestRestartContract_DeliveryFailureDoesNotClaimSavedPrompt(t *testing.T) {
	const assignment = "continue after restart"
	deliveryErr := errors.New("pane unavailable")
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", Prompt: assignment},
	}
	rt := &fakeRuntime{outputs: []string{"ready>"}}
	msg := &fakeMessenger{err: deliveryErr}
	recorder := &recordingAgent{}
	m := New(Deps{
		Runtime: rt,
		Agents: singleAgent{agent: readinessAgent{
			afterStartAgent: afterStartAgent{recordingAgent: recorder},
			hints:           ports.PromptReadinessHints{Patterns: []string{"ready>"}, Timeout: time.Second},
		}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: msg,
		Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})

	result, err := m.RestoreWithMode(ctx, "mer-1")
	if err == nil {
		t.Fatal("RestoreWithMode succeeded, want delivery failure")
	}
	if result.Mode == RestoreModeSavedPrompt {
		t.Fatalf("failed restore mode = %q, must not claim saved_prompt", result.Mode)
	}
	if !errors.Is(err, deliveryErr) {
		t.Fatalf("restore error = %v, want wrapped delivery error", err)
	}
	for _, want := range []string{
		"task must be treated as unsent",
		"session was parked as terminated",
		"Resolve the reported prompt-readiness or pane-delivery failure",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("restore error missing %q: %v", want, err)
		}
	}
	if got := st.sessions["mer-1"]; !got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("failed delivery session = %+v, want terminated/exited", got)
	}
	if recorder.lastLaunch.Prompt != "" || len(msg.msgs) != 1 || msg.msgs[0] != assignment {
		t.Fatalf("delivery attempt launchPrompt=%q messages=%#v, want one host attempt only", recorder.lastLaunch.Prompt, msg.msgs)
	}
}

func TestRestartContract_LaunchTimeDeathIsNotOverwrittenAsIdle(t *testing.T) {
	tests := []struct {
		name          string
		workloadAlive func(call int) (bool, error)
		observeDeath  bool
		wantErr       error
		wantState     domain.ActivityState
		wantMode      RestoreMode
	}{
		{
			name:          "confirmed death repairs the idle seed",
			workloadAlive: func(int) (bool, error) { return false, nil },
			observeDeath:  true,
			wantErr:       ErrAgentExited,
			wantState:     domain.ActivityExited,
		},
		{
			name:          "failed probe is not proof of death",
			workloadAlive: func(int) (bool, error) { return false, errors.New("probe unavailable") },
			wantState:     domain.ActivityIdle,
			wantMode:      RestoreModeSavedPrompt,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const assignment = "resume the original assignment"
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			st.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
				Harness:  domain.HarnessCodex,
				Activity: domain.Activity{State: domain.ActivityExited},
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1",
					RuntimeHandleID: "tmux-mer-1", Prompt: assignment,
				},
			}
			base := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
			restarter := &fakeRestartRuntime{fakeRuntime: base}
			rt := &restartWorkloadRuntime{fakeRestartRuntime: restarter, workloadAlive: tt.workloadAlive}
			if tt.observeDeath {
				restarter.onRestart = func() {
					rec := st.sessions["mer-1"]
					rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
					st.sessions["mer-1"] = rec
				}
			}
			agent := supervisedRestartAssignmentAgent{restartAssignmentAgent: &restartAssignmentAgent{recordingAgent: &recordingAgent{}}}
			lcm := &fakeLCM{store: st}
			m := New(Deps{
				Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
				Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm,
				DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
				Executable:  func() (string, error) { return "/opt/ao", nil },
				NewLaunchID: func() string { return "launch-new" },
			})

			result, err := m.ResumeAgentWithMode(ctx, "mer-1")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResumeAgentWithMode error = %v, want %v", err, tt.wantErr)
				}
				if result.Mode == RestoreModeSavedPrompt {
					t.Fatalf("failed restart mode = %q, must not claim saved_prompt", result.Mode)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if result.Mode != tt.wantMode {
					t.Fatalf("restart mode = %q, want %q", result.Mode, tt.wantMode)
				}
			}
			got := st.sessions["mer-1"]
			if got.Activity.State != tt.wantState {
				t.Fatalf("activity after launch window = %q, want %q", got.Activity.State, tt.wantState)
			}
			if tt.wantErr != nil && !got.IsTerminated {
				t.Fatalf("confirmed-dead relaunch = %+v, want parked terminated", got)
			}
			if tt.wantErr == nil && got.IsTerminated {
				t.Fatalf("failed probe terminated session: %+v", got)
			}
			if lcm.completed != 1 {
				t.Fatalf("MarkSpawned calls = %d, want one idle seed before the fence", lcm.completed)
			}
			if rt.lastRef.SessionID != "mer-1" || rt.lastRef.LaunchID != "launch-new" {
				t.Fatalf("workload probe ref = %+v, want current launch", rt.lastRef)
			}
		})
	}
}

func TestRestartContract_PausedDeadRestartPreservesPauseAndDeliversAssignment(t *testing.T) {
	for _, tc := range []struct {
		name       string
		observedID string
	}{
		{name: "generation bound", observedID: "launch-old"},
		{name: "legacy unbound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const assignment = "resume the paused assignment"
			pause := &domain.SessionPause{
				IncidentID: "incident-1", Reason: domain.PauseReasonUsageLimit,
				DetectedBy: domain.PauseDetectionStructured, Harness: domain.HarnessCodex,
				ObservedRuntimeLaunchID: tc.observedID,
				EvidenceJSON: `{"version":1,"kind":"usage_limit","harness":"codex",` +
					`"scope":"account","sourceKey":"window-1"}`,
				PausedAt: time.Now().Add(-time.Minute),
			}
			if err := pause.Validate(); err != nil {
				t.Fatalf("fixture pause is invalid: %v", err)
			}
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			st.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
				Harness:  domain.HarnessCodex,
				Activity: domain.Activity{State: domain.ActivityExited},
				Metadata: domain.SessionMetadata{
					WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1",
					RuntimeHandleID: "tmux-mer-1", RuntimeLaunchID: "launch-old",
					Prompt: assignment, Pause: pause,
				},
			}
			base := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}, outputs: []string{"ready>"}}
			rt := &fakeRestartRuntime{fakeRuntime: base}
			msg := &fakeMessenger{}
			recorder := &recordingAgent{}
			m := New(Deps{
				Runtime: rt,
				Agents: singleAgent{agent: readinessAgent{
					afterStartAgent: afterStartAgent{recordingAgent: recorder},
					hints:           ports.PromptReadinessHints{Patterns: []string{"ready>"}, Timeout: time.Second},
				}},
				Workspace: &fakeWorkspace{}, Store: st, Messenger: msg,
				Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
				LookPath:    func(string) (string, error) { return "/bin/true", nil },
				NewLaunchID: func() string { return "launch-new" },
			})

			result, err := m.ResumeAgentWithMode(ctx, "mer-1")
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != RestoreModeSavedPrompt {
				t.Fatalf("restart mode = %q, want %q", result.Mode, RestoreModeSavedPrompt)
			}
			if recorder.lastLaunch.Prompt != "" || len(msg.msgs) != 1 || msg.msgs[0] != assignment {
				t.Fatalf("paused restart launchPrompt=%q messages=%#v, want one host delivery", recorder.lastLaunch.Prompt, msg.msgs)
			}
			got := st.sessions["mer-1"]
			if got.IsTerminated || got.Activity.State != domain.ActivityIdle {
				t.Fatalf("paused restart session = %+v, want live restarted agent", got)
			}
			if got.Metadata.Pause == nil || got.Metadata.Pause.IncidentID != pause.IncidentID {
				t.Fatalf("restart cleared or changed pause: got=%+v want=%+v", got.Metadata.Pause, pause)
			}
			if got.Metadata.RuntimeLaunchID != "launch-new" ||
				got.Metadata.Pause.ObservedRuntimeLaunchID != tc.observedID {
				t.Fatalf("restart rebound pause provenance: current=%q observed=%q want observed=%q",
					got.Metadata.RuntimeLaunchID, got.Metadata.Pause.ObservedRuntimeLaunchID, tc.observedID)
			}
		})
	}
}

func TestRestartContract_SavedPromptFallbackFamilyStillReportsSuccessfulOutcome(t *testing.T) {
	for _, harness := range []domain.AgentHarness{
		domain.HarnessCodex,
		domain.HarnessOpenCode,
		domain.HarnessClaudeCode,
	} {
		t.Run(string(harness), func(t *testing.T) {
			const assignment = "continue the saved task"
			st := newFakeStore()
			st.sessions["mer-1"] = domain.SessionRecord{
				ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
				Harness: harness, IsTerminated: true,
				Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "ao/mer-1", Prompt: assignment},
			}
			rt := &fakeRuntime{}
			agent := &restartAssignmentAgent{recordingAgent: &recordingAgent{}}
			m := New(Deps{
				Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
				Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
			})

			result, err := m.RestoreWithMode(ctx, "mer-1")
			if err != nil {
				t.Fatal(err)
			}
			if result.Mode != RestoreModeSavedPrompt {
				t.Fatalf("restore mode = %q, want successful saved-prompt outcome", result.Mode)
			}
			if got := countRestartAssignment(rt.lastCfg.Argv, assignment); got != 1 {
				t.Fatalf("assignment occurrences = %d, want 1: %#v", got, rt.lastCfg.Argv)
			}
		})
	}
}

func countRestartAssignment(argv []string, assignment string) int {
	count := 0
	for _, arg := range argv {
		if arg == assignment {
			count++
		}
	}
	return count
}
