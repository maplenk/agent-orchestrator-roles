package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// newChatManager mirrors newManager() with a chat launcher injected, so both
// branches can be exercised against the same fakes.
func newChatManager(chat ChatLauncher) (*Manager, *fakeStore, *fakeRuntime) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime:   rt,
		Agents:    fakeAgents{},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Chat:      chat,
		Lifecycle: &fakeLCM{store: st},
		DataDir:   "/ao-test-data",
		LookPath:  lookPath,
	})
	return m, st, rt
}

const chatTestProject = domain.ProjectID("mer")

// The load-bearing property of the split: exactly one controller starts. A chat
// spawn must not touch the terminal runtime, and a TUI spawn must not touch the
// chat launcher. Anything else means two writers on one conversation.

type recordingLauncher struct {
	preflightErr error
	startErr     error
	turnErr      error

	preflighted []domain.AgentHarness
	started     []ChatStart
	turns       []string
	// relayed is what arrived through Manager.Send rather than as an initial
	// prompt, kept separate so a test can tell the two apart.
	relayed []string
	stopped []domain.SessionID
}

func (l *recordingLauncher) PreflightChat(_ context.Context, harness domain.AgentHarness) error {
	l.preflighted = append(l.preflighted, harness)
	return l.preflightErr
}

func (l *recordingLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.started = append(l.started, cfg)
	if l.startErr != nil {
		return ChatStarted{}, l.startErr
	}
	return ChatStarted{
		ProviderConversationID: "thread-1",
		ControllerGeneration:   "gen-1",
	}, nil
}

func (l *recordingLauncher) StartChatTurn(_ context.Context, _ domain.SessionID, text string) (string, error) {
	l.turns = append(l.turns, text)
	return "turn-1", l.turnErr
}

func (l *recordingLauncher) RelayChatTurn(_ context.Context, _ domain.SessionID, text string) (string, error) {
	l.relayed = append(l.relayed, text)
	return "turn-relay", l.turnErr
}

func (l *recordingLauncher) RelayChatTurnWithID(_ context.Context, _ domain.SessionID, text, _ string) (string, error) {
	l.relayed = append(l.relayed, text)
	return "turn-relay", l.turnErr
}

func (l *recordingLauncher) StopChat(_ context.Context, id domain.SessionID) error { //nolint:unparam

	l.stopped = append(l.stopped, id)
	return nil
}

// An unsupported chat request must be refused before anything durable exists: no
// session row, no worktree, nothing to clean up.
func TestChatSpawnRejectedBeforeDurableStateWhenUnsupported(t *testing.T) {
	mgr, store, _ := newChatManager(&recordingLauncher{preflightErr: ports.ErrChatUnsupported})
	launcher := mgr.chat.(*recordingLauncher)

	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		Prompt:        "do the thing",
		RequestedMode: domain.SessionModeChat,
	})
	if !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("err = %v, want ErrChatUnsupported", err)
	}

	sessions, listErr := store.ListAllSessions(context.Background())
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	if len(sessions) != 0 {
		t.Fatalf("a refused chat spawn left %d session rows behind", len(sessions))
	}
	if len(launcher.started) != 0 {
		t.Error("a refused preflight still started a controller")
	}
}

// Chat mode with no launcher wired must fail, never silently become a TUI session
// in a terminal the user did not ask for.
func TestChatSpawnWithoutLauncherIsRefusedNotDowngraded(t *testing.T) {
	mgr, _, runtime := newChatManager(nil)

	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	})
	if !errors.Is(err, ports.ErrChatUnsupported) {
		t.Fatalf("err = %v, want ErrChatUnsupported", err)
	}
	if runtime.created != 0 {
		t.Fatalf("a refused chat spawn created %d runtimes — it downgraded to TUI", runtime.created)
	}
}

// A TUI spawn must never reach the chat launcher, even when one is wired.
func TestTUISpawnNeverTouchesTheChatLauncher(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, _, runtime := newChatManager(launcher)

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject,
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Prompt:    "hello",
		// No requested mode: resolution must land on TUI.
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rec.Mode != domain.SessionModeTUI {
		t.Fatalf("mode = %q, want tui when none was requested", rec.Mode)
	}
	if len(launcher.preflighted) != 0 || len(launcher.started) != 0 {
		t.Fatalf("a TUI spawn reached the chat launcher: preflight=%v started=%d",
			launcher.preflighted, len(launcher.started))
	}
	if runtime.created == 0 {
		t.Error("a TUI spawn created no runtime")
	}
}

// A chat spawn must persist its mode and provider handle, start no runtime, and
// deliver the initial prompt as a turn.
func TestChatSpawnStartsControllerAndNoRuntime(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, _, runtime := newChatManager(launcher)

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindOrchestrator,
		Harness:       domain.HarnessCodex,
		Prompt:        "coordinate the work",
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if rec.Mode != domain.SessionModeChat {
		t.Fatalf("mode = %q, want chat", rec.Mode)
	}
	if runtime.created != 0 {
		t.Fatalf("a chat spawn created %d terminal runtimes, want 0", runtime.created)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d controllers, want 1", len(launcher.started))
	}

	start := launcher.started[0]
	if start.WorkspacePath == "" {
		t.Error("controller started with no workspace path")
	}
	if start.DataDir != "/ao-test-data" {
		t.Errorf("controller data dir = %q, want manager-owned data dir", start.DataDir)
	}
	// The controller must receive the session env, which is what carries the
	// HookPATH pin in production and is how the agent's own shell commands find
	// `ao` — the mechanism an orchestrator delegates through.
	//
	// The PATH value itself is not asserted here: HookPATH deliberately declines
	// to pin when the running binary is not named "ao", which is always the case
	// under `go test`. That the pin works end to end was verified against a real
	// app-server with a fake `ao` on an injected PATH.
	if start.Env == nil {
		t.Error("controller started with no environment; the agent could not resolve `ao`")
	}
	if start.Env[EnvSessionID] == "" {
		t.Errorf("controller env missing %s; session-scoped hooks would not identify the session", EnvSessionID)
	}

	// The provider handle must be persisted, or a restart cannot resume.
	if rec.Metadata.ProviderConversationID != "thread-1" {
		t.Errorf("provider conversation id = %q", rec.Metadata.ProviderConversationID)
	}
	if rec.Metadata.ControllerGeneration != "gen-1" {
		t.Errorf("controller generation = %q", rec.Metadata.ControllerGeneration)
	}
	// A chat session has no agent pane; leaving these empty is what stops the
	// reaper probing for a terminal that never existed.
	if rec.Metadata.RuntimeHandleID != "" || rec.Metadata.RuntimeLaunchID != "" {
		t.Errorf("chat session carries runtime handles: handle=%q launch=%q",
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID)
	}

	if len(launcher.turns) != 1 || launcher.turns[0] == "" {
		t.Fatalf("initial prompt was not delivered as a turn: %v", launcher.turns)
	}
}

// A controller that fails to start must leave nothing running and no live row.
func TestChatSpawnRollsBackWhenControllerFailsToStart(t *testing.T) {
	mgr, store, runtime := newChatManager(&recordingLauncher{startErr: errors.New("app-server exited")})

	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	})
	if err == nil {
		t.Fatal("expected a failed controller start to fail the spawn")
	}
	if runtime.created != 0 {
		t.Error("a failed chat spawn created a terminal runtime")
	}

	sessions, listErr := store.ListAllSessions(context.Background())
	if listErr != nil {
		t.Fatalf("list sessions: %v", listErr)
	}
	for _, session := range sessions {
		if !session.IsTerminated {
			t.Errorf("session %s left live after a failed chat spawn", session.ID)
		}
	}
}

// Kill must close the controller, not tear down a runtime the session never had.
// A chat controller owns an app-server child process, so skipping this leaks it.
func TestKillClosesTheChatControllerAndTouchesNoRuntime(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, _, runtime := newChatManager(launcher)
	ctx := context.Background()

	rec, _, _, err := mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if _, err := mgr.Kill(ctx, rec.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if len(launcher.stopped) != 1 || launcher.stopped[0] != rec.ID {
		t.Fatalf("controller not closed on kill: %v", launcher.stopped)
	}
	if runtime.destroyed != 0 {
		t.Errorf("kill destroyed %d runtimes for a session that never had one", runtime.destroyed)
	}
}

// Restore must not cross modes. A chat session resumes its provider conversation
// with the handle it stored; giving it a terminal would hand it a controller it
// was not created with.
func TestRestoreResumesChatRatherThanRelaunchingATerminal(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, runtime := newChatManager(launcher)
	ctx := context.Background()

	rec, _, _, err := mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	startsAfterSpawn := len(launcher.started)
	runtimeAfterSpawn := runtime.created

	// Simulate a daemon restart: the controller dies and the session is marked
	// terminated with a restore marker, which is the state RestoreAll acts on.
	if _, err := mgr.Kill(ctx, rec.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	stored, _, err := store.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if stored.Metadata.ProviderConversationID == "" {
		t.Fatal("spawn stored no provider conversation id; a restart could not resume")
	}

	result, err := mgr.RestoreWithMode(ctx, rec.ID)
	if err != nil {
		t.Fatalf("RestoreWithMode: %v", err)
	}

	if runtime.created != runtimeAfterSpawn {
		t.Errorf("restore created a terminal runtime for a chat session")
	}
	if len(launcher.started) != startsAfterSpawn+1 {
		t.Fatalf("restore did not start a controller: %d starts", len(launcher.started))
	}
	resumed := launcher.started[len(launcher.started)-1]
	if resumed.ProviderConversationID != stored.Metadata.ProviderConversationID {
		t.Errorf("restore passed provider conversation %q, want the stored %q — without it this is a new conversation, not a resume",
			resumed.ProviderConversationID, stored.Metadata.ProviderConversationID)
	}
	// The provider still holds the history, so continuity is native rather than a
	// replayed prompt.
	if result.Mode != RestoreModeNative {
		t.Errorf("restore mode = %q, want native", result.Mode)
	}
}

// `ao send` and orchestrator-to-worker relay both go through Manager.Send. A chat
// session has no pane to type into, so without a mode branch the send reached the
// runtime guard and was refused as "missing runtime handles" — which is true of
// the handles and wrong about the session, and left chat workers unreachable by
// AO's own automation.
func TestSendRoutesIntoTheChatConversation(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, _, runtime := newChatManager(launcher)
	ctx := context.Background()

	rec, _, _, err := mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		Prompt:        "initial brief",
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	if err := mgr.Send(ctx, rec.ID, "relayed from an orchestrator"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(launcher.relayed) != 1 || launcher.relayed[0] != "relayed from an orchestrator" {
		t.Fatalf("relayed = %v, want the message routed to the conversation", launcher.relayed)
	}
	// The initial prompt is a different thing and must not be conflated with it.
	if len(launcher.turns) != 1 || launcher.turns[0] != "initial brief" {
		t.Errorf("initial prompt turns = %v", launcher.turns)
	}
	// The terminal path is not merely unused, it is unreachable here: no runtime
	// was ever created for this session, so a send that fell through would have
	// been refused by the runtime guard instead of returning nil above.
	if runtime.created != 0 {
		t.Errorf("chat spawn created %d runtimes", runtime.created)
	}
}

// A terminated chat session cannot receive a message, matching the terminal path.
// The controller is gone; accepting the send would record a message nothing will
// ever deliver.
func TestSendRefusedForTerminatedChatSession(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, _, _ := newChatManager(launcher)
	ctx := context.Background()

	rec, _, _, err := mgr.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     chatTestProject,
		Kind:          domain.KindWorker,
		Harness:       domain.HarnessCodex,
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := mgr.Kill(ctx, rec.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	err = mgr.Send(ctx, rec.ID, "too late")
	if !errors.Is(err, ErrTerminated) {
		t.Fatalf("err = %v, want ErrTerminated", err)
	}
	if len(launcher.relayed) != 0 {
		t.Errorf("a terminated session still received %v", launcher.relayed)
	}
}

// The gate has to be on the SPAWN path, not only on relaunch.
//
// This is the test the previous one should have been. A unit test of
// requireChatModeAllowed passes whether or not anything calls it, which is
// exactly how the hole survived: the helper was right, relaunch called it, and
// a read-only role still started in chat on the first try. Only a live spawn
// found it. So this drives Spawn and asserts nothing durable was created.
func TestChatSpawnRefusesAReadOnlyRole(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, runtime := newChatManager(launcher)

	roleMap := domain.RoleMap{
		SchemaVersion:    domain.RoleMapSchemaVersion,
		StrictDelegation: true,
		OrchestratorRole: "orchestrator",
		Roles: map[string]domain.RoleBinding{
			"orchestrator": {Harness: domain.HarnessCodex, Template: "orchestrator",
				Permissions: domain.RoleExecutionPolicy{CanSpawn: true}},
			"reviewer": {Harness: domain.HarnessCodex, Template: "reviewer",
				Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: false}},
		},
	}
	project := st.projects["mer"]
	project.Config.RoleMap = roleMap
	st.projects["mer"] = project

	dir := t.TempDir()
	for _, name := range []string{"orchestrator", "reviewer"} {
		body := "---\nid: " + name + "\nname: " + name + "\nroleReminder: r\n---\n# body\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	before := len(st.sessions)
	_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:     "mer",
		Kind:          domain.KindWorker,
		RoleID:        "reviewer",
		Prompt:        "review it",
		RequestedMode: domain.SessionModeChat,
	})
	if !errors.Is(err, ErrChatModeReadOnlyUnsupported) {
		t.Fatalf("Spawn = %v, want ErrChatModeReadOnlyUnsupported", err)
	}
	// Refused BEFORE anything durable: no row, no worktree, no controller. A
	// request AO cannot honour must cost nothing.
	if len(st.sessions) != before {
		t.Errorf("a refused chat spawn still created a session row")
	}
	if len(launcher.started) != 0 {
		t.Errorf("a refused chat spawn still started a controller")
	}
	if runtime.created != 0 {
		t.Errorf("a refused chat spawn still created a runtime")
	}
	// Nor a role template artifact. The CAS write is harmless residue on its
	// own — content-addressed and reused by the next spawn of the same template
	// — but "refused before anything durable" should be true rather than nearly
	// true, so the gate sits above it.
	if len(st.artifacts) != 0 {
		t.Errorf("a refused chat spawn persisted %d template artifact(s)", len(st.artifacts))
	}
}

// A chat spawn that names no harness must preflight the PROJECT's configured
// agent, not an empty string.
//
// Moving the mode preflight above the role template CAS also moved it above
// effectiveHarness, which is what fills in the project default. Every valid
// non-role chat spawn — the ordinary case, where the user picked a project and
// typed a task — then asked the adapter about harness "" and was refused. The
// preflight has to come after the harness is known and validated, and still
// before anything durable is written; this pins the harness it actually asks
// about.
func TestChatSpawnPreflightsTheProjectDefaultHarness(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, _ := newChatManager(launcher)

	project := st.projects["mer"]
	project.Config.Worker = domain.RoleOverride{Harness: domain.HarnessCodex}
	st.projects["mer"] = project

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		// No Harness and no RoleID: the project default is the whole point.
		Prompt:        "do the thing",
		RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if len(launcher.preflighted) != 1 {
		t.Fatalf("preflighted %v, want exactly one harness", launcher.preflighted)
	}
	if launcher.preflighted[0] != domain.HarnessCodex {
		t.Fatalf("preflighted %q, want the project default %q — an empty harness means "+
			"preflight ran before effectiveHarness", launcher.preflighted[0], domain.HarnessCodex)
	}
	if rec.Harness != domain.HarnessCodex {
		t.Fatalf("session harness = %q, want %q", rec.Harness, domain.HarnessCodex)
	}
}

// The pause fence has to cover Chat, not only the terminal pane.
//
// sendChat short-circuits above the messenger, and the messenger is where
// sessionguard lives — so an AO-initiated write to a paused CHAT session went
// through while the identical write to a paused TUI session was suppressed. The
// transition-message outbox is exactly such a writer. Found by pausing one
// session of each mode on a live daemon and sending to both.
//
// The rule is the guard's, unchanged: AO's own writes are refused while paused,
// a human's turn is not. Pause stops AO acting on its own; it does not stop a
// person talking to their agent.
func TestChatPauseFencesAutomaticWritesButNotHumanTurns(t *testing.T) {
	for _, tc := range []struct {
		name       string
		origin     sendOrigin
		wantRelays int
		why        string
	}{
		{"AO's own relay", sendOriginAuto, 0,
			"an automatic write to a paused session is the thing pause exists to stop"},
		{"a person's turn", sendOriginUser, 1,
			"pause fences AO, not the human who paused it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher := &recordingLauncher{}
			mgr, st, _ := newChatManager(launcher)

			rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Prompt: "start", RequestedMode: domain.SessionModeChat,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			paused := st.sessions[rec.ID]
			paused.Metadata.Pause = &domain.SessionPause{
				IncidentID: "limit-1", Reason: domain.PauseReasonOperator,
				DetectedBy: domain.PauseDetectionOperator, PausedAt: time.Now().UTC(),
			}
			st.sessions[rec.ID] = paused

			before := len(launcher.relayed)
			if err := mgr.send(context.Background(), rec.ID, "message", "", tc.origin); err != nil {
				t.Fatalf("send: %v", err)
			}
			if got := len(launcher.relayed) - before; got != tc.wantRelays {
				t.Fatalf("relayed %d turn(s) to a paused chat session, want %d — %s", got, tc.wantRelays, tc.why)
			}
		})
	}
}

// Restart must work on a chat session, because Restart is half of the pause
// contract's two controls and on the paused-dead cell it is the only way back.
//
// ResumeAgent required a runtime handle, which a chat session has BY DESIGN
// never had — no pane, nothing to reattach — so every chat restart answered
// "missing runtime or workspace handles". Found live: on a paused-dead chat
// session, Resume lifted the pause and correctly left it exited, and Restart
// then returned 409, leaving no way to bring the agent back.
func TestResumeAgentRestartsAChatSessionWithNoRuntimeHandle(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, runtime := newChatManager(launcher)

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "start", RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The paused-dead cell after Resume: the pause is lifted, the controller is
	// gone, and the session still has no runtime handle.
	dead := st.sessions[rec.ID]
	dead.Activity.State = domain.ActivityExited
	dead.Metadata.RuntimeHandleID = ""
	st.sessions[rec.ID] = dead

	startsBefore := len(launcher.started)
	if _, err := mgr.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("ResumeAgent on a chat session: %v", err)
	}
	if got := len(launcher.started) - startsBefore; got != 1 {
		t.Fatalf("started %d chat controller(s), want 1", got)
	}
	if runtime.created != 0 {
		t.Errorf("restarting a chat session created a terminal runtime")
	}
}

// The pause fence must hold for BOTH controllers, driven from the same origin.
//
// The first version of this fix threaded the origin into sendChat and stopped
// there — so chat was fenced and the terminal path, which always called
// sessionguard's USER-origin Deliver, was not. An automatic write to a paused
// TUI session still went through. The two branches are one rule, so they are
// tested as one table.
func TestPauseFenceHoldsForBothControllers(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		for _, tc := range []struct {
			name    string
			origin  sendOrigin
			wantOut bool
		}{
			{"AO's own write", sendOriginAuto, false},
			{"a person's turn", sendOriginUser, true},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				launcher := &recordingLauncher{}
				mgr, st, _ := newChatManager(launcher)
				messenger := &fakeMessenger{}
				mgr.messenger = sessionguard.New(st, messenger, mgr.logger)

				rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
					ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
					Prompt: "start", RequestedMode: mode,
				})
				if err != nil {
					t.Fatalf("Spawn: %v", err)
				}
				paused := st.sessions[rec.ID]
				paused.Activity.State = domain.ActivityIdle
				paused.Metadata.Pause = &domain.SessionPause{
					IncidentID: "limit-1", Reason: domain.PauseReasonOperator,
					DetectedBy: domain.PauseDetectionOperator, PausedAt: time.Now().UTC(),
				}
				st.sessions[rec.ID] = paused

				chatBefore, tuiBefore := len(launcher.relayed), len(messenger.msgs)
				if err := mgr.send(context.Background(), rec.ID, "message", "", tc.origin); err != nil {
					t.Fatalf("send: %v", err)
				}
				got := (len(launcher.relayed)-chatBefore)+(len(messenger.msgs)-tuiBefore) > 0
				if got != tc.wantOut {
					t.Fatalf("%s write to a paused %s session delivered=%v, want %v",
						tc.name, mode, got, tc.wantOut)
				}
			})
		}
	}
}

// sendChat must claim only what it owns, and in sessionguard's order: a paused
// TUI record has to fall through to the terminal path, and termination has to
// outrank pause rather than being reported as a successful suppression.
func TestSendChatSuppressionOrderMatchesTheGuard(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, _ := newChatManager(launcher)

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "start", RequestedMode: domain.SessionModeTUI,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	tui := st.sessions[rec.ID]
	tui.Metadata.Pause = &domain.SessionPause{
		IncidentID: "limit-1", Reason: domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator, PausedAt: time.Now().UTC(),
	}
	st.sessions[rec.ID] = tui
	if handled, _ := mgr.sendChat(context.Background(), rec.ID, "m", "", sendOriginAuto); handled {
		t.Error("sendChat claimed a paused TUI session; it never reaches the terminal path")
	}

	chatRec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "start", RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	both := st.sessions[chatRec.ID]
	both.IsTerminated = true
	both.Metadata.Pause = &domain.SessionPause{
		IncidentID: "limit-2", Reason: domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator, PausedAt: time.Now().UTC(),
	}
	st.sessions[chatRec.ID] = both
	// Terminated AND paused: the guard pins termination as the answer, because
	// a terminated session cannot receive a message under any policy and
	// "suppressed, fine" would hide that behind a pause.
	_, err = func() (bool, error) { return mgr.sendChat(context.Background(), chatRec.ID, "m", "", sendOriginAuto) }()
	if !errors.Is(err, ErrTerminated) {
		t.Fatalf("terminated+paused chat send = %v, want ErrTerminated", err)
	}
}

// A scratch project's sessions have no branch, by design. The runtime-handle
// exemption alone still left a scratch chat session unrestartable, because the
// branch requirement was unconditional.
func TestResumeAgentRestartsABranchlessScratchChatSession(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, runtime := newChatManager(launcher)

	project := st.projects["mer"]
	project.Kind = domain.ProjectKindScratch
	st.projects["mer"] = project

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "start", RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	dead := st.sessions[rec.ID]
	dead.Activity.State = domain.ActivityExited
	dead.Metadata.RuntimeHandleID = ""
	dead.Metadata.Branch = "" // scratch: no branch, and none is expected
	st.sessions[rec.ID] = dead

	before := len(launcher.started)
	if _, err := mgr.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatalf("ResumeAgent on a branchless scratch chat session: %v", err)
	}
	if len(launcher.started)-before != 1 {
		t.Fatal("no chat controller was started")
	}
	if runtime.created != 0 {
		t.Error("restarting a chat session created a terminal runtime")
	}
}

// The switch/fresh saga must refuse a chat session explicitly, before it stops
// anything.
//
// It was already refused — but only because the saga demands a runtime handle
// a chat session has never had. That is the same precondition Restart had to
// exempt to work at all, so "correct" here rested on a rule the next fix was
// going to relax. The saga stops a tmux runtime, probes it for liveness, and
// reads an empty handle as confirmed death; none of that describes a chat
// controller.
func TestSwitchAndFreshRefuseChatSessionsBeforeStoppingAnything(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Manager, domain.SessionID) error
	}{
		{"cross-harness switch", func(m *Manager, id domain.SessionID) error {
			_, err := m.SwitchWorker(context.Background(), SwitchRequest{
				SessionID: id, TargetHarness: domain.HarnessClaudeCode,
			})
			return err
		}},
		{"fresh conversation", func(m *Manager, id domain.SessionID) error {
			_, err := m.FreshConversation(context.Background(), id, domain.SemanticHandoffV1{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher := &recordingLauncher{}
			mgr, st, runtime := newChatManager(launcher)

			rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Prompt: "start", RequestedMode: domain.SessionModeChat,
			})
			if err != nil {
				t.Fatalf("Spawn: %v", err)
			}

			if err := tc.call(mgr, rec.ID); !errors.Is(err, ErrSwitchChatUnsupported) {
				t.Fatalf("%s = %v, want ErrSwitchChatUnsupported", tc.name, err)
			}
			// Nothing stopped, nothing terminated, mode untouched.
			if len(launcher.stopped) != 0 {
				t.Errorf("the saga stopped the chat controller before refusing")
			}
			if runtime.destroyed != 0 {
				t.Errorf("the saga destroyed a runtime for a chat session")
			}
			after := st.sessions[rec.ID]
			if after.IsTerminated || domain.NormalizeSessionMode(after.Mode) != domain.SessionModeChat {
				t.Errorf("session after refusal: terminated=%v mode=%s", after.IsTerminated, after.Mode)
			}
		})
	}
}

// Recovery re-enters the saga, so it re-applies the saga's refusals. A chat row
// with a pending switch should be unreachable now that both sagas share a
// fence, but "should be unreachable" is exactly what recovery exists to
// disbelieve — its job is states nobody meant to create. Without the check it
// walks into terminal-oriented probing and finishSwitchTarget with an empty
// runtime handle, which this saga reads as confirmed death.
func TestRecoverSwitchRefusesAChatSessionWithoutTouchingAnything(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, st, runtime := newChatManager(launcher)

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Prompt: "start", RequestedMode: domain.SessionModeChat,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// The corrupt/legacy shape: chat, with a switch pending against it.
	corrupt := st.sessions[rec.ID]
	corrupt.Metadata.SwitchPending = &domain.SwitchPending{GenerationID: "gen-1"}
	st.sessions[rec.ID] = corrupt

	stopsBefore, startsBefore := len(launcher.stopped), len(launcher.started)
	if _, err := mgr.RecoverSwitchFromPostStop(context.Background(), rec.ID); !errors.Is(err, ErrSwitchChatUnsupported) {
		t.Fatalf("RecoverSwitchFromPostStop = %v, want ErrSwitchChatUnsupported", err)
	}
	if len(launcher.stopped) != stopsBefore || len(launcher.started) != startsBefore {
		t.Errorf("recovery touched the chat controller: stops %d->%d starts %d->%d",
			stopsBefore, len(launcher.stopped), startsBefore, len(launcher.started))
	}
	if runtime.created != 0 || runtime.destroyed != 0 {
		t.Errorf("recovery touched a terminal runtime: created=%d destroyed=%d", runtime.created, runtime.destroyed)
	}
	// And the pending pin is untouched: refusing is not resolving.
	if after := st.sessions[rec.ID]; after.Metadata.SwitchPending == nil {
		t.Error("recovery cleared the pending pin it refused to act on")
	}
}

// Both chat cleanup branches must preserve BOTH failures.
//
// The compensating write is what makes a failed launch safe to boot on: if
// MarkTerminated fails, an active row survives for a session that does not
// exist, and the next boot adopts it. markSpawnFailedTerminated reports that as
// ErrLaunchCleanupUnresolved, which is an ErrBootUnsafe — and upstream's chat
// path discarded the return value, so the boot-safety signal was lost on both
// branches while the original error travelled alone. Only lint caught it.
//
// The original error has to survive too: "the turn was refused" and "and then
// cleanup failed" are different facts and the caller needs both.
func TestChatSpawnCleanupPreservesBothFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		arrange func(*fakeStore, *fakeLCM, *recordingLauncher)
		wantErr string
	}{
		{
			name: "MarkSpawned fails, then termination fails",
			arrange: func(_ *fakeStore, lcm *fakeLCM, _ *recordingLauncher) {
				lcm.markSpawnedErr = errors.New("adoption rejected")
				lcm.markTerminatedErr = errors.New("compensating write rejected")
			},
			wantErr: "adoption rejected",
		},
		{
			name: "the initial turn fails, then termination fails",
			arrange: func(_ *fakeStore, lcm *fakeLCM, l *recordingLauncher) {
				l.turnErr = errors.New("provider refused the turn")
				lcm.markTerminatedErr = errors.New("compensating write rejected")
			},
			wantErr: "provider refused the turn",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			launcher := &recordingLauncher{}
			mgr, st, _ := newChatManager(launcher)
			lcm := &fakeLCM{store: st}
			mgr.lcm = lcm
			tc.arrange(st, lcm, launcher)

			_, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
				ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
				Prompt: "start", RequestedMode: domain.SessionModeChat,
			})
			if err == nil {
				t.Fatal("Spawn succeeded despite an injected failure")
			}
			// The original cause, not replaced by the cleanup failure.
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error lost the original cause %q: %v", tc.wantErr, err)
			}
			// And the boot-safety identity, which is the one lint was guarding.
			if !errors.Is(err, ErrLaunchCleanupUnresolved) {
				t.Errorf("error does not carry ErrLaunchCleanupUnresolved: %v", err)
			}
			if !errors.Is(err, ErrBootUnsafe) {
				t.Errorf("ErrLaunchCleanupUnresolved no longer implies ErrBootUnsafe: %v", err)
			}
			// The residue the signal is about: a live row for a session that
			// does not exist. Asserted so the test fails if the fixture ever
			// stops reproducing the condition.
			active := 0
			for _, rec := range st.sessions {
				if !rec.IsTerminated {
					active++
				}
			}
			if active == 0 {
				t.Error("no active row survived, so this fixture no longer exercises unresolved cleanup")
			}
		})
	}
}
