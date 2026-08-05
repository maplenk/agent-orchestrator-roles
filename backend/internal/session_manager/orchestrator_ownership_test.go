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
