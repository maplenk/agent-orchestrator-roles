package sessionmanager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

func pinImplementorTemplate(t *testing.T, st *fakeStore) (artifactID, sha string) {
	t.Helper()
	raw := []byte("---\nid: implementor\nname: Impl\n---\n# body\n")
	tmpl, err := roles.ParseTemplate(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutTemplateArtifact(ctx, tmpl.ArtifactID, tmpl.SHA256, raw, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return tmpl.ArtifactID, tmpl.SHA256
}

func workerSession(st *fakeStore, id domain.SessionID, harness domain.AgentHarness, ws, art, sha string) {
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: harness,
		Metadata: domain.SessionMetadata{
			Branch: "ao/mer-1/root", WorkspacePath: ws, RuntimeHandleID: "rt-1",
			RuntimeLaunchID: "src-gen", AgentSessionID: "native-old", Prompt: "implement feature",
			Role: domain.SessionRoleBinding{
				RoleID: "implementor", TemplateArtifactID: art, TemplateSHA256: sha,
				ResolvedHarness: harness, ResolvedModel: "claude-sonnet-source",
				ResolvedPermissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
	}
}

// testSwitchCaps enables switch_supported for the initial matrix in unit tests
// without advertising production capability.
func testSwitchCaps(h domain.AgentHarness) capabilities.Caps {
	c := capabilities.For(h)
	switch h {
	case domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessFake:
		c.SwitchSupported = true
	}
	return c
}

func TestSwitchWorker_GenerationMatchesRuntime(t *testing.T) {
	st := newFakeStore()
	ws := filepath.Join(t.TempDir(), "ws")
	_ = os.MkdirAll(ws, 0o750)
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	// Distinct IDs: first would be used if we called newLaunchID twice incorrectly.
	var calls atomic.Int32
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string {
			n := calls.Add(1)
			return fmt.Sprintf("unique-gen-%d", n)
		},
	})
	m.switchCapsOverride = testSwitchCaps

	res, err := m.SwitchWorker(ctx, SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
		Semantic: domain.SemanticHandoffV1{Objective: "keep going"},
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if res.GenerationID != res.Session.Metadata.RuntimeLaunchID {
		t.Fatalf("ledger gen %q != runtime gen %q", res.GenerationID, res.Session.Metadata.RuntimeLaunchID)
	}
	// Exactly one launch id consumed for the target (source gen was pre-existing).
	if calls.Load() != 1 {
		t.Fatalf("newLaunchID calls = %d, want 1 (unified generation)", calls.Load())
	}
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatalf("harness after ack = %q", res.Session.Harness)
	}
	if res.Session.Metadata.SwitchPending != nil {
		t.Fatal("pending must be cleared after ack")
	}
	// Cross-harness must not leak Claude model to Codex.
	if res.Session.Metadata.Role.ResolvedModel == "claude-sonnet-source" {
		t.Fatal("source model leaked across harnesses")
	}
	if res.Session.Metadata.Role.ResolvedModel != "" {
		t.Fatalf("cross-harness empty TargetModel should clear model, got %q", res.Session.Metadata.Role.ResolvedModel)
	}
}

func TestSwitchWorker_CrossHarnessClearsModel(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	res, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "codex-target"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Session.Metadata.Role.ResolvedModel != "codex-target" {
		t.Fatalf("model = %q", res.Session.Metadata.Role.ResolvedModel)
	}
}

func TestSwitchWorker_HarnessNotPromotedBeforeAck(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	// Fail Create so we stop after post_stop + pending, before ack.
	rt := &fakeRuntime{createErr: errors.New("no runtime")}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v", err)
	}
	rec := st.sessions[id]
	if rec.Harness != domain.HarnessClaudeCode {
		t.Fatalf("current harness promoted early: %q", rec.Harness)
	}
	if rec.Metadata.SwitchPending == nil || rec.Metadata.SwitchPending.ToHarness != domain.HarnessCodex {
		t.Fatalf("pending = %+v", rec.Metadata.SwitchPending)
	}
}

func TestSwitchWorker_DestroyErrorStillAliveIsPreStop(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	rt := &fakeRuntime{
		destroyErr:   errors.New("cleanup failed"),
		aliveByHandle: map[string]bool{"rt-1": true}, // still alive after destroy
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if err == nil || errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("want pre-stop failure, got %v", err)
	}
	// Pending is staged before destroy so recovery can re-drive; source still alive.
	if st.sessions[id].Metadata.SwitchPending == nil {
		t.Fatal("pending must exist as recoverable fence before destroy completes")
	}
	if st.sessions[id].Harness != domain.HarnessClaudeCode {
		t.Fatal("must not promote harness when source still alive")
	}
	if rt.created != 0 {
		t.Fatal("must not launch target while source still alive")
	}
}

func TestSwitchWorker_DestroyErrorButDeadProceedsToPostStop(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	rt := &fakeRuntime{
		destroyErr:    errors.New("cleanup noise"),
		aliveByHandle: map[string]bool{"rt-1": false},
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	res, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if err != nil {
		t.Fatalf("should proceed when probe shows dead: %v", err)
	}
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatal("expected successful switch")
	}
}

func TestRecover_RefusesDoubleLaunchWhenStaleAlive(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-1", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor",
	}
	rec.Metadata.RuntimeHandleID = "stale-live"
	rec.Metadata.RuntimeLaunchID = "other-gen"
	st.sessions[id] = rec
	st.ledger = []domain.LifecycleLedgerRecord{{
		ID: "mer-1:gen-1:post_stop", SessionID: id, ProjectID: "mer",
		Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePostStop,
		GenerationID: "gen-1", FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		PayloadJSON: `{"compiled":"## Host-compiled handoff\n\nx"}`,
	}}

	rt := &fakeRuntime{aliveByHandle: map[string]bool{"stale-live": true}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.RecoverSwitchFromPostStop(ctx, id)
	if !errors.Is(err, ErrSwitchUncertain) {
		t.Fatalf("err = %v, want ErrSwitchUncertain", err)
	}
	if rt.created != 0 {
		t.Fatalf("must not launch second target, create=%d", rt.created)
	}
}

func TestRecover_AcksLiveMatchingGeneration(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-live", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor",
	}
	rec.Metadata.RuntimeHandleID = "h-live"
	rec.Metadata.RuntimeLaunchID = "gen-live"
	st.sessions[id] = rec
	st.ledger = []domain.LifecycleLedgerRecord{{
		ID: "mer-1:gen-live:post_stop", SessionID: id, ProjectID: "mer",
		Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePostStop,
		GenerationID: "gen-live", FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		PayloadJSON: `{"compiled":"## Host-compiled handoff\n\nx"}`,
	}}

	rt := &fakeRuntime{aliveByHandle: map[string]bool{"h-live": true}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	res, err := m.RecoverSwitchFromPostStop(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if rt.created != 0 {
		t.Fatal("ack-only path must not create")
	}
	if res.Session.Harness != domain.HarnessCodex || res.Session.Metadata.SwitchPending != nil {
		t.Fatalf("promoted=%+v pending=%+v", res.Session.Harness, res.Session.Metadata.SwitchPending)
	}
}

func TestFreshConversation_DoesNotStackHandoffs(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps

	res1, err := m.FreshConversation(ctx, id, domain.SemanticHandoffV1{Objective: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate live session after first fresh (need runtime handle for second).
	rec := res1.Session
	rec.Metadata.RuntimeHandleID = "rt-2"
	st.sessions[id] = rec

	res2, err := m.FreshConversation(ctx, id, domain.SemanticHandoffV1{Objective: "r2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(res2.Session.Metadata.Prompt, "## Host-compiled handoff") != 1 {
		t.Fatalf("stacked handoffs:\n%s", res2.Session.Metadata.Prompt)
	}
	if !strings.Contains(res2.Session.Metadata.Prompt, "implement feature") {
		t.Fatal("original task lost")
	}
}

func TestSwitchWorker_RequiresCapability(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	// No override: production caps have switch_supported=false.
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchNotSupported) {
		t.Fatalf("err = %v, want ErrSwitchNotSupported", err)
	}
}

func TestOwnershipMutex_NoDeadlock(t *testing.T) {
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	id := domain.SessionID("mer-1")
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if m.beginSwitch(id) {
				m.endSwitch(id)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if m.beginAgentResume(id) {
				m.endAgentResume(id)
			}
		}
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("deadlock: ownership mutex test timed out")
	}
}

func TestLedgerPhaseIdsAreStable(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "stable-gen" },
	})
	m.switchCapsOverride = testSwitchCaps
	if _, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex}); err != nil {
		t.Fatal(err)
	}
	// Append same phase again must no-op.
	rec := st.sessions[id]
	if err := m.appendSwitchLedger(ctx, rec, domain.LifecycleKindSwitch, domain.LifecyclePhaseTargetAck, "stable-gen", domain.HarnessClaudeCode, domain.HarnessCodex, "", "", "implementor", "", "", "{}"); err != nil {
		t.Fatal(err)
	}
	nAck := 0
	for _, e := range st.ledger {
		if e.Phase == domain.LifecyclePhaseTargetAck {
			nAck++
			if e.ID != "mer-1:stable-gen:target_ack" {
				t.Fatalf("id = %q", e.ID)
			}
		}
	}
	if nAck != 1 {
		t.Fatalf("ack rows = %d, want 1", nAck)
	}
}

func TestComposeSwitchPrompt_StripsPrior(t *testing.T) {
	prior := "## Host-compiled handoff\n\nold\n\n## Prior task prompt\ntask"
	got := composeSwitchPrompt(prior, "## Host-compiled handoff\n\nnew")
	if strings.Count(got, "## Host-compiled handoff") != 1 {
		t.Fatalf("stacked: %s", got)
	}
	if !strings.Contains(got, "task") || strings.Contains(got, "\nold\n") {
		t.Fatalf("got %q", got)
	}
}

func TestSwitchWorker_PendingAndPayloadBeforeDestroy(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	// Fail create so we stop after post_stop path; pending must already have payload.
	rt := &fakeRuntime{createErr: errors.New("no runtime")}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
		Semantic: domain.SemanticHandoffV1{Objective: "keep going"},
	})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v", err)
	}
	p := st.sessions[id].Metadata.SwitchPending
	if p == nil || p.PayloadJSON == "" {
		t.Fatalf("pending payload missing: %+v", p)
	}
	if !strings.Contains(p.PayloadJSON, "keep going") {
		t.Fatalf("payload missing semantic: %s", p.PayloadJSON)
	}
	if !strings.Contains(st.sessions[id].Metadata.Prompt, "Host-compiled handoff") {
		t.Fatal("composed prompt must be durable on session")
	}
}

func TestRecover_UsesPendingPayloadWithoutPostStop(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	payload := `{"semantic":{"schemaVersion":1,"objective":"from-pending"},"observed":{"schemaVersion":1,"branch":"b","head":"abc","generationId":"src-gen"},"compiled":"## Host-compiled handoff\n\nfrom-pending-compile"}`
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-p", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor", PayloadJSON: payload,
	}
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.Prompt = "## Host-compiled handoff\n\nfrom-pending-compile\n\n## Prior task prompt\nimplement feature"
	st.sessions[id] = rec
	// No post_stop row — only pre_stop (optional).
	st.ledger = []domain.LifecycleLedgerRecord{{
		ID: "mer-1:gen-p:pre_stop", SessionID: id, ProjectID: "mer",
		Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePreStop,
		GenerationID: "gen-p", PayloadJSON: payload,
	}}

	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "should-not-use" },
	})
	m.switchCapsOverride = testSwitchCaps
	res, err := m.RecoverSwitchFromPostStop(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Compiled.Text, "from-pending") && !strings.Contains(res.Session.Metadata.Prompt, "from-pending-compile") {
		t.Fatalf("lost handoff: compiled=%q prompt=%q", res.Compiled.Text, res.Session.Metadata.Prompt)
	}
	if rt.created != 1 {
		t.Fatalf("create=%d", rt.created)
	}
}

func TestRecover_RevalidatesReadOnlyTarget(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	rec := st.sessions[id]
	// RO role but pending target is Pi (no RO).
	rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites = false
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-ro", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessCodex, ToHarness: domain.HarnessPi,
		OriginalTask: "task", RoleID: "orchestrator",
		PayloadJSON: `{"compiled":"## Host-compiled handoff\n\nx"}`,
	}
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[id] = rec

	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		if h == domain.HarnessPi {
			c.SwitchSupported = true // allow switch check so RO is what fails
		}
		return c
	}
	_, err := m.RecoverSwitchFromPostStop(ctx, id)
	if !errors.Is(err, ErrReadOnlyUnsupported) {
		t.Fatalf("err = %v, want ErrReadOnlyUnsupported", err)
	}
}

func TestAllowTerminalInput_BlocksPending(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			SwitchPending: &domain.SwitchPending{GenerationID: "g1", ToHarness: domain.HarnessCodex},
		},
	}
	m := New(Deps{Store: st, Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if err := m.AllowTerminalInput(ctx, "mer-1"); err == nil {
		t.Fatal("expected block")
	}
	if err := m.AllowTerminalInput(ctx, "shell-xyz"); err != nil {
		t.Fatalf("non-session terminal should allow: %v", err)
	}
}
