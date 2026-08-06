package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Boot restore is the last ungated path that can create a second active
// orchestrator for a project. Migration 0057 reconciles rows that are ALREADY
// duplicated; these tests cover the loop that could produce them — and the
// order matters, because workspace.Restore adopts the shared canonical worktree
// before any row flips, so the damage lands before the unique index is reached.

// savedOrchestrator adds a terminated orchestrator carrying a shutdown-saved
// marker: the state RestoreAll acts on.
func savedOrchestrator(st *fakeStore, id domain.SessionID, project domain.ProjectID, created time.Time) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: project, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		IsTerminated: true, CreatedAt: created, UpdatedAt: created,
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/" + string(project) + "/orchestrator",
			Branch:        "ao/" + string(project) + "-orchestrator",
			Prompt:        "coordinate",
		},
	}
	st.worktrees[id] = []domain.SessionWorktreeRecord{{
		SessionID: id, RepoName: domain.RootWorkspaceRepoName,
		WorktreePath: "/ws/" + string(project) + "/orchestrator",
		Branch:       "ao/" + string(project) + "-orchestrator", State: "removed",
	}}
}

func restoreAllHarness(t *testing.T) (*Manager, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{
		Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}},
		Agents:  singleAgent{agent: supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex"}}}},
		// A shared path mirrors production: every orchestrator for a project
		// restores into the SAME canonical worktree.
		Workspace: &fakeWorkspace{path: "/ws/mer/orchestrator"}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle:   &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	return m, st
}

func activeIDs(st *fakeStore, kind domain.SessionKind) []domain.SessionID {
	var out []domain.SessionID
	for id, rec := range st.sessions {
		if rec.Kind == kind && !rec.IsTerminated {
			out = append(out, id)
		}
	}
	return out
}

// TestRestoreAll_RestoresOnlyTheSurvivingOrchestrator is the core regression.
// Two terminated orchestrators both carrying markers would each have been
// restored, giving one project two live orchestrators sharing one worktree —
// the exact state migration 0057 exists to clean up after.
func TestRestoreAll_RestoresOnlyTheSurvivingOrchestrator(t *testing.T) {
	m, st := restoreAllHarness(t)
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	savedOrchestrator(st, "mer-1", "mer", older)
	savedOrchestrator(st, "mer-2", "mer", newer)

	if err := m.RestoreAll(context.Background()); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	live := activeIDs(st, domain.KindOrchestrator)
	if len(live) != 1 {
		t.Fatalf("live orchestrators = %v, want exactly 1: two would share the canonical worktree", live)
	}
	// Same rule as newestOrchestratorRecord and migration 0057: newest wins.
	if live[0] != "mer-2" {
		t.Errorf("survivor = %s, want mer-2 (newest CreatedAt)", live[0])
	}
	// The loser's marker must be gone, or every later boot retries it.
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Errorf("loser kept its restore marker (%d rows): boot would retry it forever", len(rows))
	}
}

// TestRestoreAll_DropsSavedOrchestratorWhenOneIsAlreadyLive: Reconcile's adopt
// pass runs BEFORE RestoreAll, so a crash-surviving orchestrator is already
// active by the time this loop runs. Restoring a saved one on top of it is the
// same duplicate, reached by a different route.
func TestRestoreAll_DropsSavedOrchestratorWhenOneIsAlreadyLive(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))
	// mer-2 is already live — adopted moments ago.
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		IsTerminated: false, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{
			WorkspacePath: "/ws/mer/orchestrator", Branch: "ao/mer-orchestrator",
			RuntimeHandleID: "tmux-mer-2",
		},
	}

	if err := m.RestoreAll(context.Background()); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}

	live := activeIDs(st, domain.KindOrchestrator)
	if len(live) != 1 || live[0] != "mer-2" {
		t.Fatalf("live orchestrators = %v, want only the already-live mer-2", live)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("the saved orchestrator was restored on top of a live owner")
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Errorf("dropped candidate kept its marker (%d rows)", len(rows))
	}
}

// TestBootUnsafeChildren pins the membership the daemon gate depends on.
//
// daemon.go checks ErrBootUnsafe and nothing else, which makes each leaf's
// wrapping load-bearing rather than cosmetic: unwrap one and the daemon keeps
// compiling, keeps checking the parent, and silently stops treating that
// condition as fatal. Nothing else in the suite would notice, because every
// other test asserts on the leaf it cares about.
func TestBootUnsafeChildren(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"launch cleanup", ErrLaunchCleanupUnresolved},
		{"restore marker", ErrRestoreMarkerUnresolved},
		{"orchestrator evidence", ErrOrchestratorEvidenceUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, ErrBootUnsafe) {
				t.Fatalf("%v does not match ErrBootUnsafe: the daemon gate keys on the parent, "+
					"so this condition would no longer stop boot", tc.err)
			}
			// And still matchable as itself, so callers can distinguish causes.
			wrapped := fmt.Errorf("context: %w", tc.err)
			if !errors.Is(wrapped, tc.err) || !errors.Is(wrapped, ErrBootUnsafe) {
				t.Fatalf("%v lost identity or parentage when wrapped", tc.err)
			}
		})
	}

	// Negative control: the assertion above must not pass for everything.
	for _, err := range []error{ErrNotFound, ErrTerminated, ErrNotResumable} {
		if errors.Is(err, ErrBootUnsafe) {
			t.Errorf("%v matches ErrBootUnsafe: an ordinary failure would abort boot", err)
		}
	}
}

// TestRestoreAll_AbortsElectionWhenAMarkerCannotBeRead is the fail-closed rule
// for incomplete evidence.
//
// "Marker absent" and "marker lookup failed" are different facts. Collapsing
// them lets a failed read on the NEWEST candidate silently promote an older
// one, which then adopts the shared canonical worktree and replays older
// preserved state. Unlike a duplicate row, that is not something a later boot
// corrects — the older state has already been written into the worktree the
// survivor owns. So the whole project's election is abandoned instead.
func TestRestoreAll_AbortsElectionWhenAMarkerCannotBeRead(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-2*time.Hour))
	savedOrchestrator(st, "mer-2", "mer", time.Now().Add(-time.Hour))
	// The NEWEST candidate's marker is unreadable — the dangerous direction.
	st.worktreeListErr["mer-2"] = errors.New("database is locked")

	err := m.RestoreAll(context.Background())
	if !errors.Is(err, ErrOrchestratorEvidenceUnresolved) {
		t.Fatalf("RestoreAll = %v, want ErrOrchestratorEvidenceUnresolved", err)
	}
	// Boot must refuse to serve: the unread marker may belong to a predecessor
	// that should have been neutralized, and it stays eligible.
	if !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("err = %v, want it marked ErrBootUnsafe", err)
	}

	if live := activeIDs(st, domain.KindOrchestrator); len(live) != 0 {
		t.Fatalf("restored %v from incomplete evidence: the newest candidate's marker was unreadable, "+
			"so an older orchestrator would have adopted the canonical worktree", live)
	}
	// And nothing was neutralized: the next boot must decide from a full read.
	if rows := st.worktrees["mer-1"]; len(rows) == 0 {
		t.Error("an abandoned election still neutralized a marker; the next boot has lost a candidate")
	}
}

// TestRestoreAll_UnreadableMarkerOnAnOlderCandidateAlsoAborts: the rule is
// about evidence, not about which row happened to fail. A read failure anywhere
// in the candidate set means the set is unknown.
func TestRestoreAll_UnreadableMarkerOnAnOlderCandidateAlsoAborts(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-2*time.Hour))
	savedOrchestrator(st, "mer-2", "mer", time.Now().Add(-time.Hour))
	st.worktreeListErr["mer-1"] = errors.New("database is locked")

	if err := m.RestoreAll(context.Background()); !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("RestoreAll = %v, want ErrBootUnsafe", err)
	}
	if live := activeIDs(st, domain.KindOrchestrator); len(live) != 0 {
		t.Fatalf("restored %v while a candidate's marker was unreadable", live)
	}
}

// TestRestoreAll_UnreadableProjectSessionsIsBootFatal covers the other place
// evidence can go missing: the under-gate session read itself. Without it there
// may be a predecessor we never even enumerated, which is strictly less
// information than an unreadable marker, so it cannot be treated more leniently.
func TestRestoreAll_UnreadableProjectSessionsIsBootFatal(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))
	st.listSessionsErr = errors.New("database is locked")

	err := m.RestoreAll(context.Background())
	if !errors.Is(err, ErrOrchestratorEvidenceUnresolved) {
		t.Fatalf("RestoreAll = %v, want ErrOrchestratorEvidenceUnresolved", err)
	}
	if !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("err = %v, want it marked ErrBootUnsafe", err)
	}
}

// TestRestoreAll_FailedLoserNeutralizationIsBootFatal is the durability rule.
//
// A surviving loser marker is not inert and does not stay a loser: once the
// winner is killed — or loses its own marker — that stale row becomes the only
// restorable orchestrator and a later boot resurrects the session this election
// superseded. So neutralization is a precondition of restoring the winner, and
// its failure is boot-fatal rather than logged.
func TestRestoreAll_FailedLoserNeutralizationIsBootFatal(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-2*time.Hour))
	savedOrchestrator(st, "mer-2", "mer", time.Now().Add(-time.Hour))
	st.worktreeDeleteErr["mer-1"] = errors.New("disk full") // the loser

	err := m.RestoreAll(context.Background())
	if !errors.Is(err, ErrRestoreMarkerUnresolved) {
		t.Fatalf("RestoreAll = %v, want ErrRestoreMarkerUnresolved", err)
	}
	// Boot must refuse to serve, not merely log.
	if !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("err = %v, want it marked ErrBootUnsafe so the daemon gate catches it", err)
	}
	// The winner must NOT have been restored: doing so alongside a live loser
	// marker is exactly the resurrection this guards.
	if live := activeIDs(st, domain.KindOrchestrator); len(live) != 0 {
		t.Fatalf("restored %v while a loser marker survived", live)
	}
}

// TestRestoreAll_FailedNeutralizationUnderALiveOwnerIsAlsoFatal covers the
// other neutralization site: candidates displaced by an already-live owner.
func TestRestoreAll_FailedNeutralizationUnderALiveOwnerIsAlsoFatal(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		IsTerminated: false, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator", RuntimeHandleID: "tmux-mer-2"},
	}
	st.worktreeDeleteErr["mer-1"] = errors.New("disk full")

	if err := m.RestoreAll(context.Background()); !errors.Is(err, ErrBootUnsafe) {
		t.Fatalf("RestoreAll = %v, want ErrBootUnsafe: the displaced candidate's marker survived", err)
	}
}

// TestRestoreAll_ElectsFromTheUnderGateRead: the pre-gate snapshot may name a
// project whose rows have since changed. Election and relaunch must use the
// records read UNDER the gate, so a row terminated-and-retired (or newly
// active) in between is honoured rather than acted on from a stale copy.
func TestRestoreAll_ElectsFromTheUnderGateRead(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))

	// The pre-gate snapshot sees mer-1 as the only candidate. Between that read
	// and the gate, a newer orchestrator lands and wins.
	snapshot, err := st.ListAllSessions(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	savedOrchestrator(st, "mer-9", "mer", time.Now())

	if errs := m.restoreProjectOrchestrators(context.Background(), snapshot); len(errs) != 0 {
		t.Fatalf("restoreProjectOrchestrators: %v", errs)
	}

	live := activeIDs(st, domain.KindOrchestrator)
	if len(live) != 1 || live[0] != "mer-9" {
		t.Fatalf("live = %v, want mer-9: the election used the stale pre-gate snapshot", live)
	}
	if rows := st.worktrees["mer-1"]; len(rows) != 0 {
		t.Errorf("the loser discovered only under the gate kept its marker")
	}
}

// TestRestoreAll_OrchestratorRestoreTakesTheProjectGate proves the restore runs
// UNDER the ownership gate rather than beside it. Holding the gate must block
// the restore; releasing it must let the restore complete.
func TestRestoreAll_OrchestratorRestoreTakesTheProjectGate(t *testing.T) {
	m, st := restoreAllHarness(t)
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))

	release, err := m.acquireProjectOwnership(context.Background(), "mer")
	if err != nil {
		t.Fatalf("pre-acquire gate: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- m.RestoreAll(context.Background()) }()

	select {
	case <-done:
		t.Fatal("RestoreAll restored the orchestrator while the project gate was held by someone else")
	case <-time.After(150 * time.Millisecond):
		// Correctly blocked.
	}

	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RestoreAll after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RestoreAll never completed after the gate was released")
	}
	if live := activeIDs(st, domain.KindOrchestrator); len(live) != 1 {
		t.Fatalf("live orchestrators = %v, want 1 once the gate freed", live)
	}
}

// TestRestoreAll_WorkersAreUnaffectedByOrchestratorGating: the cardinality rule
// is orchestrator-only. Workers have no per-project limit and must all restore,
// or gating would silently halve a project's session set.
func TestRestoreAll_WorkersAreUnaffectedByOrchestratorGating(t *testing.T) {
	m, st := restoreAllHarness(t)
	for _, id := range []domain.SessionID{"mer-1", "mer-2", "mer-3"} {
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
			IsTerminated: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			Metadata: domain.SessionMetadata{
				WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id),
				AgentSessionID: "agent-x", Prompt: "work",
			},
		}
		st.worktrees[id] = []domain.SessionWorktreeRecord{{
			SessionID: id, RepoName: domain.RootWorkspaceRepoName,
			WorktreePath: "/ws/" + string(id), Branch: "ao/" + string(id), State: "removed",
		}}
	}

	if err := m.RestoreAll(context.Background()); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
	if live := activeIDs(st, domain.KindWorker); len(live) != 3 {
		t.Fatalf("restored workers = %v, want all 3", live)
	}
}

// TestRestoreAll_OrchestratorPathStillPropagatesUnresolvedCleanup: routing
// orchestrators through the gated path must not swallow the one error boot
// refuses to serve on.
func TestRestoreAll_OrchestratorPathStillPropagatesUnresolvedCleanup(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	savedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour))
	rt := &stubbornRuntime{
		fakeRuntime: &fakeRuntime{aliveByHandle: map[string]bool{"h1": true}},
		stubbornID:  "h1",
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex"}}}},
		Workspace: &fakeWorkspace{path: "/ws/mer/orchestrator"}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle:   &fakeLCM{store: st},
		DataDir:     t.TempDir(),
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-new" },
	})
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")

	err := m.RestoreAll(context.Background())
	if !errors.Is(err, ErrLaunchCleanupUnresolved) {
		t.Fatalf("RestoreAll = %v, want ErrLaunchCleanupUnresolved through the gated orchestrator path", err)
	}
}

// TestActiveOrchestratorSessionID_UsesTheSurvivorRule pins the resolver
// collapse. ListSessions order is insertion order, so first-match returned the
// OLDEST active orchestrator while EnsureOrchestrator owned the newest — every
// worker spawned in that window was told to report to the wrong coordinator.
func TestActiveOrchestratorSessionID_UsesTheSurvivorRule(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	older := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	for id, created := range map[domain.SessionID]time.Time{"mer-1": older, "mer-2": newer} {
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindOrchestrator,
			IsTerminated: false, CreatedAt: created, UpdatedAt: created,
		}
	}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})

	got, ok, err := m.activeOrchestratorSessionID(context.Background(), "mer")
	if err != nil || !ok {
		t.Fatalf("resolve owner: %v ok=%v", err, ok)
	}
	if got != "mer-2" {
		t.Fatalf("owner = %s, want mer-2 — the same survivor rule EnsureOrchestrator and 0046 apply", got)
	}
	// And it must agree with the shared helper, not merely happen to match.
	want := newestOrchestratorRecord([]domain.SessionRecord{st.sessions["mer-1"], st.sessions["mer-2"]}).ID
	if got != want {
		t.Fatalf("resolver = %s but newestOrchestratorRecord = %s: the two rules have diverged again", got, want)
	}
}

// TestRestoreAll_ConcurrentProjectsDoNotSerialize: the gate is per project, so
// two projects restoring at once must not block each other.
func TestRestoreAll_ConcurrentProjectsDoNotSerialize(t *testing.T) {
	m, st := restoreAllHarness(t)
	st.projects["other"] = domain.ProjectRecord{ID: "other", Config: testRoleAgents()}
	savedOrchestrator(st, "mer-1", "mer", time.Now())
	savedOrchestrator(st, "other-1", "other", time.Now())

	// Hold only "mer": "other" must still restore.
	release, err := m.acquireProjectOwnership(context.Background(), "mer")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := m.restoreOneOrchestrator(context.Background(), "other"); err != nil {
			t.Errorf("restore other: %v", err)
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a different project's restore blocked on \"mer\"'s gate: the gate is not per project")
	}
	release()
}
