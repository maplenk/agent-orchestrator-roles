package sessionmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
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
				ResolvedHarness: harness, ResolvedModel: "model-a",
				ResolvedPermissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
			},
		},
	}
}

func TestSwitchWorker_ClaudeToCodex_LedgerPhases(t *testing.T) {
	st := newFakeStore()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o750); err != nil {
		t.Fatal(err)
	}
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	rt := &fakeRuntime{}
	agent := &recordingAgent{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "tgt-gen" },
	})

	res, err := m.SwitchWorker(ctx, SwitchRequest{
		SessionID:     id,
		TargetHarness: domain.HarnessCodex,
		Semantic:      domain.SemanticHandoffV1{Objective: "keep going"},
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want codex", res.Session.Harness)
	}
	if res.Kind != domain.LifecycleKindSwitch {
		t.Fatalf("kind = %q", res.Kind)
	}
	if rt.destroyed != 1 || rt.created != 1 {
		t.Fatalf("destroy=%d create=%d", rt.destroyed, rt.created)
	}
	wantPhases := []domain.LifecycleLedgerPhase{
		domain.LifecyclePhaseRequested,
		domain.LifecyclePhasePreStop,
		domain.LifecyclePhasePostStop,
		domain.LifecyclePhaseTargetAck,
	}
	if len(st.ledger) != len(wantPhases) {
		t.Fatalf("ledger len = %d, want %d: %+v", len(st.ledger), len(wantPhases), st.ledger)
	}
	for i, p := range wantPhases {
		if st.ledger[i].Phase != p {
			t.Fatalf("ledger[%d].phase = %q, want %q", i, st.ledger[i].Phase, p)
		}
	}
	if !strings.Contains(res.Session.Metadata.Prompt, "Host-compiled handoff") {
		t.Fatalf("prompt missing handoff:\n%s", res.Session.Metadata.Prompt)
	}
	if !strings.Contains(res.Session.Metadata.Prompt, "implement feature") {
		t.Fatalf("prompt should retain prior task:\n%s", res.Session.Metadata.Prompt)
	}
	if agent.launchCalls < 1 {
		t.Fatal("expected launch after switch")
	}
	if res.Session.Metadata.Role.ResolvedHarness != domain.HarnessCodex {
		t.Fatalf("role pin harness = %q", res.Session.Metadata.Role.ResolvedHarness)
	}
}

func TestSwitchWorker_ConcurrentFence(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	id := domain.SessionID("mer-1")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Branch: "b", WorkspacePath: ws, RuntimeHandleID: "rt-1", Prompt: "t"},
	}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	if !m.beginSwitch(id) {
		t.Fatal("first beginSwitch failed")
	}
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchInProgress) {
		t.Fatalf("err = %v, want ErrSwitchInProgress", err)
	}
	m.endSwitch(id)
}

func TestFreshConversation_SameHarness(t *testing.T) {
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
	res, err := m.FreshConversation(ctx, id, domain.SemanticHandoffV1{Objective: "refresh"})
	if err != nil {
		t.Fatalf("fresh: %v", err)
	}
	if res.Kind != domain.LifecycleKindFreshConversation {
		t.Fatalf("kind = %q", res.Kind)
	}
	if res.Session.Harness != domain.HarnessCodex {
		t.Fatalf("harness changed: %q", res.Session.Harness)
	}
	if !strings.Contains(res.Compiled.Text, "fresh conversation") {
		t.Fatalf("compiled:\n%s", res.Compiled.Text)
	}
}

func TestSwitchWorker_RejectsOrchestrator(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	id := domain.SessionID("mer-orch")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir(), RuntimeHandleID: "rt"},
	}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessClaudeCode})
	if !errors.Is(err, ErrNotWorker) {
		t.Fatalf("err = %v, want ErrNotWorker", err)
	}
}

func TestSwitchWorker_PostStopRetainsHandoffOnRelaunchFailure(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)

	rt := &fakeRuntime{createErr: errors.New("runtime down")}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v, want ErrSwitchPostStop", err)
	}
	var sawPost bool
	for _, e := range st.ledger {
		if e.Phase == domain.LifecyclePhasePostStop {
			sawPost = true
			if !strings.Contains(e.PayloadJSON, "compiled") {
				t.Fatalf("post_stop payload missing handoff: %s", e.PayloadJSON)
			}
		}
	}
	if !sawPost {
		t.Fatal("expected post_stop ledger entry")
	}
}

func TestSwitchWorker_RejectsPi(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	id := domain.SessionID("mer-1")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessPi,
		Metadata: domain.SessionMetadata{WorkspacePath: ws, RuntimeHandleID: "rt", Prompt: "t"},
	}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
	if !errors.Is(err, ErrSwitchNotSupported) {
		t.Fatalf("err = %v, want ErrSwitchNotSupported", err)
	}
}