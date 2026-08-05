package sessionmanager

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ownershipHarness builds a Manager over the package fakes with one registered
// project per id.
func ownershipHarness(t *testing.T, agent ports.Agent, projects ...domain.ProjectID) (*Manager, *fakeStore, *fakeMessenger) {
	t.Helper()
	st := newFakeStore()
	// fakeStore mints ids as "<project>-<n>" from a counter starting at 0, which
	// would collide with the literal ids these tests seed and let a successor
	// silently overwrite a row it just retired. Start the counter past them.
	st.num = 100
	for _, p := range projects {
		st.projects[string(p)] = domain.ProjectRecord{ID: string(p), Config: testRoleAgents()}
	}
	msg := &fakeMessenger{}
	if agent == nil {
		agent = &recordingAgent{}
	}
	m := New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st, msg
}

func seedOrchestrator(st *fakeStore, id domain.SessionID, project domain.ProjectID, createdAt time.Time, terminated bool) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: project, Kind: domain.KindOrchestrator,
		IsTerminated: terminated, CreatedAt: createdAt, UpdatedAt: createdAt,
		Metadata: domain.SessionMetadata{WorkspacePath: "/tmp/ws/" + string(id), Branch: "ao/x-orchestrator"},
	}
}

func activeOrchestratorIDs(t *testing.T, m *Manager, project domain.ProjectID) []domain.SessionID {
	t.Helper()
	recs, err := m.activeOrchestratorRecords(context.Background(), project)
	if err != nil {
		t.Fatalf("activeOrchestratorRecords: %v", err)
	}
	out := make([]domain.SessionID, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.ID)
	}
	return out
}

// TestEnsureOrchestrator_CleanRetiresActiveBeforeSpawn is the manager-level
// home of the retire→spawn ordering rule. It moved here from the service when
// the project ownership gate took over the sequence: the whole point of the
// gate is that lookup, retirement and spawn are one atomic operation, so the
// behaviour can only be asserted where that gate is held.
func TestEnsureOrchestrator_CleanRetiresActiveBeforeSpawn(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	m, st, msg := ownershipHarness(t, nil, "mer")
	seedOrchestrator(st, "mer-1", "mer", base, false)
	seedOrchestrator(st, "mer-2", "mer", base.Add(time.Minute), false)
	seedOrchestrator(st, "mer-4", "mer", base, true) // terminated: must be left alone
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}

	res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true)
	if err != nil {
		t.Fatalf("EnsureOrchestrator: %v", err)
	}
	if res.Reused {
		t.Fatal("clean replacement must spawn, not reuse")
	}

	for _, id := range []domain.SessionID{"mer-1", "mer-2"} {
		if !st.sessions[id].IsTerminated {
			t.Errorf("%s must be retired before the successor spawns", id)
		}
	}
	if st.sessions["mer-3"].IsTerminated {
		t.Error("worker sessions must not be retired")
	}
	// Exactly one active orchestrator remains, and it is the new one.
	active := activeOrchestratorIDs(t, m, "mer")
	if len(active) != 1 || active[0] != res.Record.ID {
		t.Fatalf("active orchestrators = %v, want only the successor %s", active, res.Record.ID)
	}
	// Both outgoing orchestrators were warned, with the accurate notice.
	if len(msg.msgs) != 2 {
		t.Fatalf("retire notices = %d, want 2", len(msg.msgs))
	}
	for _, got := range msg.msgs {
		if got != OrchestratorRetireNotice {
			t.Errorf("retire notice = %q, want OrchestratorRetireNotice", got)
		}
	}
}

// TestEnsureOrchestrator_CleanContinuesWhenRetireNoticeFails pins that
// replacement never depends on the advisory notice landing. A retire notice can
// legitimately be suppressed (pane exited, awaiting input).
func TestEnsureOrchestrator_CleanContinuesWhenRetireNoticeFails(t *testing.T) {
	m, st, msg := ownershipHarness(t, nil, "mer")
	msg.err = errors.New("pane closed")
	seedOrchestrator(st, "mer-1", "mer", time.Now().Add(-time.Hour), false)

	res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true)
	if err != nil {
		t.Fatalf("EnsureOrchestrator: %v", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Error("outgoing orchestrator must retire even when the notice fails")
	}
	if res.Record.ID == "" || res.Reused {
		t.Fatalf("successor must still spawn: %+v", res)
	}
}

// TestEnsureOrchestrator_NoCleanReusesNewestActive covers the idempotent path.
func TestEnsureOrchestrator_NoCleanReusesNewestActive(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	m, st, _ := ownershipHarness(t, nil, "mer")
	seedOrchestrator(st, "mer-1", "mer", base, false)
	seedOrchestrator(st, "mer-2", "mer", base.Add(time.Minute), false)
	before := len(st.sessions)

	res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, false)
	if err != nil {
		t.Fatalf("EnsureOrchestrator: %v", err)
	}
	if !res.Reused {
		t.Fatal("an active orchestrator exists; must be reused, not replaced")
	}
	if res.Record.ID != "mer-2" {
		t.Fatalf("reused %s, want the newest active orchestrator mer-2", res.Record.ID)
	}
	if len(st.sessions) != before {
		t.Fatalf("sessions = %d, want no new session on the idempotent path", len(st.sessions))
	}
	if st.sessions["mer-1"].IsTerminated {
		t.Error("non-clean ensure must not retire anything")
	}
}

// blockingAgent parks inside spawn until released, so a test can prove two
// spawns are (or are not) in flight at the same time.
type blockingAgent struct {
	recordingAgent
	entered chan struct{}
	release chan struct{}
}

func (a *blockingAgent) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	select {
	case a.entered <- struct{}{}:
	default:
	}
	<-a.release
	return a.recordingAgent.GetLaunchCommand(ctx, cfg)
}

// TestEnsureOrchestrator_SerializesSameProject is the core gate guarantee:
// concurrent callers for one project cannot both observe "no orchestrator" and
// both spawn.
func TestEnsureOrchestrator_SerializesSameProject(t *testing.T) {
	agent := &recordingAgent{}
	m, _, _ := ownershipHarness(t, agent, "mer")

	const callers = 8
	var wg sync.WaitGroup
	ids := make([]domain.SessionID, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, false)
			ids[i], errs[i] = res.Record.ID, err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if agent.launchCalls != 1 {
		t.Fatalf("launches = %d, want exactly 1 orchestrator spawned under contention", agent.launchCalls)
	}
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("caller %d got %s, caller 0 got %s: all callers must observe one owner", i, id, ids[0])
		}
	}
}

// TestAcquireProjectOwnership_DifferentProjectsDoNotBlock proves the gate is
// keyed by project, not global: holding ownership of one project must never
// stall another.
//
// This exercises the gate directly rather than driving two concurrent
// EnsureOrchestrator calls through the package fakes. Running two real spawns
// at once is what we want to assert, but fakeStore is not safe for concurrent
// use (`CreateSession` writes a plain map), so a full-stack version of this
// test fails with "concurrent map writes" — a fixture limitation, not a
// production one. Same-project serialization is still covered end to end by
// TestEnsureOrchestrator_SerializesSameProject, which never runs two spawns
// concurrently by construction.
func TestAcquireProjectOwnership_DifferentProjectsDoNotBlock(t *testing.T) {
	m, _, _ := ownershipHarness(t, nil, "alpha", "beta")

	releaseAlpha, err := m.acquireProjectOwnership(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("acquire alpha: %v", err)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, err := m.acquireProjectOwnership(context.Background(), "beta")
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		if release == nil {
			t.Fatal("acquiring beta failed")
		}
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("beta blocked behind alpha: the ownership gate must be per-project")
	}
	releaseAlpha()

	// And the gate is reusable once released.
	release, err := m.acquireProjectOwnership(context.Background(), "alpha")
	if err != nil {
		t.Fatalf("re-acquire alpha after release: %v", err)
	}
	release()
}

// TestSpawn_OrchestratorCannotBypassTheGate pins that the plain Spawn entry
// point self-acquires ownership, so it cannot race a concurrent replacement.
func TestSpawn_OrchestratorCannotBypassTheGate(t *testing.T) {
	agent := &blockingAgent{entered: make(chan struct{}, 2), release: make(chan struct{})}
	m, _, _ := ownershipHarness(t, agent, "mer")

	spawned := make(chan error, 1)
	go func() {
		_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator})
		spawned <- err
	}()
	<-agent.entered // Spawn now holds the project gate.

	ensured := make(chan error, 1)
	go func() {
		_, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true)
		ensured <- err
	}()

	select {
	case err := <-ensured:
		t.Fatalf("EnsureOrchestrator ran while Spawn held the gate (err=%v)", err)
	case <-time.After(200 * time.Millisecond):
		// Correctly blocked.
	}
	close(agent.release)
	if err := <-spawned; err != nil {
		t.Fatalf("spawn: %v", err)
	}
	select {
	case err := <-ensured:
		if err != nil {
			t.Fatalf("ensure after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("EnsureOrchestrator never proceeded after the gate was released")
	}
}

// TestAcquireProjectOwnership_HonoursContextCancellation keeps a blocked
// acquirer cancellable rather than parked forever behind a stuck operation.
func TestAcquireProjectOwnership_HonoursContextCancellation(t *testing.T) {
	m, _, _ := ownershipHarness(t, nil, "mer")
	release, err := m.acquireProjectOwnership(context.Background(), "mer")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.acquireProjectOwnership(ctx, "mer"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewestOrchestratorRecord_IsDeterministic(t *testing.T) {
	at := time.Now()
	recs := []domain.SessionRecord{
		{ID: "a", CreatedAt: at, UpdatedAt: at},
		{ID: "c", CreatedAt: at, UpdatedAt: at},
		{ID: "b", CreatedAt: at, UpdatedAt: at},
	}
	// Equal timestamps: lexically greatest id wins, so the choice never depends
	// on ListSessions insertion order.
	if got := newestOrchestratorRecord(recs).ID; got != "c" {
		t.Fatalf("survivor = %s, want c", got)
	}
	newer := append([]domain.SessionRecord(nil), recs...)
	newer = append(newer, domain.SessionRecord{ID: "a0", CreatedAt: at.Add(time.Minute), UpdatedAt: at})
	if got := newestOrchestratorRecord(newer).ID; got != "a0" {
		t.Fatalf("survivor = %s, want the newest CreatedAt a0", got)
	}
}

// ---------------------------------------------------------------------------
// Canonical-workspace aliasing
//
// The orchestrator worktree is canonical per project, so a superseded row that
// keeps recording it is a live alias for whatever the successor owns. These
// tests cover both layers of the fix: retirement releases the claim, and any
// path-keyed teardown refuses a path the active orchestrator holds.
// ---------------------------------------------------------------------------

// TestRetireForReplacement_ReleasesWorkspaceClaim is the root fix: a retired row
// must stop naming the canonical workspace it no longer owns.
func TestRetireForReplacement_ReleasesWorkspaceClaim(t *testing.T) {
	m, st, _ := ownershipHarness(t, nil, "mer")
	seedOrchestrator(st, "mer-a", "mer", time.Now().Add(-time.Hour), false)

	if err := m.RetireForReplacement(context.Background(), "mer-a"); err != nil {
		t.Fatalf("RetireForReplacement: %v", err)
	}
	got := st.sessions["mer-a"].Metadata
	if got.WorkspacePath != "" || got.Branch != "" || got.RuntimeHandleID != "" {
		t.Fatalf("retired row still claims workspace ownership: %+v", got)
	}
	if !st.sessions["mer-a"].IsTerminated {
		t.Fatal("retired row must be terminated")
	}
}

// TestKill_SupersededOrchestratorLeavesSuccessorIntact is the sequential bug:
// replace A with B, then kill A. B keeps its workspace.
func TestKill_SupersededOrchestratorLeavesSuccessorIntact(t *testing.T) {
	m, st, _ := ownershipHarness(t, nil, "mer")
	seedOrchestrator(st, "mer-a", "mer", time.Now().Add(-time.Hour), false)

	res, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, true)
	if err != nil {
		t.Fatalf("EnsureOrchestrator: %v", err)
	}
	successor := res.Record.ID
	successorPath := st.sessions[successor].Metadata.WorkspacePath
	if successorPath == "" {
		t.Fatal("successor has no workspace path; fixture cannot exercise the alias")
	}

	if _, err := m.Kill(context.Background(), "mer-a"); err != nil {
		t.Fatalf("Kill(superseded): %v", err)
	}

	if st.sessions[successor].IsTerminated {
		t.Fatal("killing the superseded orchestrator terminated the successor")
	}
	if got := st.sessions[successor].Metadata.WorkspacePath; got != successorPath {
		t.Fatalf("successor workspace path changed: %q -> %q", successorPath, got)
	}
	active := activeOrchestratorIDs(t, m, "mer")
	if len(active) != 1 || active[0] != successor {
		t.Fatalf("active orchestrators = %v, want only the successor %s", active, successor)
	}
}

// aliasedFixture builds the legacy shape this fix defends against: a terminated
// predecessor whose row still records the canonical path the active successor
// owns. Rows written before releaseRetiredWorkspaceClaim look exactly like this.
func aliasedFixture(t *testing.T) (*Manager, *fakeStore, *fakeWorkspace, string) {
	t.Helper()
	st := newFakeStore()
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	const canonical = "/ws/mer/orchestrator/mer-orchestrator"
	st.sessions["mer-old"] = domain.SessionRecord{
		ID: "mer-old", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: canonical, Branch: "ao/mer-orchestrator"},
	}
	st.sessions["mer-new"] = domain.SessionRecord{
		ID: "mer-new", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{WorkspacePath: canonical, Branch: "ao/mer-orchestrator"},
	}
	return m, st, ws, canonical
}

func TestKill_RefusesWorkspaceOwnedByActiveOrchestrator(t *testing.T) {
	m, st, ws, canonical := aliasedFixture(t)

	if _, err := m.Kill(context.Background(), "mer-old"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if ws.lastDestroyInfo.Path == canonical {
		t.Fatal("killed the canonical workspace owned by the active orchestrator")
	}
	if st.sessions["mer-new"].IsTerminated {
		t.Fatal("active orchestrator was terminated")
	}
}

func TestCleanup_SkipsWorkspaceOwnedByActiveOrchestrator(t *testing.T) {
	m, st, ws, canonical := aliasedFixture(t)

	res, err := m.Cleanup(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if ws.lastDestroyInfo.Path == canonical {
		t.Fatal("cleanup reclaimed the canonical workspace owned by the active orchestrator")
	}
	for _, id := range res.Cleaned {
		if id == "mer-old" {
			t.Fatal("superseded orchestrator must not be reported as cleaned")
		}
	}
	var skipped bool
	for _, s := range res.Skipped {
		if s.SessionID == "mer-old" {
			skipped = true
			if s.Reason == "" {
				t.Error("skip must carry a user-facing reason")
			}
		}
	}
	if !skipped {
		t.Fatalf("mer-old must be skipped with a reason; got %+v", res)
	}
	if st.sessions["mer-new"].IsTerminated {
		t.Fatal("active orchestrator was terminated")
	}
}

// TestPublicOrchestratorMutationsBlockOnTheGate proves every public mutation
// path acquires ownership rather than racing a replacement. Each call must
// block while the gate is held and complete once it is released; the returned
// error is irrelevant (some legitimately fail on state), only the blocking is.
func TestPublicOrchestratorMutationsBlockOnTheGate(t *testing.T) {
	cases := []struct {
		name string
		call func(*Manager) error
	}{
		{"Kill", func(m *Manager) error { _, err := m.Kill(context.Background(), "mer-a"); return err }},
		{"RetireForReplacement", func(m *Manager) error { return m.RetireForReplacement(context.Background(), "mer-a") }},
		{"RestoreWithMode", func(m *Manager) error { _, err := m.RestoreWithMode(context.Background(), "mer-a"); return err }},
		{"ResumeAgentWithMode", func(m *Manager) error { _, err := m.ResumeAgentWithMode(context.Background(), "mer-a"); return err }},
		{"RollbackSpawn", func(m *Manager) error { _, _, err := m.RollbackSpawn(context.Background(), "mer-a"); return err }},
		{"Cleanup", func(m *Manager) error { _, err := m.Cleanup(context.Background(), "mer"); return err }},
		{"EnsureOrchestrator", func(m *Manager) error {
			_, err := m.EnsureOrchestrator(context.Background(), ports.SpawnConfig{ProjectID: "mer"}, false)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := ownershipHarness(t, nil, "mer")
			seedOrchestrator(st, "mer-a", "mer", time.Now().Add(-time.Hour), false)

			release, err := m.acquireProjectOwnership(context.Background(), "mer")
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}

			done := make(chan error, 1)
			go func() { done <- tc.call(m) }()

			select {
			case <-done:
				release()
				t.Fatalf("%s proceeded while the project ownership gate was held", tc.name)
			case <-time.After(150 * time.Millisecond):
			}

			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("%s never proceeded after the gate was released", tc.name)
			}
		})
	}
}

// pathRecordingWorkspace records every path any teardown touches, so a test can
// assert a successor's worktrees were never destroyed. Wraps the shared fake
// rather than modifying it.
type pathRecordingWorkspace struct {
	fakeWorkspace
	mu        sync.Mutex
	destroyed []string
}

func (w *pathRecordingWorkspace) record(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if path != "" {
		w.destroyed = append(w.destroyed, path)
	}
}

func (w *pathRecordingWorkspace) touched(path string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range w.destroyed {
		if p == path {
			return true
		}
	}
	return false
}

func (w *pathRecordingWorkspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	w.record(info.Path)
	return w.fakeWorkspace.Destroy(ctx, info)
}

func (w *pathRecordingWorkspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	w.record(info.Path)
	return w.fakeWorkspace.ForceDestroy(ctx, info)
}

// TestRetireForReplacement_ReleasesClaimOnBranchlessPath covers the
// scratch/incomplete-handle branch, which terminates early and previously
// returned without releasing the row's claim.
func TestRetireForReplacement_ReleasesClaimOnBranchlessPath(t *testing.T) {
	m, st, _ := ownershipHarness(t, nil, "mer")
	// WorkspacePath set but no Branch => the degenerate retirement branch.
	st.sessions["mer-a"] = domain.SessionRecord{
		ID: "mer-a", ProjectID: "mer", Kind: domain.KindOrchestrator,
		CreatedAt: time.Now().Add(-time.Hour),
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer/orchestrator/canonical"},
	}

	if err := m.RetireForReplacement(context.Background(), "mer-a"); err != nil {
		t.Fatalf("RetireForReplacement: %v", err)
	}
	got := st.sessions["mer-a"]
	if got.Metadata.WorkspacePath != "" {
		t.Fatalf("branchless retirement left a workspace claim: %q", got.Metadata.WorkspacePath)
	}
	if !got.IsTerminated {
		t.Fatal("row must be terminated")
	}
}

// workspaceProjectHarness registers a workspace-kind project and gives a
// session more than one saved worktree row, which is what makes
// workspaceProjectRows report a workspace project.
func workspaceProjectHarness(t *testing.T) (*Manager, *fakeStore, *pathRecordingWorkspace) {
	t.Helper()
	st := newFakeStore()
	st.num = 100
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer", Kind: domain.ProjectKindWorkspace, Path: "/repo/mer", Config: testRoleAgents(),
	}
	// Child repos must be registered, or sessionWorktreeRowsToRepoInfos rejects
	// the saved rows and workspaceProjectRows never reports a workspace project
	// — which would let the alias tests pass for the wrong reason.
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{
		{ProjectID: "mer", Name: "child", RelativePath: "child"},
		{ProjectID: "mer", Name: "api", RelativePath: "api"},
		{ProjectID: "mer", Name: "web", RelativePath: "web"},
	}
	ws := &pathRecordingWorkspace{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: ws, Store: st, Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	return m, st, ws
}

func TestRetireForReplacement_ReleasesClaimOnWorkspaceProjectPath(t *testing.T) {
	m, st, _ := workspaceProjectHarness(t)
	const root = "/ws/mer/orchestrator/mer-orchestrator"
	st.sessions["mer-a"] = domain.SessionRecord{
		ID: "mer-a", ProjectID: "mer", Kind: domain.KindOrchestrator,
		CreatedAt: time.Now().Add(-time.Hour),
		Metadata:  domain.SessionMetadata{WorkspacePath: root, Branch: "ao/mer-orchestrator"},
	}
	st.worktrees["mer-a"] = []domain.SessionWorktreeRecord{
		{SessionID: "mer-a", RepoName: domain.RootWorkspaceRepoName, Branch: "ao/mer-orchestrator", WorktreePath: root, State: "active"},
		{SessionID: "mer-a", RepoName: "child", Branch: "ao/mer-orchestrator", WorktreePath: root + "/child", State: "active"},
	}

	if err := m.RetireForReplacement(context.Background(), "mer-a"); err != nil {
		t.Fatalf("RetireForReplacement: %v", err)
	}
	got := st.sessions["mer-a"]
	if got.Metadata.WorkspacePath != "" || got.Metadata.Branch != "" {
		t.Fatalf("workspace-project retirement left a claim: %+v", got.Metadata)
	}
	if !got.IsTerminated {
		t.Fatal("row must be terminated")
	}
}

// TestKill_WorkspaceProjectAliasCannotDestroySuccessorChildren is the legacy
// shape: a superseded workspace-project row whose saved rows name the root AND
// child worktrees the active successor now owns. Clearing the root WorkspaceInfo
// alone is not enough — the saved rows must be suppressed too.
func TestKill_WorkspaceProjectAliasCannotDestroySuccessorChildren(t *testing.T) {
	m, st, ws := workspaceProjectHarness(t)
	const root = "/ws/mer/orchestrator/mer-orchestrator"
	child1, child2 := root+"/api", root+"/web"

	rows := []domain.SessionWorktreeRecord{
		{SessionID: "mer-old", RepoName: domain.RootWorkspaceRepoName, Branch: "ao/mer-orchestrator", WorktreePath: root, State: "active"},
		{SessionID: "mer-old", RepoName: "api", Branch: "ao/mer-orchestrator", WorktreePath: child1, State: "active"},
		{SessionID: "mer-old", RepoName: "web", Branch: "ao/mer-orchestrator", WorktreePath: child2, State: "active"},
	}
	st.sessions["mer-old"] = domain.SessionRecord{
		ID: "mer-old", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: root, Branch: "ao/mer-orchestrator"},
	}
	st.worktrees["mer-old"] = rows
	// The active successor owns the same canonical paths.
	st.sessions["mer-new"] = domain.SessionRecord{
		ID: "mer-new", ProjectID: "mer", Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{WorkspacePath: root, Branch: "ao/mer-orchestrator"},
	}

	if _, err := m.Kill(context.Background(), "mer-old"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	for _, p := range []string{root, child1, child2} {
		if ws.touched(p) {
			t.Errorf("killed %q, which the active orchestrator owns", p)
		}
	}
	if st.sessions["mer-new"].IsTerminated {
		t.Fatal("active orchestrator was terminated")
	}
}

// TestCleanupAllProjects_BlocksOnEachProjectGate pins the unfiltered path:
// Cleanup("") must serialize against real per-project mutations, not against a
// synthetic empty-project gate.
func TestCleanupAllProjects_BlocksOnEachProjectGate(t *testing.T) {
	m, st, _ := ownershipHarness(t, nil, "alpha", "beta")
	seedOrchestrator(st, "alpha-1", "alpha", time.Now().Add(-time.Hour), true)
	seedOrchestrator(st, "beta-1", "beta", time.Now().Add(-time.Hour), true)

	// Hold beta; unfiltered cleanup must not run to completion.
	releaseBeta, err := m.acquireProjectOwnership(context.Background(), "beta")
	if err != nil {
		t.Fatalf("acquire beta: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, cleanErr := m.Cleanup(context.Background(), "")
		done <- cleanErr
	}()

	select {
	case <-done:
		releaseBeta()
		t.Fatal("Cleanup(\"\") completed while a real project's ownership gate was held")
	case <-time.After(150 * time.Millisecond):
	}

	releaseBeta()
	select {
	case cleanErr := <-done:
		if cleanErr != nil {
			t.Fatalf("cleanup: %v", cleanErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Cleanup(\"\") never proceeded after the gate was released")
	}
}
