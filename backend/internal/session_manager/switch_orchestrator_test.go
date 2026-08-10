package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles"
)

// 2B-1: the orchestrator is the longest-lived session in a project and the
// worst context-exhaustion offender, so it needs a fresh conversation without
// losing what it was coordinating. In-place is what makes that safe — the
// session id, canonical worktree and branch never change, so live workers
// (whose system prompts embed the orchestrator id at spawn/restore only) keep
// addressing a coordinator that still exists.

func orchestratorSwitchHarness(t *testing.T) (*Manager, *fakeStore, domain.SessionID) {
	t.Helper()
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessCodex, ws, art, sha)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	rec.Metadata.Role.RoleID = "orchestrator"
	rec.Metadata.Role.ResolvedPermissions = domain.RoleExecutionPolicy{WorkspaceWrites: true, CanSpawn: true}
	rec.Metadata.SpawnCapabilityHash = "source-capability-hash"
	st.sessions[id] = rec
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{RoleMap: domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			StrictDelegation: true,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {
					Template: "orchestrator",
					Harness:  domain.HarnessClaudeCode,
					Permissions: domain.RoleExecutionPolicy{
						WorkspaceWrites: true,
						CanSpawn:        true,
					},
				},
			},
			Failover: domain.FailoverConfig{
				Mode: domain.FailoverModeManual,
				Roles: map[string][]domain.FailoverTarget{
					"orchestrator": {{Harness: domain.HarnessCodex}},
				},
			},
		}},
	}

	m := New(Deps{
		Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}},
		Agents:  singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	return m, st, id
}

type harnessAgentResolver map[domain.AgentHarness]ports.Agent

func (r harnessAgentResolver) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := r[harness]
	return agent, ok
}

// tmuxPreflightRuntime subjects every target RuntimeConfig to the real tmux
// adapter's 15,360-byte launch-command preflight. /usr/bin/false is reached
// only after that preflight passes; its ordinary execution error is ignored so
// the existing deterministic fake can model the rest of a successful launch.
type tmuxPreflightRuntime struct {
	*fakeRuntime
	preflight *tmux.Runtime
	attempts  []ports.RuntimeConfig
}

func (r *tmuxPreflightRuntime) Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	r.attempts = append(r.attempts, cfg)
	if _, err := r.preflight.Create(ctx, cfg); errors.Is(err, ports.ErrRuntimeLaunchCommandTooLong) {
		return ports.RuntimeHandle{}, err
	}
	return r.fakeRuntime.Create(ctx, cfg)
}

func pinAcceptanceOrchestratorTemplate(t *testing.T, st *fakeStore) (string, string, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "profiles", "orchestrator.md"))
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := roles.ParseTemplate(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutTemplateArtifact(ctx, tmpl.ArtifactID, tmpl.SHA256, raw, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return tmpl.ArtifactID, tmpl.SHA256, raw
}

// TestFreshOrchestratorConversation_KeepsIdentityInPlace is the core of 2B-1.
// A successor-session shape would strand every live worker, because a worker's
// system prompt embeds its orchestrator's id and is recomputed only at
// spawn/restore.
func TestFreshOrchestratorConversation_KeepsIdentityInPlace(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	before := st.sessions[id]

	res, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{
		Objective: "keep coordinating",
	})
	if err != nil {
		t.Fatalf("orchestrator fresh: %v", err)
	}
	if res.Session.ID != id {
		t.Fatalf("session id changed to %s: live workers address the old id", res.Session.ID)
	}
	if res.Session.Kind != domain.KindOrchestrator {
		t.Fatalf("kind = %s, want orchestrator", res.Session.Kind)
	}
	after := st.sessions[id]
	if after.Metadata.WorkspacePath != before.Metadata.WorkspacePath {
		t.Errorf("workspace moved: %q -> %q (canonical per project)",
			before.Metadata.WorkspacePath, after.Metadata.WorkspacePath)
	}
	if after.Metadata.Branch != before.Metadata.Branch {
		t.Errorf("branch moved: %q -> %q", before.Metadata.Branch, after.Metadata.Branch)
	}
	if after.IsTerminated {
		t.Error("orchestrator left terminated after a successful fresh conversation")
	}
	if res.Kind != domain.LifecycleKindOrchestratorFresh {
		t.Errorf("ledger kind = %q, want %q", res.Kind, domain.LifecycleKindOrchestratorFresh)
	}
}

// TestFreshOrchestratorConversation_DoesNotHoldTheFenceWhileWaitingForTheGate
// is the test that actually distinguishes lock ORDER.
//
// Merely showing that the operation blocks while the gate is held proves
// nothing: an inverted implementation (beginSwitch first, then wait on the
// gate) blocks identically. The difference is observable only in what is held
// WHILE blocked. Correct order waits on the gate holding no fence, so the
// per-session fence is still free; inverted order holds the fence while
// waiting, which is the same-project deadlock this ordering exists to prevent.
func TestFreshOrchestratorConversation_DoesNotHoldTheFenceWhileWaitingForTheGate(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	release, err := m.acquireProjectOwnership(context.Background(), st.sessions[id].ProjectID)
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
		done <- err
	}()

	// Let it reach whichever lock it takes first.
	select {
	case <-done:
		t.Fatal("orchestrator fresh completed while the project gate was held")
	case <-time.After(150 * time.Millisecond):
	}

	// The switch fence must still be free. beginSwitch returning true proves it
	// was NOT taken before the gate.
	if !m.beginSwitch(id) {
		t.Fatal("the switch fence is held while blocked on the project gate: locks are taken " +
			"beginSwitch -> projectOwnership, the inversion that deadlocks recovery for the same project")
	}
	m.endSwitch(id)
}

func TestSwitchOrchestrator_TakesProjectGateBeforeSwitchFence(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	release, err := m.acquireProjectOwnership(context.Background(), st.sessions[id].ProjectID)
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		_, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
			SessionID: id, TargetHarness: domain.HarnessClaudeCode,
		})
		done <- err
	}()

	select {
	case <-done:
		t.Fatal("orchestrator switch completed while the project gate was held")
	case <-time.After(150 * time.Millisecond):
	}
	if !m.beginSwitch(id) {
		t.Fatal("switch fence held while waiting for project ownership: lock order is inverted")
	}
	m.endSwitch(id)
}

// TestFreshOrchestratorConversation_TakesProjectGateBeforeSwitchFence pins that
// the gate is taken at all. Ordering is covered separately above.
func TestFreshOrchestratorConversation_TakesProjectGateBeforeSwitchFence(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	release, err := m.acquireProjectOwnership(context.Background(), st.sessions[id].ProjectID)
	if err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
		done <- err
	}()

	select {
	case <-done:
		t.Fatal("orchestrator fresh ran while another owner held the project gate")
	case <-time.After(150 * time.Millisecond):
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("after release: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("orchestrator fresh never completed after the gate was released")
	}
}

// TestFreshOrchestratorConversation_RejectsWorker: the entry points are not
// interchangeable, because they take different locks.
func TestFreshOrchestratorConversation_RejectsWorker(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Kind = domain.KindWorker
	st.sessions[id] = rec

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{}); !errors.Is(err, ErrNotOrchestrator) {
		t.Fatalf("err = %v, want ErrNotOrchestrator", err)
	}
}

// TestSwitchWorker_RejectsOrchestrator is the other half: an orchestrator must
// not enter through the worker path, which would take beginSwitch WITHOUT the
// project gate and so invert the lock order.
func TestSwitchWorker_RejectsOrchestrator(t *testing.T) {
	m, _, id := orchestratorSwitchHarness(t)
	_, err := m.SwitchWorker(context.Background(), SwitchRequest{SessionID: id, FreshConversation: true})
	if !errors.Is(err, ErrNotWorker) {
		t.Fatalf("err = %v, want ErrNotWorker: orchestrators must gate first", err)
	}
}

func TestSwitchOrchestrator_CodexClaudeInPlacePreservesIdentityAndRotatesCredential(t *testing.T) {
	for _, tc := range []struct {
		name string
		from domain.AgentHarness
		to   domain.AgentHarness
	}{
		{name: "codex to claude", from: domain.HarnessCodex, to: domain.HarnessClaudeCode},
		{name: "claude to codex", from: domain.HarnessClaudeCode, to: domain.HarnessCodex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, id := orchestratorSwitchHarness(t)
			st.sessions["mer-worker"] = domain.SessionRecord{
				ID: "mer-worker", ProjectID: "mer", Kind: domain.KindWorker,
				Harness:  domain.HarnessCodex,
				Activity: domain.Activity{State: domain.ActivityActive},
				Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{RoleID: "implementor"}},
			}
			rec := st.sessions[id]
			rec.Harness = tc.from
			rec.Metadata.Role.ResolvedHarness = tc.from
			st.sessions[id] = rec
			before := rec

			res, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
				SessionID: id, TargetHarness: tc.to,
			})
			if err != nil {
				t.Fatalf("switch orchestrator: %v", err)
			}
			after := st.sessions[id]
			if res.Kind != domain.LifecycleKindSwitch {
				t.Fatalf("kind = %q, want existing switch ledger kind", res.Kind)
			}
			if after.ID != before.ID || after.ProjectID != before.ProjectID || after.Kind != before.Kind {
				t.Fatalf("durable identity drifted: before=%+v after=%+v", before, after)
			}
			if after.Metadata.WorkspacePath != before.Metadata.WorkspacePath || after.Metadata.Branch != before.Metadata.Branch {
				t.Fatalf("canonical workspace drifted: before=%+v after=%+v", before.Metadata, after.Metadata)
			}
			if after.Metadata.Role.RoleID != before.Metadata.Role.RoleID ||
				after.Metadata.Role.TemplateArtifactID != before.Metadata.Role.TemplateArtifactID ||
				after.Metadata.Role.TemplateSHA256 != before.Metadata.Role.TemplateSHA256 ||
				after.Metadata.Role.ResolvedPermissions != before.Metadata.Role.ResolvedPermissions {
				t.Fatalf("role/template/permissions drifted: before=%+v after=%+v", before.Metadata.Role, after.Metadata.Role)
			}
			if !after.Metadata.Role.ResolvedPermissions.CanSpawn {
				t.Fatal("CanSpawn was not preserved")
			}
			if after.Harness != tc.to || after.Metadata.Role.ResolvedHarness != tc.to {
				t.Fatalf("target not promoted: session=%q role=%q", after.Harness, after.Metadata.Role.ResolvedHarness)
			}
			if before.Metadata.Role.ResolvedModel == "" {
				t.Fatal("fixture must carry a non-empty source model to prove cross-provider clearing")
			}
			if after.Metadata.Role.ResolvedModel != "" {
				t.Fatalf("source model leaked across harnesses: %q", after.Metadata.Role.ResolvedModel)
			}
			if after.Metadata.SpawnCapabilityHash == "" || after.Metadata.SpawnCapabilityHash == before.Metadata.SpawnCapabilityHash {
				t.Fatalf("spawn credential did not rotate: before=%q after=%q",
					before.Metadata.SpawnCapabilityHash, after.Metadata.SpawnCapabilityHash)
			}
			wantPhases := []domain.LifecycleLedgerPhase{
				domain.LifecyclePhaseRequested,
				domain.LifecyclePhasePreStop,
				domain.LifecyclePhasePostStop,
				domain.LifecyclePhaseTargetAck,
			}
			if len(st.ledger) != len(wantPhases) {
				t.Fatalf("ledger = %+v, want exactly four switch phases", st.ledger)
			}
			for i, want := range wantPhases {
				if st.ledger[i].Kind != domain.LifecycleKindSwitch || st.ledger[i].Phase != want ||
					st.ledger[i].GenerationID != res.GenerationID {
					t.Fatalf("ledger[%d] = %+v, want switch/%s generation %s", i, st.ledger[i], want, res.GenerationID)
				}
			}
			runtime := m.runtime.(*fakeRuntime)
			if runtime.created != 1 || runtime.destroyed != 1 {
				t.Fatalf("runtime create/destroy = %d/%d, want one target and one source", runtime.created, runtime.destroyed)
			}
			agent := m.agents.(singleAgent).agent.(*recordingAgent)
			combinedPrompt := agent.lastLaunch.SystemPrompt + "\n" + agent.lastLaunch.Prompt
			if !strings.Contains(agent.lastLaunch.SystemPrompt, "Harness: "+string(tc.to)+".") {
				t.Fatalf("authoritative role footer does not name target %q:\n%s", tc.to, agent.lastLaunch.SystemPrompt)
			}
			if strings.Count(combinedPrompt, "## Host-compiled handoff") != 1 ||
				strings.Count(combinedPrompt, "### Observed fleet") != 1 ||
				strings.Count(combinedPrompt, "mer-worker") != 1 {
				t.Fatalf("orchestrator roster/handoff was omitted or stacked:\n%s", combinedPrompt)
			}
		})
	}
}

func TestSwitchOrchestrator_LegacySessionAdoptsStarterRoleAtRelaunch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orchestrator.md"), []byte("---\nid: orchestrator\nname: Orchestrator\n---\n# Default role\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	m, st, id := orchestratorSwitchHarness(t)
	project := st.projects["mer"]
	project.Config.RoleMap = domain.StarterRoleMap(
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode},
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode},
	)
	st.projects["mer"] = project
	rec := st.sessions[id]
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.Role = domain.SessionRoleBinding{}
	st.sessions[id] = rec

	res, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	role := res.Session.Metadata.Role
	if role.RoleID != domain.DefaultOrchestratorRoleID || role.ResolvedHarness != domain.HarnessCodex || !role.ResolvedPermissions.CanSpawn || !role.ResolvedPermissions.WorkspaceWrites {
		t.Fatalf("adopted role = %+v", role)
	}
	if role.RoleMapSHA256 == "" || role.TemplateArtifactID == "" || role.TemplateSHA256 == "" {
		t.Fatalf("incomplete durable role pin = %+v", role)
	}
	if _, _, ok, err := st.GetTemplateArtifact(context.Background(), role.TemplateArtifactID); err != nil || !ok {
		t.Fatalf("template artifact = %v, %v", ok, err)
	}
	if res.Session.Metadata.SwitchPending != nil {
		t.Fatalf("switch pending not cleared: %+v", res.Session.Metadata.SwitchPending)
	}
}

func TestSwitchOrchestrator_LegacyRoleAdoptionRollsBackWithLiveSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "orchestrator.md"), []byte("---\nid: orchestrator\nname: Orchestrator\n---\n# Default role\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testTemplateLoader = roles.NewLoader(roles.NewArtifactStore(), dir)
	t.Cleanup(func() { testTemplateLoader = nil })

	m, st, id := orchestratorSwitchHarness(t)
	project := st.projects["mer"]
	project.Config.RoleMap = domain.StarterRoleMap(
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode},
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode},
	)
	st.projects["mer"] = project
	rec := st.sessions[id]
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.Role = domain.SessionRoleBinding{}
	st.sessions[id] = rec
	runtime := m.runtime.(*fakeRuntime)
	runtime.aliveByHandle[rec.Metadata.RuntimeHandleID] = true
	runtime.destroyLeavesAlive = true

	if _, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	}); err == nil {
		t.Fatal("switch unexpectedly succeeded while source remained alive")
	}
	after := st.sessions[id]
	if rolePinHasAnyField(after.Metadata.Role) {
		t.Fatalf("failed pre-stop switch left an unapplied role pin: %+v", after.Metadata.Role)
	}
	if after.Metadata.SwitchPending != nil || after.Metadata.RuntimeHandleID != rec.Metadata.RuntimeHandleID {
		t.Fatalf("source usability not restored: %+v", after.Metadata)
	}
}

// This is the live strict-role specimen: the repository's full orchestrator
// template (kept at least as large as the original 3,885-byte specimen), one
// worker in the observed roster, and an immediate
// Codex -> Claude -> Codex roundtrip. The second handoff is large enough that
// inlining Codex developer_instructions crosses tmux's real 15,360-byte
// preflight. Codex already supports model_instructions_file, so the target must
// launch from the manager-owned file and durably acknowledge the same saga.
func TestSwitchOrchestrator_CodexClaudeCodexRoundTripFitsTmuxCommandBudget(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	artifactID, templateSHA, templateRaw := pinAcceptanceOrchestratorTemplate(t, st)
	if len(templateRaw) < 3885 {
		t.Fatalf("acceptance orchestrator template size = %d, want at least the original 3885-byte pressure", len(templateRaw))
	}
	rec := st.sessions[id]
	rec.Metadata.Prompt = "Hold for deterministic MVP acceptance."
	rec.Metadata.Role.TemplateArtifactID = artifactID
	rec.Metadata.Role.TemplateSHA256 = templateSHA
	st.sessions[id] = rec
	st.sessions["mer-worker"] = domain.SessionRecord{
		ID: "mer-worker", ProjectID: rec.ProjectID, Kind: domain.KindWorker,
		Harness: domain.HarnessClaudeCode, Activity: domain.Activity{State: domain.ActivityIdle},
		Metadata: domain.SessionMetadata{
			Branch: "ao/mer-worker/root",
			Role:   domain.SessionRoleBinding{RoleID: "implementor"},
		},
	}

	binDir := t.TempDir()
	codexBinary := filepath.Join(binDir, "codex")
	if err := os.WriteFile(codexBinary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	m.agents = harnessAgentResolver{
		domain.HarnessClaudeCode: &recordingAgent{},
		domain.HarnessCodex:      codex.New(),
	}
	m.dataDir = t.TempDir()
	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{}}
	runtime := &tmuxPreflightRuntime{
		fakeRuntime: baseRuntime,
		preflight:   tmux.New(tmux.Options{Binary: "/usr/bin/false", Shell: "/bin/sh"}),
	}
	m.runtime = runtime

	first, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if err != nil {
		t.Fatalf("Codex -> Claude: %v", err)
	}
	second, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("Claude -> Codex: %v", err)
	}
	if first.GenerationID == second.GenerationID || second.Session.Harness != domain.HarnessCodex {
		t.Fatalf("roundtrip generations/target = %q -> %q, harness %q",
			first.GenerationID, second.GenerationID, second.Session.Harness)
	}
	if runtime.created != 2 || runtime.destroyed != 2 || len(runtime.attempts) != 2 {
		t.Fatalf("runtime creates/destroys/attempts = %d/%d/%d, want 2/2/2",
			runtime.created, runtime.destroyed, len(runtime.attempts))
	}

	codexCfg := runtime.attempts[1]
	var instructionFile string
	for _, arg := range codexCfg.Argv {
		if strings.HasPrefix(arg, "model_instructions_file=") {
			instructionFile = strings.TrimPrefix(arg, "model_instructions_file=")
		}
		if strings.HasPrefix(arg, "developer_instructions=") {
			t.Fatalf("Codex target inlined the full system prompt into argv: %.120s", arg)
		}
	}
	if instructionFile == "" {
		t.Fatalf("Codex target argv does not use model_instructions_file: %#v", codexCfg.Argv)
	}
	instructions, err := os.ReadFile(instructionFile)
	if err != nil {
		t.Fatalf("read Codex target instruction file: %v", err)
	}
	if !strings.Contains(string(instructions), "Harness: codex.") ||
		!strings.Contains(string(instructions), "## Role") {
		t.Fatalf("Codex target instruction file lost role authority: %.500s", instructions)
	}
	after := st.sessions[id]
	if after.Metadata.SwitchPending != nil || after.Metadata.RuntimeLaunchID != second.GenerationID {
		t.Fatalf("roundtrip did not acknowledge/promote target: %+v", after.Metadata)
	}
	if strings.Count(after.Metadata.Prompt, "## Host-compiled handoff") != 1 ||
		strings.Count(after.Metadata.Prompt, "Hold for deterministic MVP acceptance.") != 1 {
		t.Fatalf("roundtrip stacked or lost the task handoff:\n%s", after.Metadata.Prompt)
	}
	if len(st.ledger) != 8 || st.ledger[7].Phase != domain.LifecyclePhaseTargetAck ||
		st.ledger[7].GenerationID != second.GenerationID {
		t.Fatalf("roundtrip ledger did not ack both targets: %+v", st.ledger)
	}
}

// Once the source is stopped, the manager intentionally joins the generic
// recovery state with the exact target-launch cause. Both identities are
// load-bearing: recovery uses ErrSwitchPostStop, while the API must still give
// the operator the command-size remedy instead of hiding it.
func TestSwitchOrchestrator_PostStopCommandTooLongPreservesBothErrorIdentities(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	m.agents = singleAgent{agent: launchArgvAgent{argv: []string{
		"target-agent", strings.Repeat("oversized-role-and-handoff", 900),
	}}}
	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m.runtime = &tmuxPreflightRuntime{
		fakeRuntime: baseRuntime,
		preflight:   tmux.New(tmux.Options{Binary: "/usr/bin/false", Shell: "/bin/sh"}),
	}

	_, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if !errors.Is(err, ErrSwitchPostStop) || !errors.Is(err, ports.ErrRuntimeLaunchCommandTooLong) {
		t.Fatalf("error identities = %v, want ErrSwitchPostStop + ErrRuntimeLaunchCommandTooLong", err)
	}
	rec := st.sessions[id]
	if rec.Metadata.SwitchPending == nil || rec.Metadata.RuntimeHandleID != "" {
		t.Fatalf("post-stop handoff not retained: %+v", rec.Metadata)
	}
	if baseRuntime.destroyed != 1 || baseRuntime.created != 0 {
		t.Fatalf("source destroys/target creates = %d/%d, want 1/0",
			baseRuntime.destroyed, baseRuntime.created)
	}
}

func TestSwitchOrchestrator_RejectsUnauthorizedExactModelBeforeEffects(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	_, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode, TargetModel: "not-authorized",
	})
	if !errors.Is(err, domain.ErrSwitchTargetUnauthorized) {
		t.Fatalf("err = %v, want domain.ErrSwitchTargetUnauthorized", err)
	}
	if len(st.ledger) != 0 || m.runtime.(*fakeRuntime).destroyed != 0 {
		t.Fatalf("unauthorized target reached effects: ledger=%+v runtime=%+v", st.ledger, m.runtime)
	}
}

func TestSwitchOrchestrator_RejectsRolelessOrchestratorBeforeEffects(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Metadata.Role = domain.SessionRoleBinding{}
	st.sessions[id] = rec
	_, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if !errors.Is(err, domain.ErrSwitchTargetUnauthorized) {
		t.Fatalf("err = %v, want domain.ErrSwitchTargetUnauthorized", err)
	}
	if len(st.ledger) != 0 || m.runtime.(*fakeRuntime).destroyed != 0 {
		t.Fatalf("roleless target reached effects: ledger=%+v runtime=%+v", st.ledger, m.runtime)
	}
}

func TestSwitchOrchestrator_ExplicitReadOnlyRoleRejectsClaudeBeforeEffects(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Harness = domain.HarnessCodex
	rec.Metadata.Role.ResolvedHarness = domain.HarnessCodex
	rec.Metadata.Role.ResolvedPermissions.WorkspaceWrites = false
	st.sessions[id] = rec

	_, err := m.SwitchOrchestrator(context.Background(), SwitchRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if !errors.Is(err, ErrReadOnlyUnsupported) {
		t.Fatalf("err = %v, want ErrReadOnlyUnsupported", err)
	}
	if len(st.ledger) != 0 || m.runtime.(*fakeRuntime).destroyed != 0 {
		t.Fatalf("read-only target reached effects: ledger=%+v runtime=%+v", st.ledger, m.runtime)
	}
}

// TestSwitchWorker_RejectsOrchestratorEvenWhenTheGuardReadFails is the
// fail-closed half. The kind check must sit on the AUTHORITATIVE read inside
// the saga, not on a pre-read that has to guess when it errors: a pre-read that
// falls through on error skips the gate at exactly the moment it learned
// nothing.
func TestSwitchWorker_RejectsOrchestratorEvenWhenTheGuardReadFails(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	// The old implementation did a throwaway GetSession first and ignored its
	// error; nothing here makes that read succeed for it to key on.
	_ = st

	_, err := m.SwitchWorker(context.Background(), SwitchRequest{SessionID: id, FreshConversation: true})
	if !errors.Is(err, ErrNotWorker) {
		t.Fatalf("err = %v, want ErrNotWorker: an orchestrator on the worker entry point would hold "+
			"beginSwitch without the project gate", err)
	}
	// And it must be refused BEFORE any saga work: no ledger event, no stop.
	if len(st.ledger) != 0 {
		t.Errorf("the saga started for an ungated orchestrator: %d ledger events", len(st.ledger))
	}
}

// Recovery re-drives a durable record, but the pending pin is not
// authorization: removing the target from the role map while the daemon is
// down must fail before probing or launching anything.
func TestRecoverSwitch_ReauthorizesCrossHarnessOrchestratorPendingTarget(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-x", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessCodex, ToHarness: domain.HarnessClaudeCode,
	}
	rec.Metadata.RuntimeHandleID = ""
	st.sessions[id] = rec
	project := st.projects["mer"]
	roleMap := project.Config.RoleMap
	roleMap.Failover.Roles["orchestrator"] = nil
	binding := roleMap.Roles["orchestrator"]
	binding.Harness = domain.HarnessCodex
	roleMap.Roles["orchestrator"] = binding
	project.Config.RoleMap = roleMap
	st.projects["mer"] = project

	_, err := m.RecoverSwitchFromPostStop(context.Background(), id)
	if !errors.Is(err, domain.ErrSwitchTargetUnauthorized) {
		t.Fatalf("err = %v, want target authorization failure", err)
	}
	if m.runtime.(*fakeRuntime).created != 0 {
		t.Fatal("unauthorized recovery launched a target")
	}
}

func TestRecoverSwitch_CrossHarnessOrchestratorUsesSameGenerationAndOneOwner(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	rec := st.sessions[id]
	const compiled = "## Host-compiled handoff\n\n### Observed fleet (host; authoritative over any recollection of workers)\n- mer-worker"
	const payload = `{"semantic":{"schemaVersion":1},"observed":{"schemaVersion":1,"generationId":"src-gen"},"compiled":"## Host-compiled handoff\n\n### Observed fleet (host; authoritative over any recollection of workers)\n- mer-worker"}`
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-recover", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessCodex, ToHarness: domain.HarnessClaudeCode,
		RoleID: "orchestrator", SourceRuntimeHandleID: "rt-1",
		PayloadJSON: payload,
	}
	rec.Metadata.Prompt = composeSwitchPrompt("coordinate", compiled)
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	st.sessions[id] = rec

	res, err := m.RecoverSwitchFromPostStop(context.Background(), id)
	if err != nil {
		t.Fatalf("recover orchestrator switch: %v", err)
	}
	if res.GenerationID != "gen-recover" || res.Session.Metadata.RuntimeLaunchID != "gen-recover" {
		t.Fatalf("generation changed during recovery: result=%q runtime=%q",
			res.GenerationID, res.Session.Metadata.RuntimeLaunchID)
	}
	if res.Session.Harness != domain.HarnessClaudeCode || res.Session.Metadata.SwitchPending != nil {
		t.Fatalf("target not promoted after recovery: %+v", res.Session)
	}
	if m.runtime.(*fakeRuntime).created != 1 {
		t.Fatalf("target runtime creates = %d, want exactly one", m.runtime.(*fakeRuntime).created)
	}
	agent := m.agents.(singleAgent).agent.(*recordingAgent)
	combinedPrompt := agent.lastLaunch.SystemPrompt + "\n" + agent.lastLaunch.Prompt
	if !strings.Contains(agent.lastLaunch.SystemPrompt, "Harness: claude-code.") {
		t.Fatalf("recovered target footer does not name Claude:\n%s", agent.lastLaunch.SystemPrompt)
	}
	if strings.Count(combinedPrompt, "## Host-compiled handoff") != 1 ||
		strings.Count(combinedPrompt, "### Observed fleet") != 1 ||
		strings.Count(combinedPrompt, "mer-worker") != 1 {
		t.Fatalf("recovery omitted or stacked the durable orchestrator handoff:\n%s", combinedPrompt)
	}
	activeOwners := 0
	for _, got := range st.sessions {
		if got.ProjectID == "mer" && got.Kind == domain.KindOrchestrator && !got.IsTerminated {
			activeOwners++
		}
	}
	if activeOwners != 1 {
		t.Fatalf("active orchestrators = %d, want exactly one", activeOwners)
	}
}

// TestIsSwitchLedgerKind_CoversEverySagaKind: findPhasePayload,
// findRecoverablePostStop and hasIncompletePostStop all filter through this, so
// an omitted kind silently disables the ledger fallback — the path that exists
// for when the pending pin is gone.
func TestIsSwitchLedgerKind_CoversEverySagaKind(t *testing.T) {
	for _, k := range []domain.LifecycleLedgerKind{
		domain.LifecycleKindSwitch,
		domain.LifecycleKindFreshConversation,
		domain.LifecycleKindOrchestratorFresh,
	} {
		if !isSwitchLedgerKind(k) {
			t.Errorf("%q is appended by the switch saga but invisible to recovery", k)
		}
	}
	for _, k := range []domain.LifecycleLedgerKind{
		domain.LifecycleKindPause, domain.LifecycleKindResume, domain.LifecycleKindFailover,
	} {
		if isSwitchLedgerKind(k) {
			t.Errorf("%q is not a switch saga kind but recovery treats it as one", k)
		}
	}
}

// TestObserveOrchestratorFleet_BoundsTerminatedHistory: this text goes into the
// target's launch prompt — into argv on Codex — and the launch happens AFTER
// the source stopped. Unbounded history would produce an oversized prompt at
// the moment failure is most expensive, and post-stop recovery would retry it
// forever.
func TestObserveOrchestratorFleet_BoundsTerminatedHistory(t *testing.T) {
	m, st, _ := orchestratorSwitchHarness(t)
	for i := 0; i < 200; i++ {
		id := domain.SessionID(fmt.Sprintf("mer-old-%03d", i))
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: true,
		}
	}
	// Live workers are never dropped.
	for i := 0; i < 5; i++ {
		id := domain.SessionID(fmt.Sprintf("mer-live-%d", i))
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker,
			Activity: domain.Activity{State: domain.ActivityActive},
		}
	}

	obs, err := m.ObserveOrchestratorFleet(context.Background(), "mer", "gen-1")
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if len(obs.Workers) > 5+maxObservedTerminatedWorkers {
		t.Fatalf("fleet not bounded: %d entries", len(obs.Workers))
	}
	var live int
	for _, w := range obs.Workers {
		if !w.IsTerminated {
			live++
		}
	}
	if live != 5 {
		t.Errorf("live workers = %d, want all 5 kept: they are what the coordinator must act on", live)
	}
	if obs.OmittedTerminated != 200-maxObservedTerminatedWorkers {
		t.Errorf("omitted count = %d, want %d — silent truncation reads as a complete fleet",
			obs.OmittedTerminated, 200-maxObservedTerminatedWorkers)
	}
}

// TestFreshConversation_WorksWithoutARolePin is the regression for the failure
// live testing found.
//
// relaunchSession stamps an ephemeral ResolvedHarness so the target's
// AUTHORITATIVE ROLE FOOTER names the new harness. On a session with NO role
// pin that stamp manufactured a partial pin out of an empty binding, and
// restoreRoleApplyResult correctly refused it as corrupt — after the source had
// already stopped. The result was a post-stop session that failed identically on
// every boot: SWITCH_POST_STOP forever.
//
// Un-pinned sessions were previously unreachable behind the service's
// ROLE_PIN_REQUIRED check, which is why no unit test covered it: every fixture
// seeded a role-mapped project.
func TestFreshConversation_WorksWithoutARolePin(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		t.Run(string(kind), func(t *testing.T) {
			m, st, id := orchestratorSwitchHarness(t)
			rec := st.sessions[id]
			rec.Kind = kind
			// Exactly what a session on a project with no role map looks like.
			rec.Metadata.Role = domain.SessionRoleBinding{}
			st.sessions[id] = rec

			var err error
			if kind == domain.KindOrchestrator {
				_, err = m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{})
			} else {
				_, err = m.FreshConversation(context.Background(), id, domain.SemanticHandoffV1{})
			}
			if err != nil {
				t.Fatalf("fresh conversation failed for an un-pinned session: %v", err)
			}
			got := st.sessions[id]
			if got.IsTerminated {
				t.Error("session left terminated")
			}
			// The empty pin must stay empty — not half-populated by the stamp.
			if got.Metadata.Role.ResolvedHarness != "" || got.Metadata.Role.RoleID != "" {
				t.Errorf("an empty role pin was partially populated: %+v", got.Metadata.Role)
			}
		})
	}
}

// TestObserveOrchestratorFleet_ReportsWhatAOKnows: the fleet is read from the
// session table, not from the outgoing conversation's recollection. Terminated
// workers are included because "that one already finished" is the correction a
// long-running coordinator most needs.
func TestObserveOrchestratorFleet_ReportsWhatAOKnows(t *testing.T) {
	m, st, _ := orchestratorSwitchHarness(t)
	st.sessions["mer-2"] = domain.SessionRecord{
		ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "ao/mer-2", Role: domain.SessionRoleBinding{RoleID: "implementor"}},
	}
	st.sessions["mer-3"] = domain.SessionRecord{
		ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		IsTerminated: true,
	}

	obs, err := m.ObserveOrchestratorFleet(context.Background(), "mer", "gen-1")
	if err != nil {
		t.Fatalf("observe fleet: %v", err)
	}
	if obs.SchemaVersion != domain.ObservedOrchestratorSchemaVersion {
		t.Errorf("schema version = %d", obs.SchemaVersion)
	}
	if len(obs.Workers) != 2 {
		t.Fatalf("workers = %+v, want both the live and the terminated worker", obs.Workers)
	}
	// Stable order so two handoffs can be diffed.
	if obs.Workers[0].SessionID != "mer-2" || obs.Workers[1].SessionID != "mer-3" {
		t.Fatalf("workers not in stable id order: %+v", obs.Workers)
	}
	if obs.Workers[0].RoleID != "implementor" || obs.Workers[0].Branch != "ao/mer-2" {
		t.Errorf("live worker facts lost: %+v", obs.Workers[0])
	}
	if !obs.Workers[1].IsTerminated {
		t.Error("terminated worker not marked: the fresh conversation would re-delegate finished work")
	}
	// The orchestrator itself is not a member of its own fleet.
	for _, w := range obs.Workers {
		if w.SessionID == "mer-1" {
			t.Error("the orchestrator listed itself as one of its workers")
		}
	}
}

// TestFreshOrchestratorConversation_HandoffCarriesTheFleet: observing is only
// useful if it reaches the launched agent.
func TestFreshOrchestratorConversation_HandoffCarriesTheFleet(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	st.sessions["mer-7"] = domain.SessionRecord{
		ID: "mer-7", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{Branch: "ao/mer-7"},
	}
	agent := &recordingAgent{}
	m.agents = singleAgent{agent: agent}

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{
		Objective: "keep coordinating",
	}); err != nil {
		t.Fatalf("orchestrator fresh: %v", err)
	}

	prompt := agent.lastLaunch.SystemPrompt
	if prompt == "" {
		prompt = agent.lastRestore.SystemPrompt
	}
	combined := prompt + "\n" + agent.lastLaunch.Prompt
	if !strings.Contains(combined, "mer-7") {
		t.Fatalf("compiled handoff does not name the live worker; the fresh conversation starts blind:\n%s", combined)
	}
	if !strings.Contains(combined, "Observed fleet") {
		t.Errorf("handoff lacks the host fleet section:\n%s", combined)
	}
}

// TestFreshOrchestratorConversation_FleetFailureDegrades: the fleet is context,
// not a safety invariant. Failing the switch would strand an orchestrator that
// is already out of context — the exact condition being remedied.
func TestFreshOrchestratorConversation_FleetFailureDegrades(t *testing.T) {
	m, st, id := orchestratorSwitchHarness(t)
	st.listSessionsErr = errors.New("database is locked")

	if _, err := m.FreshOrchestratorConversation(context.Background(), id, domain.SemanticHandoffV1{}); err != nil {
		t.Fatalf("orchestrator fresh: %v — an unreadable fleet must degrade, not abort", err)
	}
	if st.sessions[id].IsTerminated {
		t.Error("orchestrator terminated because its fleet could not be read")
	}
}
