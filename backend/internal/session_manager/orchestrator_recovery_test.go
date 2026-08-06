package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 2B-2. "Never zero orchestrators" is not achievable: the canonical worktree
// must be released before a successor can create it, so a spawn failure
// necessarily leaves a zero-owner interval. The guarantee is that the interval
// is never TERMINAL — intent is persisted before retirement, and boot recovers.

func recoveryHarness(t *testing.T) (*Manager, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	// The fake mints ids as "<project>-<n>" from zero, so a spawned successor
	// would silently OVERWRITE a seeded predecessor and make a broken recovery
	// look correct. Start the counter past anything these tests seed.
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{
		Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}},
		Agents:  singleAgent{agent: &recordingAgent{}},
		// A real path so spawn produces a normal-looking orchestrator row.
		Workspace: &fakeWorkspace{path: "/ws/mer/orchestrator"}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		DataDir:   t.TempDir(),
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st
}

func liveOrchestrators(st *fakeStore) []domain.SessionID {
	var out []domain.SessionID
	for id, rec := range st.sessions {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			out = append(out, id)
		}
	}
	return out
}

// TestEnsureOrchestrator_PersistsIntentBeforeRetiring is the ordering the whole
// guarantee rests on. If retirement happened first, a crash in the window would
// leave a project with no orchestrator and NOTHING recording that it should
// have one.
func TestEnsureOrchestrator_PersistsIntentBeforeRetiring(t *testing.T) {
	m, st := recoveryHarness(t)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", Branch: "ao/mer-orchestrator"},
	}
	// Retirement is reached only if intent was already durable.
	st.intentPutErr = errors.New("disk full")

	_, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true)
	if err == nil {
		t.Fatal("EnsureOrchestrator succeeded despite being unable to record intent")
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Fatal("the predecessor was retired before intent was durable: a crash here strands the project silently")
	}
}

// TestEnsureOrchestrator_RetainsIntentWhenSpawnFails: the zero-owner state is
// exactly what recovery exists for, so the record of it must survive.
func TestEnsureOrchestrator_RetainsIntentWhenSpawnFails(t *testing.T) {
	m, st := recoveryHarness(t)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", Branch: "ao/mer-orchestrator"},
	}
	m.workspace = &fakeWorkspace{createErr: errors.New("no disk")}

	if _, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true); err == nil {
		t.Fatal("expected the spawn to fail")
	}
	if len(liveOrchestrators(st)) != 0 {
		t.Fatal("fixture did not reproduce a zero-owner interval")
	}
	if _, ok := st.intents["mer"]; !ok {
		t.Fatal("intent was discharged despite the project having no orchestrator: nothing will recover it")
	}
}

// TestEnsureOrchestrator_DischargesIntentOnSuccess: a retained intent would
// make every later boot re-check a healthy project.
func TestEnsureOrchestrator_DischargesIntentOnSuccess(t *testing.T) {
	m, st := recoveryHarness(t)
	if _, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true); err != nil {
		t.Fatalf("EnsureOrchestrator: %v", err)
	}
	if _, ok := st.intents["mer"]; ok {
		t.Fatal("intent survived a successful replacement")
	}
}

// TestEnsureOrchestrator_NonCleanSpawnAlsoDischargesIntent: a stranded project
// is usually rescued by an ordinary idempotent spawn, not a clean replacement.
// Only the clean path discharged, so a healthy project kept a durable record
// saying it was still owed an orchestrator until some later boot noticed —
// found by live dogfood, where the intent survived a successful recovery spawn.
func TestEnsureOrchestrator_NonCleanSpawnAlsoDischargesIntent(t *testing.T) {
	t.Run("spawned", func(t *testing.T) {
		m, st := recoveryHarness(t)
		st.intents["mer"] = domain.OrchestratorReplacementIntent{ProjectID: "mer", RequestedAt: time.Now()}

		if _, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, false); err != nil {
			t.Fatalf("EnsureOrchestrator: %v", err)
		}
		if len(liveOrchestrators(st)) != 1 {
			t.Fatal("no orchestrator spawned")
		}
		if _, ok := st.intents["mer"]; ok {
			t.Error("intent survived a successful non-clean spawn: the record contradicts the live state")
		}
	})

	t.Run("reused", func(t *testing.T) {
		m, st := recoveryHarness(t)
		st.sessions["mer-9"] = domain.SessionRecord{
			ID: "mer-9", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator"},
		}
		st.intents["mer"] = domain.OrchestratorReplacementIntent{ProjectID: "mer", RequestedAt: time.Now()}

		res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, false)
		if err != nil || !res.Reused {
			t.Fatalf("EnsureOrchestrator: %v reused=%v", err, res.Reused)
		}
		if _, ok := st.intents["mer"]; ok {
			t.Error("intent survived an idempotent reuse of a live orchestrator")
		}
	})
}

// TestRecoverOrchestratorReplacements_SpawnsForAStrandedProject is the payoff:
// the interval is not terminal.
func TestRecoverOrchestratorReplacements_SpawnsForAStrandedProject(t *testing.T) {
	m, st := recoveryHarness(t)
	st.intents["mer"] = domain.OrchestratorReplacementIntent{
		ProjectID: "mer", RetiredSessionID: "mer-1", RequestedAt: time.Now().Add(-time.Minute),
	}

	if err := m.RecoverOrchestratorReplacements(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if live := liveOrchestrators(st); len(live) != 1 {
		t.Fatalf("live orchestrators = %v, want the project recovered to exactly 1", live)
	}
	if _, ok := st.intents["mer"]; ok {
		t.Error("intent not discharged after a successful recovery")
	}
}

// TestRecoverOrchestratorReplacements_DischargesWhenAlreadyRecovered: Reconcile
// adopts crash-surviving runtimes before this pass, so a project whose
// orchestrator is actually alive must be left alone, not spawned over.
func TestRecoverOrchestratorReplacements_DischargesWhenAlreadyRecovered(t *testing.T) {
	m, st := recoveryHarness(t)
	st.sessions["mer-9"] = domain.SessionRecord{
		ID: "mer-9", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", RuntimeHandleID: "tmux-mer-9"},
	}
	st.intents["mer"] = domain.OrchestratorReplacementIntent{ProjectID: "mer", RequestedAt: time.Now()}

	if err := m.RecoverOrchestratorReplacements(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	live := liveOrchestrators(st)
	if len(live) != 1 || live[0] != "mer-9" {
		t.Fatalf("live = %v, want only the already-live mer-9: recovery spawned over a healthy owner", live)
	}
	if _, ok := st.intents["mer"]; ok {
		t.Error("intent not discharged for an already-recovered project")
	}
}

// TestRecoverOrchestratorReplacements_RepairsHalfRetirementBeforeDeciding is
// the ordering inside recovery, and it is load-bearing on its own.
//
// A retirement interrupted after MarkTerminated but before the claim release
// is harmless here; the dangerous residue is the older ordering's: an ACTIVE
// orchestrator owning no workspace. It reads as "this project has an owner", so
// recovery would discharge the intent and walk away, leaving a phantom
// coordinator that can never be replaced — the project's only slot occupied by
// a session that owns nothing. The repair pass has to run BEFORE the
// does-an-owner-exist question is asked.
func TestRecoverOrchestratorReplacements_RepairsHalfRetirementBeforeDeciding(t *testing.T) {
	m, st := recoveryHarness(t)
	// The half-retired predecessor: active, but owns nothing.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		IsTerminated: false, CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now().Add(-time.Hour),
	}
	st.intents["mer"] = domain.OrchestratorReplacementIntent{
		ProjectID: "mer", RetiredSessionID: "mer-1", RequestedAt: time.Now().Add(-time.Hour),
	}

	if err := m.RecoverOrchestratorReplacements(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}

	if !st.sessions["mer-1"].IsTerminated {
		t.Error("the half-retired row was left active: it still holds the project's only slot")
	}
	live := liveOrchestrators(st)
	if len(live) != 1 {
		t.Fatalf("live orchestrators = %v, want exactly 1 real successor", live)
	}
	if live[0] == "mer-1" {
		t.Fatal("recovery mistook the claimless half-retired row for a healthy owner and " +
			"discharged the intent: the project keeps a coordinator that owns nothing")
	}
	if got := st.sessions[live[0]].Metadata.WorkspacePath; got == "" {
		t.Errorf("the recovered orchestrator owns no workspace either: %q", got)
	}
	if _, ok := st.intents["mer"]; ok {
		t.Error("intent not discharged after a successful recovery")
	}
}

// TestRecoverOrchestratorReplacements_StampsAndRetainsOnFailure: a project that
// cannot be recovered must stay visible and stay owed.
func TestRecoverOrchestratorReplacements_StampsAndRetainsOnFailure(t *testing.T) {
	m, st := recoveryHarness(t)
	st.intents["mer"] = domain.OrchestratorReplacementIntent{ProjectID: "mer", RequestedAt: time.Now()}
	m.workspace = &fakeWorkspace{createErr: errors.New("no disk")}

	if err := m.RecoverOrchestratorReplacements(context.Background()); err == nil {
		t.Fatal("recover returned nil despite failing to spawn")
	}
	in, ok := st.intents["mer"]
	if !ok {
		t.Fatal("intent discharged even though the project still has no orchestrator")
	}
	if in.AttemptCount != 1 || in.LastError == "" {
		t.Errorf("failure not stamped: %+v", in)
	}
}

// TestRecoverOrchestratorReplacements_MissingTableSurfaces: an unreadable
// intent list means the schema is not what this code expects. It is reported
// rather than read as "nothing owed" — but not made boot-fatal, because an
// un-recovered project is inert and stopping an otherwise-healthy daemon is the
// worse outcome.
func TestRecoverOrchestratorReplacements_MissingTableSurfaces(t *testing.T) {
	m, st := recoveryHarness(t)
	st.intentListErr = errors.New("no such table: orchestrator_replacement_intent")

	err := m.RecoverOrchestratorReplacements(context.Background())
	if err == nil {
		t.Fatal("an unreadable intent list must be reported, not treated as empty")
	}
	if errors.Is(err, ErrBootUnsafe) {
		t.Fatal("recovery failure marked boot-unsafe: a project without a coordinator is inert, " +
			"and refusing to boot would punish every other project")
	}
}

// TestFinalizeRetirement_TerminatesBeforeReleasingTheClaim pins the crash
// ordering. Retirement is two writes and cannot be one, so the question is
// which residue a crash leaves. Terminating first leaves a dead row with a
// stale path — an alias already guarded, and cleared at boot. Releasing first
// would leave an ACTIVE orchestrator owning no workspace: it holds the
// project's only slot so no successor can be created, while
// EnsureOrchestrator's idempotent path hands callers an empty shell.
func TestFinalizeRetirement_TerminatesBeforeReleasingTheClaim(t *testing.T) {
	m, st := recoveryHarness(t)
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator"},
	}
	// Fail the SECOND write, so the observable state is the crash residue.
	st.updateErr = errors.New("disk full")
	st.updateFailAfter = st.updateCount + 1

	if err := m.finalizeRetirement(context.Background(), "mer-1"); err == nil {
		t.Fatal("expected the claim release to fail")
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Fatal("a crash mid-retirement left the orchestrator ACTIVE with no workspace: " +
			"it holds the project's only slot and no successor can be created")
	}
}

// TestReconcileOrchestratorRetirement_ClearsBothResidues: whichever half-state
// a crash left, boot repairs it.
func TestReconcileOrchestratorRetirement_ClearsBothResidues(t *testing.T) {
	m, st := recoveryHarness(t)
	// Residue A: terminated but still naming the canonical worktree.
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", Branch: "ao/mer-orchestrator"},
	}
	// Residue B: active but owning nothing.
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: false,
	}

	if err := m.reconcileOrchestratorRetirement(context.Background(), "mer"); err != nil {
		t.Fatalf("reconcile retirement: %v", err)
	}
	if p := st.sessions["mer-1"].Metadata.WorkspacePath; p != "" {
		t.Errorf("terminated orchestrator still aliases the canonical worktree: %q", p)
	}
	if !st.sessions["mer-2"].IsTerminated {
		t.Error("claimless active orchestrator left holding the project's only slot")
	}
}

// TestReconcileOrchestratorRetirement_LeavesHealthyRowsAlone is the control: a
// normal project must survive the repair pass untouched.
func TestReconcileOrchestratorRetirement_LeavesHealthyRowsAlone(t *testing.T) {
	m, st := recoveryHarness(t)
	healthy := domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: false,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", Branch: "ao/mer-orchestrator"},
	}
	st.sessions["mer-1"] = healthy
	// A cleanly retired predecessor: terminated AND already released.
	st.sessions["mer-0"] = domain.SessionRecord{
		ID: "mer-0", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
	}

	if err := m.reconcileOrchestratorRetirement(context.Background(), "mer"); err != nil {
		t.Fatalf("reconcile retirement: %v", err)
	}
	if got := st.sessions["mer-1"]; got.IsTerminated || got.Metadata.WorkspacePath != healthy.Metadata.WorkspacePath {
		t.Fatalf("the live orchestrator was mutated: %+v", got)
	}
}

// TestRecoverOrchestratorReplacements_TakesTheProjectGate: recovery spawns, so
// it must exclude concurrent ownership operations exactly as EnsureOrchestrator
// does.
func TestRecoverOrchestratorReplacements_TakesTheProjectGate(t *testing.T) {
	m, st := recoveryHarness(t)
	st.intents["mer"] = domain.OrchestratorReplacementIntent{ProjectID: "mer", RequestedAt: time.Now()}

	release, err := m.acquireProjectOwnership(context.Background(), "mer")
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- m.RecoverOrchestratorReplacements(context.Background()) }()

	select {
	case <-done:
		t.Fatal("recovery spawned while another owner held the project gate")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery never completed after the gate was released")
	}
}
