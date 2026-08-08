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

// testSwitchCaps is a unit-test helper that pins switch_supported for the
// Claude/Codex/fake matrix. Production For() already promotes Claude/Codex;
// the override keeps tests independent of other harness cells and documents
// intent on older test fixtures.
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

func TestSwitchWorker_PausedSessionRefusesBeforeEffects(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	rec := st.sessions[id]
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: "limit-1"}
	st.sessions[id] = rec
	runtime := &fakeRuntime{}
	m := New(Deps{
		Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps

	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPaused) {
		t.Fatalf("err = %v, want ErrSwitchPaused", err)
	}
	if runtime.created != 0 || runtime.destroyed != 0 || len(st.ledger) != 0 || st.updateCount != 0 {
		t.Fatalf("paused refusal had effects: created=%d destroyed=%d ledger=%d updates=%d",
			runtime.created, runtime.destroyed, len(st.ledger), st.updateCount)
	}
}

func TestSwitchWorker_PromotionFailureAfterAckIsRecoverable(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	// pending, clear source, credential rotation, re-pin target, promote.
	st.updateFailAfter = 5
	st.updateErr = errors.New("promotion write failed")
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps

	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPostStop) || !errors.Is(err, st.updateErr) {
		t.Fatalf("err = %v, want ErrSwitchPostStop and promotion cause", err)
	}
	var acked bool
	for _, event := range st.ledger {
		acked = acked || event.Phase == domain.LifecyclePhaseTargetAck
	}
	if !acked || st.sessions[id].Metadata.SwitchPending == nil {
		t.Fatalf("recovery facts lost: acked=%v pending=%+v", acked, st.sessions[id].Metadata.SwitchPending)
	}
}

func TestAckLiveTarget_PromotionFailureKeepsTypedRecovery(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	rec := domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "target", RuntimeLaunchID: "gen-1",
			SwitchPending: &domain.SwitchPending{GenerationID: "gen-1", Kind: domain.LifecycleKindSwitch},
		},
	}
	st.sessions[id] = rec
	st.updateFailAfter = 1
	st.updateErr = errors.New("recovery promotion failed")
	m := New(Deps{Store: st, Clock: time.Now})

	_, err := m.ackLiveTarget(ctx, rec, domain.LifecycleKindSwitch, "gen-1",
		domain.HarnessClaudeCode, domain.HarnessCodex, "opus", "", "implementor", "{}", "", domain.SemanticHandoffV1{}, domain.ObservedWorkspaceV1{})
	if !errors.Is(err, ErrSwitchPostStop) || !errors.Is(err, st.updateErr) {
		t.Fatalf("err = %v, want ErrSwitchPostStop and recovery promotion cause", err)
	}
	if len(st.ledger) != 1 || st.ledger[0].Phase != domain.LifecyclePhaseTargetAck {
		t.Fatalf("target_ack not durable before promotion failure: %+v", st.ledger)
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

// Cross-harness launch must build the authoritative role footer for the *target*
// harness while durable session identity stays on the source until target_ack.
func TestSwitchWorker_SystemPromptTargetHarnessFooter(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps

	// Before ack, durable store must still show source harness during launch.
	// Capture system prompt from the target launch argv path.
	res, err := m.SwitchWorker(ctx, SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "codex-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	sp := agent.lastLaunch.SystemPrompt
	if sp == "" {
		// Switch clears agent session id → fresh launch path.
		sp = agent.lastRestore.SystemPrompt
	}
	if !strings.Contains(sp, "Harness: codex") {
		t.Fatalf("target system prompt missing codex footer:\n%s", sp)
	}
	if strings.Contains(sp, "Harness: claude-code") {
		t.Fatalf("target system prompt still names source harness:\n%s", sp)
	}
	if !strings.Contains(sp, "Active role: implementor") {
		t.Fatalf("role pin lost in footer:\n%s", sp)
	}
	// Durable identity after ack is target; mid-launch durable rec is not re-read here.
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatalf("post-ack harness=%q", res.Session.Harness)
	}
	if res.Session.Metadata.Role.ResolvedHarness != domain.HarnessCodex {
		t.Fatalf("post-ack ResolvedHarness=%q", res.Session.Metadata.Role.ResolvedHarness)
	}
}

func TestSwitchWorker_SystemPromptTargetHarnessFooter_CodexToClaude(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	rec := st.sessions[id]
	rec.Metadata.Role.ResolvedModel = "codex-source"
	st.sessions[id] = rec
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	sp := agent.lastLaunch.SystemPrompt
	if sp == "" {
		sp = agent.lastRestore.SystemPrompt
	}
	if !strings.Contains(sp, "Harness: claude-code") {
		t.Fatalf("want claude-code footer:\n%s", sp)
	}
	if strings.Contains(sp, "Harness: codex") {
		t.Fatalf("source codex footer leaked:\n%s", sp)
	}
}

func TestRecover_SystemPromptTargetHarnessFooter(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	payload := `{"semantic":{"schemaVersion":1,"objective":"recover-footer"},"observed":{"schemaVersion":1},"compiled":"## Host-compiled handoff\n\nrecover-footer"}`
	gen := "gen-footer-rec"
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: gen, Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor", PayloadJSON: payload,
		ToModel: "codex-on-recover", SourceRuntimeHandleID: "rt-1",
	}
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.Metadata.AgentSessionID = ""
	rec.Metadata.Prompt = "## Host-compiled handoff\n\nrecover-footer\n\n## Prior task prompt\nimplement feature"
	st.sessions[id] = rec
	st.ledger = []domain.LifecycleLedgerRecord{{
		ID: string(id) + ":" + gen + ":post_stop", SessionID: id, ProjectID: "mer",
		Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePostStop, GenerationID: gen,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex, PayloadJSON: payload,
	}}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "must-not-mint" },
	})
	m.switchCapsOverride = testSwitchCaps
	res, err := m.RecoverSwitchFromPostStop(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	sp := agent.lastLaunch.SystemPrompt
	if sp == "" {
		sp = agent.lastRestore.SystemPrompt
	}
	if !strings.Contains(sp, "Harness: codex") {
		t.Fatalf("recover target prompt missing codex footer:\n%s", sp)
	}
	if strings.Contains(sp, "Harness: claude-code") {
		t.Fatalf("recover prompt still names source harness:\n%s", sp)
	}
	if res.Session.Metadata.RuntimeLaunchID != gen {
		t.Fatalf("runtime gen=%q want %q", res.Session.Metadata.RuntimeLaunchID, gen)
	}
	// During recover launch, durable pending still present until ack; after success pending cleared.
	if res.Session.Metadata.SwitchPending != nil {
		t.Fatal("pending must clear after recover ack")
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
		destroyErr:    errors.New("cleanup failed"),
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
	// Confirmed-alive: pending rolled back so source remains usable for input.
	if st.sessions[id].Metadata.SwitchPending != nil {
		t.Fatal("pending must be cleared when source is confirmed alive")
	}
	if st.sessions[id].Metadata.Prompt != "implement feature" {
		t.Fatalf("prompt restored: %q", st.sessions[id].Metadata.Prompt)
	}
	if st.sessions[id].Metadata.RuntimeHandleID != "rt-1" {
		t.Fatalf("source handle restored: %q", st.sessions[id].Metadata.RuntimeHandleID)
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
	// Claude/Codex are promoted; Pi still has switch_supported=false.
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessPi})
	if !errors.Is(err, ErrSwitchNotSupported) {
		t.Fatalf("err = %v, want ErrSwitchNotSupported for non-promoted target", err)
	}
}

func TestSwitchWorker_ClaudeCodexPromotedWithoutOverride(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-promo")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	// No switchCapsOverride — production registry must allow Claude↔Codex.
	res, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if err != nil {
		t.Fatalf("promoted Claude→Codex without override: %v", err)
	}
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatalf("harness=%q want codex", res.Session.Harness)
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
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
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
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
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
			RuntimeHandleID: "ao-mer.dots-hash-handle", // sanitized handle ≠ session id
			SwitchPending: &domain.SwitchPending{
				GenerationID: "g1", ToHarness: domain.HarnessCodex,
				SourceRuntimeHandleID: "ao-mer.dots-hash-handle",
			},
		},
	}
	m := New(Deps{Store: st, Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if err := m.AllowTerminalInput(ctx, "mer-1"); err == nil {
		t.Fatal("expected block by session id")
	}
	if err := m.AllowTerminalInput(ctx, "ao-mer.dots-hash-handle"); err == nil {
		t.Fatal("expected block by runtime handle id (normalized/sanitized)")
	}
	if err := m.AllowTerminalInput(ctx, "shell-xyz"); err != nil {
		t.Fatalf("non-session terminal should allow: %v", err)
	}
}

// After source destroy, RuntimeHandleID is cleared; gate must still resolve via
// SwitchPending.SourceRuntimeHandleID (indexed store path).
func TestAllowTerminalInput_BlocksByPendingSourceHandleAfterClear(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "", // cleared after source death
			SwitchPending: &domain.SwitchPending{
				GenerationID: "g-post", ToHarness: domain.HarnessCodex,
				SourceRuntimeHandleID: "ao-mer.dots-hash-handle",
			},
		},
	}
	m := New(Deps{Store: st, Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})
	err := m.AllowTerminalInput(ctx, "ao-mer.dots-hash-handle")
	if err == nil {
		t.Fatal("expected block by pending source handle after runtime handle clear")
	}
	if !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("err = %v, want ErrSwitchInProgress", err)
	}
}

// Confirmed-alive source + rollback UpdateSession failure → ErrSwitchUncertain
// (pending may remain; source usability is uncertain).
func TestSwitchWorker_RollbackFailWrapsErrSwitchUncertain(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	// Switch path: 1× UpdateSession installs pending, then destroy probes alive,
	// then rollback UpdateSession must fail → ErrSwitchUncertain.
	st.updateFailAfter = 2
	st.updateErr = errors.New("disk full on rollback")

	rt := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": true}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchUncertain) {
		t.Fatalf("err = %v, want ErrSwitchUncertain", err)
	}
	if rt.created != 0 {
		t.Fatalf("must not launch target after failed pre-stop: create=%d", rt.created)
	}
	// Pending remains installed (rollback never persisted clear).
	if st.sessions[id].Metadata.SwitchPending == nil {
		t.Fatal("pending should remain when rollback fails")
	}
}

// post_stop ledger append failure must block target launch (and ack).
func TestSwitchWorker_PostStopAppendFailBlocksLaunch(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	st.failLedgerPhase = domain.LifecyclePhasePostStop
	st.appendLedgerErr = errors.New("ledger disk full")

	rt := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": false}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v, want ErrSwitchPostStop", err)
	}
	if rt.created != 0 {
		t.Fatalf("must not launch target without durable post_stop: create=%d", rt.created)
	}
	// Source is dead with pending retained for recovery.
	if st.sessions[id].Metadata.SwitchPending == nil {
		t.Fatal("pending must remain for recovery after post_stop fail")
	}
	for _, e := range st.ledger {
		if e.Phase == domain.LifecyclePhaseTargetAck {
			t.Fatal("must not ack without post_stop")
		}
	}
}

// Recovery path: post_stop append fail also blocks launch.
func TestRecover_PostStopAppendFailBlocksLaunch(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	payload := `{"semantic":{"schemaVersion":1,"objective":"o"},"observed":{"schemaVersion":1},"compiled":"## Host-compiled handoff\n\nx"}`
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-ps-fail", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor", PayloadJSON: payload,
		SourceRuntimeHandleID: "rt-1",
	}
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	st.sessions[id] = rec
	st.ledger = []domain.LifecycleLedgerRecord{
		{ID: "mer-1:gen-ps-fail:pre_stop", SessionID: id, ProjectID: "mer", Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePreStop, GenerationID: "gen-ps-fail", PayloadJSON: payload},
	}
	st.failLedgerPhase = domain.LifecyclePhasePostStop
	st.appendLedgerErr = errors.New("ledger unavailable")

	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "gen-ps-fail" },
	})
	m.switchCapsOverride = testSwitchCaps
	_, err := m.RecoverSwitchFromPostStop(ctx, id)
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v, want ErrSwitchPostStop", err)
	}
	if rt.created != 0 {
		t.Fatalf("must not launch without post_stop: create=%d", rt.created)
	}
}

func TestRecover_EnsuresPostStopBeforeLaunch(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	payload := `{"semantic":{"schemaVersion":1,"objective":"o"},"observed":{"schemaVersion":1},"compiled":"## Host-compiled handoff\n\nx"}`
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-ps", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		OriginalTask: "implement feature", RoleID: "implementor", PayloadJSON: payload,
		SourceRuntimeHandleID: "rt-1",
	}
	// Source already dead, handles cleared; no post_stop row yet.
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	st.sessions[id] = rec
	st.ledger = []domain.LifecycleLedgerRecord{
		{ID: "mer-1:gen-ps:requested", SessionID: id, ProjectID: "mer", Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhaseRequested, GenerationID: "gen-ps"},
		{ID: "mer-1:gen-ps:pre_stop", SessionID: id, ProjectID: "mer", Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePreStop, GenerationID: "gen-ps", PayloadJSON: payload},
	}

	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "gen-ps" },
	})
	m.switchCapsOverride = testSwitchCaps
	if _, err := m.RecoverSwitchFromPostStop(ctx, id); err != nil {
		t.Fatal(err)
	}
	var sawPost, sawAck bool
	for _, e := range st.ledger {
		if e.GenerationID != "gen-ps" {
			continue
		}
		if e.Phase == domain.LifecyclePhasePostStop {
			sawPost = true
		}
		if e.Phase == domain.LifecyclePhaseTargetAck {
			sawAck = true
		}
	}
	if !sawPost || !sawAck {
		t.Fatalf("want post_stop then target_ack, post=%v ack=%v ledger=%+v", sawPost, sawAck, st.ledger)
	}
	// Ordering: post_stop index before target_ack
	postIdx, ackIdx := -1, -1
	for i, e := range st.ledger {
		if e.GenerationID == "gen-ps" && e.Phase == domain.LifecyclePhasePostStop {
			postIdx = i
		}
		if e.GenerationID == "gen-ps" && e.Phase == domain.LifecyclePhaseTargetAck {
			ackIdx = i
		}
	}
	if postIdx < 0 || ackIdx < 0 || postIdx > ackIdx {
		t.Fatalf("post_stop must precede target_ack: post=%d ack=%d", postIdx, ackIdx)
	}
}
