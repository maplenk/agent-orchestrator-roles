package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Delegation asks the MANAGER who owns the project; it no longer selects an
// orchestrator itself. The store below still seeds several — deliberately, to
// show that seeding them changes nothing: a second ownership resolver in the
// service is the defect 2B-0a removed, and the manager's answer is the only one
// that counts.
func TestDelegateTaskSpawnsWorkerThenRequestsTitleFromTheProjectOwner(t *testing.T) {
	tests := []struct {
		name      string
		agent     domain.AgentHarness
		model     string
		mode      domain.SessionMode
		wantAgent domain.AgentHarness
	}{
		{name: "project default"},
		{name: "requested agent model and mode", agent: domain.HarnessCursor, model: "  sonnet-custom  ", mode: domain.SessionModeChat, wantAgent: domain.HarnessCursor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
			now := time.Now().UTC()
			st.sessions["orch-old"] = domain.SessionRecord{ID: "orch-old", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now.Add(-time.Minute)}
			st.sessions["orch-new"] = domain.SessionRecord{ID: "orch-new", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now}
			st.sessions["orch-exited"] = domain.SessionRecord{ID: "orch-exited", ProjectID: "ao", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityExited}, CreatedAt: now.Add(time.Minute)}
			st.sessions["orch-dead"] = domain.SessionRecord{ID: "orch-dead", ProjectID: "ao", Kind: domain.KindOrchestrator, IsTerminated: true, CreatedAt: now.Add(2 * time.Minute)}
			st.sessions["worker"] = domain.SessionRecord{ID: "worker", ProjectID: "ao", Kind: domain.KindWorker, CreatedAt: now.Add(3 * time.Minute)}
			cmd := &fakeCommander{projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{
				"ao": {ID: "orch-new", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now},
			}}
			svc := &Service{store: st, manager: cmd, runBackground: runInline}

			brief := "  Fix the renderer\nwithout changing the API.  "
			out, err := svc.DelegateTask(context.Background(), DelegateTaskInput{
				ProjectID: "ao", Brief: brief, RequestedAgent: tt.agent, Model: tt.model, RequestedMode: tt.mode,
			})
			if err != nil {
				t.Fatalf("DelegateTask: %v", err)
			}
			if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
				t.Fatalf("out = %#v, want worker mer-9 with asynchronous title handoff", out)
			}
			if !cmd.spawned || cmd.spawnedCfg.ProjectID != "ao" || cmd.spawnedCfg.Kind != domain.KindWorker || cmd.spawnedCfg.Harness != tt.wantAgent || cmd.spawnedCfg.Prompt != brief || cmd.spawnedCfg.DisplayName != "Fix the renderer wit" {
				t.Fatalf("spawn cfg = %#v", cmd.spawnedCfg)
			}
			if cmd.spawnedCfg.AgentConfig.Model != strings.TrimSpace(tt.model) {
				t.Fatalf("spawn model = %q, want %q", cmd.spawnedCfg.AgentConfig.Model, strings.TrimSpace(tt.model))
			}
			if cmd.spawnedCfg.RequestedMode != tt.mode {
				t.Fatalf("spawn mode = %q, want %q", cmd.spawnedCfg.RequestedMode, tt.mode)
			}
			if len(cmd.sent) != 1 || cmd.sent[0] != "orch-new" {
				t.Fatalf("sent = %#v; want orch-new", cmd.sent)
			}
			if len(cmd.ready) != 1 || cmd.ready[0] != "orch-new" {
				t.Fatalf("readiness waits = %#v; want orch-new", cmd.ready)
			}
			for _, want := range []string{
				"AO TASK TITLE UPDATE",
				"Do not spawn another worker or orchestrator",
				`ao session rename mer-9 "<title, max 20 chars>"`,
				"Worker session id: mer-9",
				brief,
			} {
				if !strings.Contains(cmd.sentMessages[0], want) {
					t.Fatalf("title delegation missing %q:\n%s", want, cmd.sentMessages[0])
				}
			}
			if tt.model != "" && !strings.Contains(cmd.sentMessages[0], "Requested model: sonnet-custom") {
				t.Fatalf("title delegation missing requested model:\n%s", cmd.sentMessages[0])
			}
		})
	}
}

func TestDelegatedTaskDisplayName(t *testing.T) {
	for _, tt := range []struct {
		name  string
		brief string
		want  string
	}{
		{name: "empty", brief: " \n\t ", want: "Untitled task"},
		{name: "short", brief: "  tell me a joke  ", want: "tell me a joke"},
		{name: "whitespace", brief: "Fix the renderer\nwithout changing the API", want: "Fix the renderer wit"},
		{name: "unicode rune limit", brief: "一二三四五六七八九十一二三四五六七八九十一", want: "一二三四五六七八九十一二三四五六七八九十"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := delegatedTaskDisplayName(tt.brief); got != tt.want {
				t.Fatalf("delegatedTaskDisplayName(%q) = %q, want %q", tt.brief, got, tt.want)
			}
		})
	}
}

func TestDelegateTaskStartsPromptlessWorkerWithoutRequestingTitle(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}
	cmd := &fakeCommander{}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(
		context.Background(),
		DelegateTaskInput{ProjectID: "ao", Brief: " \n\t "},
	)
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want promptless worker mer-9", out)
	}
	if !cmd.spawned || cmd.spawnedCfg.Prompt != "" || cmd.spawnedCfg.DisplayName != "Untitled task" {
		t.Fatalf("spawn cfg = %#v", cmd.spawnedCfg)
	}
	if len(cmd.ready) != 0 || len(cmd.sent) != 0 || len(cmd.resumed) != 0 {
		t.Fatalf("promptless spawn contacted orchestrator: ready=%#v sent=%#v resumed=%#v", cmd.ready, cmd.sent, cmd.resumed)
	}
}

func TestDelegateTaskSkipsTitleWhenProjectOwnerIsExited(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	now := time.Now().UTC()
	st.sessions["orch-old"] = domain.SessionRecord{ID: "orch-old", ProjectID: "ao", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityExited}, CreatedAt: now.Add(-time.Minute)}
	st.sessions["orch-new"] = domain.SessionRecord{ID: "orch-new", ProjectID: "ao", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityExited}, CreatedAt: now}
	cmd := &fakeCommander{projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{
		"ao": {ID: "orch-new", ProjectID: "ao", Kind: domain.KindOrchestrator, Activity: domain.Activity{State: domain.ActivityExited}, CreatedAt: now},
	}}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want worker mer-9 with asynchronous title handoff", out)
	}
	if len(cmd.resumed) != 0 || len(cmd.ready) != 0 || len(cmd.sent) != 0 {
		t.Fatalf("exited owner was contacted: resumed=%#v ready=%#v sent=%#v", cmd.resumed, cmd.ready, cmd.sent)
	}
}

func TestDelegateTaskSkipsTitleWhenProjectHasNoOwner(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["orch-dead"] = domain.SessionRecord{ID: "orch-dead", ProjectID: "ao", Kind: domain.KindOrchestrator, IsTerminated: true}
	cmd := &fakeCommander{spawnFunc: func(cfg ports.SpawnConfig) domain.SessionRecord {
		if cfg.Kind == domain.KindOrchestrator {
			return domain.SessionRecord{ID: "orch-new", ProjectID: cfg.ProjectID, Kind: cfg.Kind}
		}
		return domain.SessionRecord{ID: "worker-new", ProjectID: cfg.ProjectID, Kind: cfg.Kind}
	}}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "worker-new" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want worker-new without title handoff", out)
	}
	if cmd.spawnCalls != 1 {
		t.Fatalf("spawn calls = %d, want worker only", cmd.spawnCalls)
	}
	if len(cmd.ready) != 0 || len(cmd.sent) != 0 {
		t.Fatalf("missing owner was contacted: ready=%#v sent=%#v", cmd.ready, cmd.sent)
	}
}

func TestRefineDelegatedTaskTitleReturnsProjectOwnerLookupError(t *testing.T) {
	wantErr := errors.New("owner lookup failed")
	svc := &Service{manager: &fakeCommander{projectOrchestratorErr: wantErr}}

	err := svc.refineDelegatedTaskTitle(context.Background(), "worker", DelegateTaskInput{ProjectID: "ao"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("refineDelegatedTaskTitle error = %v, want wrapped owner lookup error", err)
	}
}

func TestDelegateTaskKeepsSpawnSuccessWhenTitleOrchestratorNeverBecomesReady(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}
	cmd := &fakeCommander{
		projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{"ao": {ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}},
		readyErr:            errors.New("readiness timed out"),
	}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want spawned worker without title recipient", out)
	}
	if len(cmd.ready) != 1 || cmd.ready[0] != "orch" {
		t.Fatalf("readiness waits = %#v, want orch", cmd.ready)
	}
	if len(cmd.sent) != 0 {
		t.Fatalf("sent = %#v, want no title request before readiness", cmd.sent)
	}
}

func TestDelegateTaskKeepsSpawnSuccessWhenTitleRequestFails(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}
	cmd := &fakeCommander{
		projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{"ao": {ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}},
		sendErr:             errors.New("orchestrator exited"),
	}

	out, err := (&Service{store: st, manager: cmd, runBackground: runInline}).DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.WorkerID != "mer-9" || out.OrchestratorID != "" {
		t.Fatalf("out = %#v, want spawned worker without title recipient", out)
	}
	if !cmd.spawned {
		t.Fatal("worker was not spawned")
	}
}

// The invariant, stated directly: whoever the manager names is the recipient,
// even when the store's newest active orchestrator is somebody else. If this
// ever fails, the service has started resolving ownership again.
func TestDelegateTaskAddressesTheManagersOwnerNotTheStoresNewest(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	now := time.Now().UTC()
	// The store's newest active orchestrator.
	st.sessions["orch-newest-in-store"] = domain.SessionRecord{
		ID: "orch-newest-in-store", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now.Add(time.Hour),
	}
	// The one the manager actually considers the owner.
	st.sessions["orch-owner"] = domain.SessionRecord{
		ID: "orch-owner", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now,
	}
	cmd := &fakeCommander{projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{
		"ao": {ID: "orch-owner", ProjectID: "ao", Kind: domain.KindOrchestrator, CreatedAt: now},
	}}
	svc := &Service{store: st, manager: cmd, runBackground: runInline}

	out, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "do a thing"})
	if err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if out.OrchestratorID != "" || len(cmd.sent) != 1 || cmd.sent[0] != "orch-owner" {
		t.Fatalf("out=%#v sent=%#v, want asynchronous handoff to manager-owned orchestrator", out, cmd.sent)
	}
}

// Without this passthrough the desktop composer cannot delegate at all on a
// strictDelegation project: roles.Resolve refuses a KindWorker spawn with no
// role, so every delegation from the UI would fail ROLE_REQUIRED.
func TestDelegateTaskPassesTheRoleThroughToSpawn(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	cmd := &fakeCommander{}
	svc := &Service{store: st, manager: cmd}

	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{
		ProjectID: "ao", Brief: "ship it", RoleID: "implementor",
	}); err != nil {
		t.Fatalf("DelegateTask: %v", err)
	}
	if cmd.spawnedCfg.RoleID != "implementor" {
		t.Fatalf("spawn role = %q, want implementor", cmd.spawnedCfg.RoleID)
	}
}

// The service must not second-guess the map. Role and harness together are
// exactly what HARNESS_OVERRIDE_FORBIDDEN exists to refuse under a strict map —
// and exactly what a NON-strict map is allowed to accept. Deciding that here
// would be a second policy engine disagreeing with the manager's, so the pair
// is forwarded verbatim and the manager rules on it.
func TestDelegateTaskDoesNotAdjudicateRoleAndHarnessItself(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	cmd := &fakeCommander{}
	svc := &Service{store: st, manager: cmd}

	if _, err := svc.DelegateTask(context.Background(), DelegateTaskInput{
		ProjectID: "ao", Brief: "ship it", RoleID: "implementor", RequestedAgent: domain.HarnessCursor,
	}); err != nil {
		t.Fatalf("DelegateTask rejected a pair the map may legitimately allow: %v", err)
	}
	if cmd.spawnedCfg.RoleID != "implementor" || cmd.spawnedCfg.Harness != domain.HarnessCursor {
		t.Fatalf("spawn cfg = %#v; both fields must reach the manager unaltered", cmd.spawnedCfg)
	}
}

func TestDelegateTaskReturnsBeforeTitleRequestCompletes(t *testing.T) {
	st := newFakeStore()
	st.projects["ao"] = domain.ProjectRecord{ID: "ao"}
	st.sessions["orch"] = domain.SessionRecord{ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}
	titleStarted := make(chan struct{})
	releaseTitle := make(chan struct{})
	titleFinished := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseTitle:
		default:
			close(releaseTitle)
		}
	})
	cmd := &fakeCommander{
		projectOrchestrator: map[domain.ProjectID]domain.SessionRecord{"ao": {ID: "orch", ProjectID: "ao", Kind: domain.KindOrchestrator}},
		sendFunc: func(domain.SessionID, string) error {
			close(titleStarted)
			<-releaseTitle
			close(titleFinished)
			return nil
		},
	}
	svc := &Service{store: st, manager: cmd}

	type result struct {
		out DelegateTaskOutcome
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		out, err := svc.DelegateTask(context.Background(), DelegateTaskInput{ProjectID: "ao", Brief: "Fix it"})
		resultCh <- result{out: out, err: err}
	}()

	select {
	case got := <-resultCh:
		if got.err != nil || got.out.WorkerID != "mer-9" {
			t.Fatalf("DelegateTask = %#v, %v", got.out, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DelegateTask waited for background title request")
	}
	select {
	case <-titleStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request did not start")
	}
	close(releaseTitle)
	select {
	case <-titleFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("background title request did not finish")
	}
}

func runInline(work func()) {
	work()
}
