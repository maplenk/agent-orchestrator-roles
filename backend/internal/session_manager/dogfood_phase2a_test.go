package sessionmanager

// Phase 2A dogfood harness at manager level.
//
// After Phase 2A promotion, production capabilities.For advertises
// SwitchSupported for Claude/Codex. Dogfood exercises the saga on the
// production registry (no switchCapsOverride).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// dogfoodEvidence is filled during TestDogfood_Phase2AChecklist for the
// human-readable evidence log (docs/roles/PHASE2A_DOGFOOD.md).
type dogfoodEvidence struct {
	lines []string
}

func (e *dogfoodEvidence) add(format string, args ...any) {
	e.lines = append(e.lines, fmt.Sprintf(format, args...))
}

func (e *dogfoodEvidence) dump(t *testing.T) {
	t.Helper()
	for _, line := range e.lines {
		t.Log(line)
	}
	// Optional: write under backend when AO_DOGFOOD_EVIDENCE_DIR is set.
	if dir := strings.TrimSpace(os.Getenv("AO_DOGFOOD_EVIDENCE_DIR")); dir != "" {
		_ = os.MkdirAll(dir, 0o750)
		path := filepath.Join(dir, "phase2a-dogfood-evidence.txt")
		body := strings.Join(e.lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write evidence: %v", err)
		}
		t.Logf("wrote evidence file: %s", path)
	}
}

func dogfoodManager(st *fakeStore, rt *fakeRuntime) *Manager {
	// Production Claude/Codex SwitchSupported=true — no override.
	return New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{},
		Store:     st,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
}

func ledgerPhases(st *fakeStore, gen string) []domain.LifecycleLedgerPhase {
	var out []domain.LifecycleLedgerPhase
	for _, e := range st.ledger {
		if e.GenerationID == gen {
			out = append(out, e.Phase)
		}
	}
	return out
}

func ledgerIDs(st *fakeStore, gen string) []string {
	var out []string
	for _, e := range st.ledger {
		if e.GenerationID == gen {
			out = append(out, e.ID)
		}
	}
	return out
}

// TestDogfood_Phase2AChecklist is the manager-level dogfood gate for Phase 2A.
// After promotion it runs on production SwitchSupported cells (no override).
func TestDogfood_Phase2AChecklist(t *testing.T) {
	ev := &dogfoodEvidence{}
	ev.add("=== Phase 2A dogfood checklist (manager-level) ===")
	ev.add("note: production SwitchSupported=true for claude-code and codex (Phase 2A promote)")
	ev.add("override: none (production registry)")

	t.Run("0_production_caps_promoted", func(t *testing.T) {
		claude := capabilities.For(domain.HarnessClaudeCode)
		codex := capabilities.For(domain.HarnessCodex)
		if !claude.SwitchSupported || !codex.SwitchSupported {
			t.Fatalf("production caps must be promoted: claude=%+v codex=%+v", claude, codex)
		}
		// Without override, Claude→Codex must succeed on production registry.
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
		// no switchCapsOverride
		res, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
		if err != nil {
			t.Fatalf("promoted path without override: %v", err)
		}
		if res.Session.Harness != domain.HarnessCodex {
			t.Fatalf("harness=%q want codex", res.Session.Harness)
		}
		ev.add("PASS 0: production SwitchSupported=true; SwitchWorker Claude→Codex without override")
	})

	t.Run("1_claude_to_codex", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-c2x")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		// Distinct handle for terminal gate realism.
		rec := st.sessions[id]
		rec.Metadata.RuntimeHandleID = "ao-dog.c2x-handle"
		st.sessions[id] = rec

		var launchIDs []string
		m := dogfoodManager(st, &fakeRuntime{})
		m.newLaunchID = func() string {
			id := fmt.Sprintf("gen-c2x-%d", len(launchIDs)+1)
			launchIDs = append(launchIDs, id)
			return id
		}

		// During pending, terminal input must block (after we force a mid-switch
		// inspection via a second session fixture is overkill — assert after
		// successful path still cleared, and use dedicated fence subtest).
		res, err := m.SwitchWorker(ctx, SwitchRequest{
			SessionID: id, TargetHarness: domain.HarnessCodex,
			Semantic: domain.SemanticHandoffV1{Objective: "dogfood c2x", SchemaVersion: 1},
		})
		if err != nil {
			t.Fatalf("claude→codex: %v", err)
		}
		if res.Session.Harness != domain.HarnessCodex {
			t.Fatalf("harness=%q want codex", res.Session.Harness)
		}
		if res.Session.Metadata.SwitchPending != nil {
			t.Fatal("pending must clear after ack")
		}
		if res.GenerationID == "" || res.GenerationID != res.Session.Metadata.RuntimeLaunchID {
			t.Fatalf("gen mismatch ledger=%q runtime=%q", res.GenerationID, res.Session.Metadata.RuntimeLaunchID)
		}
		if res.Session.Metadata.Role.ResolvedModel == "claude-sonnet-source" {
			t.Fatal("cross-harness must not leak source model")
		}
		phases := ledgerPhases(st, res.GenerationID)
		want := []domain.LifecycleLedgerPhase{
			domain.LifecyclePhaseRequested,
			domain.LifecyclePhasePreStop,
			domain.LifecyclePhasePostStop,
			domain.LifecyclePhaseTargetAck,
		}
		if len(phases) < 4 {
			t.Fatalf("phases=%v want at least %v", phases, want)
		}
		for i, p := range want {
			if phases[i] != p {
				t.Fatalf("phase[%d]=%s want %s full=%v", i, phases[i], p, phases)
			}
		}
		ids := ledgerIDs(st, res.GenerationID)
		for _, lid := range ids {
			if !strings.HasPrefix(lid, string(id)+":"+res.GenerationID+":") {
				t.Fatalf("unstable ledger id %q", lid)
			}
		}
		if !strings.Contains(res.Session.Metadata.Prompt, "Host-compiled handoff") {
			t.Fatal("composed handoff missing from prompt")
		}
		ev.add("PASS 1: claude→codex gen=%s runtime=%s phases=%v ids=%v model=%q",
			res.GenerationID, res.Session.Metadata.RuntimeLaunchID, phases, ids, res.Session.Metadata.Role.ResolvedModel)
	})

	t.Run("2_codex_to_claude", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-x2c")
		workerSession(st, id, domain.HarnessCodex, ws, art, sha)
		rec := st.sessions[id]
		rec.Metadata.Role.ResolvedModel = "codex-source-model"
		rec.Metadata.RuntimeHandleID = "ao-dog.x2c-handle"
		st.sessions[id] = rec

		m := dogfoodManager(st, &fakeRuntime{})
		m.newLaunchID = func() string { return "gen-x2c-1" }
		res, err := m.SwitchWorker(ctx, SwitchRequest{
			SessionID: id, TargetHarness: domain.HarnessClaudeCode,
			Semantic: domain.SemanticHandoffV1{Objective: "dogfood x2c", SchemaVersion: 1},
		})
		if err != nil {
			t.Fatalf("codex→claude: %v", err)
		}
		if res.Session.Harness != domain.HarnessClaudeCode {
			t.Fatalf("harness=%q want claude-code", res.Session.Harness)
		}
		if res.GenerationID != "gen-x2c-1" || res.Session.Metadata.RuntimeLaunchID != "gen-x2c-1" {
			t.Fatalf("gen ledger=%q runtime=%q", res.GenerationID, res.Session.Metadata.RuntimeLaunchID)
		}
		if res.Session.Metadata.Role.ResolvedModel == "codex-source-model" {
			t.Fatal("cross-harness must not leak codex model to claude")
		}
		phases := ledgerPhases(st, res.GenerationID)
		wantPhases := []domain.LifecycleLedgerPhase{
			domain.LifecyclePhaseRequested,
			domain.LifecyclePhasePreStop,
			domain.LifecyclePhasePostStop,
			domain.LifecyclePhaseTargetAck,
		}
		if len(phases) < len(wantPhases) {
			t.Fatalf("phases=%v want at least %v", phases, wantPhases)
		}
		for i, p := range wantPhases {
			if phases[i] != p {
				t.Fatalf("phase[%d]=%s want %s full=%v", i, phases[i], p, phases)
			}
		}
		if res.Session.Metadata.SwitchPending != nil {
			t.Fatal("pending must clear after ack")
		}
		ev.add("PASS 2: codex→claude gen=%s phases=%v pending=%v",
			res.GenerationID, phases, res.Session.Metadata.SwitchPending != nil)
	})

	t.Run("3_fresh_conversation_no_stack", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-fresh")
		workerSession(st, id, domain.HarnessCodex, ws, art, sha)
		m := dogfoodManager(st, &fakeRuntime{})
		m.newLaunchID = func() string { return "gen-fresh-1" }

		res1, err := m.FreshConversation(ctx, id, domain.SemanticHandoffV1{Objective: "round-1", SchemaVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		rec := res1.Session
		rec.Metadata.RuntimeHandleID = "rt-fresh-2"
		st.sessions[id] = rec
		m.newLaunchID = func() string { return "gen-fresh-2" }

		res2, err := m.FreshConversation(ctx, id, domain.SemanticHandoffV1{Objective: "round-2", SchemaVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(res2.Session.Metadata.Prompt, "## Host-compiled handoff")
		if n != 1 {
			t.Fatalf("stacked handoffs count=%d prompt:\n%s", n, res2.Session.Metadata.Prompt)
		}
		if !strings.Contains(res2.Session.Metadata.Prompt, "implement feature") {
			t.Fatal("original task lost")
		}
		if res2.Kind != domain.LifecycleKindFreshConversation {
			t.Fatalf("kind=%s", res2.Kind)
		}
		ev.add("PASS 3: fresh×2 gen=%s handoff_count=%d kind=%s",
			res2.GenerationID, n, res2.Kind)
	})

	t.Run("4_crash_recovery_post_stop", func(t *testing.T) {
		// Simulate daemon death after source stop + post_stop, before target launch/ack.
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-crash")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		payload := `{"semantic":{"schemaVersion":1,"objective":"crash-recover"},"observed":{"schemaVersion":1,"generationId":"src-gen"},"compiled":"## Host-compiled handoff\n\ncrash-recover"}`
		gen := "gen-crash-1"
		rec := st.sessions[id]
		rec.Metadata.SwitchPending = &domain.SwitchPending{
			GenerationID: gen, Kind: domain.LifecycleKindSwitch,
			FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
			OriginalTask: "implement feature", RoleID: "implementor", PayloadJSON: payload,
			SourceRuntimeHandleID: "rt-crash-src",
		}
		rec.Metadata.RuntimeHandleID = ""
		rec.Metadata.RuntimeLaunchID = ""
		rec.Metadata.AgentSessionID = ""
		rec.Metadata.Prompt = "## Host-compiled handoff\n\ncrash-recover\n\n## Prior task prompt\nimplement feature"
		st.sessions[id] = rec
		st.ledger = []domain.LifecycleLedgerRecord{
			{ID: string(id) + ":" + gen + ":requested", SessionID: id, ProjectID: "mer",
				Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhaseRequested, GenerationID: gen},
			{ID: string(id) + ":" + gen + ":pre_stop", SessionID: id, ProjectID: "mer",
				Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePreStop, GenerationID: gen, PayloadJSON: payload},
			{ID: string(id) + ":" + gen + ":post_stop", SessionID: id, ProjectID: "mer",
				Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePostStop, GenerationID: gen, PayloadJSON: payload},
		}

		rt := &fakeRuntime{}
		m := dogfoodManager(st, rt)
		// Recovery must reuse pending generation, not mint a new one for the target.
		m.newLaunchID = func() string { return "must-not-mint" }
		res, err := m.RecoverSwitchFromPostStop(ctx, id)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		if rt.created != 1 {
			t.Fatalf("create=%d want 1 (single target launch)", rt.created)
		}
		if res.GenerationID != gen {
			t.Fatalf("recover gen=%q want %q", res.GenerationID, gen)
		}
		if res.Session.Metadata.RuntimeLaunchID != gen {
			t.Fatalf("runtime gen=%q want %q", res.Session.Metadata.RuntimeLaunchID, gen)
		}
		if res.Session.Harness != domain.HarnessCodex || res.Session.Metadata.SwitchPending != nil {
			t.Fatalf("harness=%s pending=%v", res.Session.Harness, res.Session.Metadata.SwitchPending)
		}
		// Second recover must not double-launch: pending is cleared after ack.
		rt2 := &fakeRuntime{}
		m2 := dogfoodManager(st, rt2)
		_, err = m2.RecoverSwitchFromPostStop(ctx, id)
		if !errors.Is(err, ErrSwitchNothingToRecover) {
			t.Fatalf("second recover err=%v, want ErrSwitchNothingToRecover", err)
		}
		if rt2.created != 0 {
			t.Fatalf("double-launch create=%d", rt2.created)
		}
		phases := ledgerPhases(st, gen)
		var sawAck bool
		for _, p := range phases {
			if p == domain.LifecyclePhaseTargetAck {
				sawAck = true
			}
		}
		if !sawAck {
			t.Fatalf("missing target_ack after recover: %v", phases)
		}
		ev.add("PASS 4: crash recovery gen=%s runtime=%s create=%d phases=%v no_double_launch",
			res.GenerationID, res.Session.Metadata.RuntimeLaunchID, rt.created, phases)
	})

	t.Run("4b_crash_recovery_stale_alive_uncertain", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-stale")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		gen := "gen-stale-1"
		rec := st.sessions[id]
		rec.Metadata.SwitchPending = &domain.SwitchPending{
			GenerationID: gen, Kind: domain.LifecycleKindSwitch,
			FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
			OriginalTask: "task", RoleID: "implementor",
			PayloadJSON: `{"compiled":"## Host-compiled handoff\n\nx"}`,
		}
		rec.Metadata.RuntimeHandleID = "stale-live"
		rec.Metadata.RuntimeLaunchID = "other-gen" // wrong gen
		st.sessions[id] = rec
		st.ledger = []domain.LifecycleLedgerRecord{{
			ID: string(id) + ":" + gen + ":post_stop", SessionID: id, ProjectID: "mer",
			Kind: domain.LifecycleKindSwitch, Phase: domain.LifecyclePhasePostStop, GenerationID: gen,
			PayloadJSON: `{"compiled":"## Host-compiled handoff\n\nx"}`,
		}}
		rt := &fakeRuntime{aliveByHandle: map[string]bool{"stale-live": true}}
		m := dogfoodManager(st, rt)
		_, err := m.RecoverSwitchFromPostStop(ctx, id)
		if !errors.Is(err, ErrSwitchUncertain) {
			t.Fatalf("err=%v want ErrSwitchUncertain", err)
		}
		if rt.created != 0 {
			t.Fatalf("must not launch: create=%d", rt.created)
		}
		ev.add("PASS 4b: stale-alive wrong-gen → ErrSwitchUncertain create=0")
	})

	t.Run("5_confirmed_alive_rollback", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-alive")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		prePrompt := st.sessions[id].Metadata.Prompt
		preHandle := st.sessions[id].Metadata.RuntimeHandleID
		preAgent := st.sessions[id].Metadata.AgentSessionID
		preLaunch := st.sessions[id].Metadata.RuntimeLaunchID

		rt := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": true}}
		m := dogfoodManager(st, rt)
		_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
		if err == nil {
			t.Fatal("expected pre-stop failure when source stays alive")
		}
		if errors.Is(err, ErrSwitchUncertain) {
			t.Fatalf("rollback should succeed → not uncertain: %v", err)
		}
		if rt.created != 0 {
			t.Fatalf("create=%d", rt.created)
		}
		got := st.sessions[id]
		if got.Metadata.SwitchPending != nil {
			t.Fatal("pending must be rolled back")
		}
		if got.Metadata.Prompt != prePrompt || got.Metadata.RuntimeHandleID != preHandle ||
			got.Metadata.AgentSessionID != preAgent || got.Metadata.RuntimeLaunchID != preLaunch {
			t.Fatalf("usability not restored: %+v", got.Metadata)
		}
		// Terminal must allow again.
		if err := m.AllowTerminalInput(ctx, preHandle); err != nil {
			t.Fatalf("terminal after rollback: %v", err)
		}
		ev.add("PASS 5: confirmed-alive → rollback pending; source usable handle=%s", preHandle)
	})

	t.Run("5b_rollback_persist_fail_uncertain", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-rb-fail")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		st.updateFailAfter = 2
		st.updateErr = errors.New("disk full")
		rt := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": true}}
		m := dogfoodManager(st, rt)
		_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
		if !errors.Is(err, ErrSwitchUncertain) {
			t.Fatalf("err=%v want ErrSwitchUncertain", err)
		}
		if st.sessions[id].Metadata.SwitchPending == nil {
			t.Fatal("pending remains when rollback cannot persist")
		}
		ev.add("PASS 5b: rollback persist fail → ErrSwitchUncertain pending_retained=true")
	})

	t.Run("6_terminal_fence", func(t *testing.T) {
		st := newFakeStore()
		id := domain.SessionID("dog-term")
		handle := "ao-dog.term-sanitized-handle"
		st.sessions[id] = domain.SessionRecord{
			ID: id, ProjectID: "mer", Kind: domain.KindWorker,
			Metadata: domain.SessionMetadata{
				RuntimeHandleID: handle,
				SwitchPending: &domain.SwitchPending{
					GenerationID: "g-term", ToHarness: domain.HarnessCodex,
					SourceRuntimeHandleID: handle,
				},
			},
		}
		m := dogfoodManager(st, &fakeRuntime{})
		for _, key := range []string{string(id), handle} {
			err := m.AllowTerminalInput(ctx, key)
			if !errors.Is(err, ErrSwitchRecoveryRequired) {
				t.Fatalf("key=%s err=%v want ErrSwitchRecoveryRequired", key, err)
			}
		}
		// After handle clear, pending source handle still fences.
		rec := st.sessions[id]
		rec.Metadata.RuntimeHandleID = ""
		st.sessions[id] = rec
		if err := m.AllowTerminalInput(ctx, handle); !errors.Is(err, ErrSwitchRecoveryRequired) {
			t.Fatalf("pending-source-handle fence: %v", err)
		}
		if err := m.AllowTerminalInput(ctx, "shell-unrelated"); err != nil {
			t.Fatalf("shell allow: %v", err)
		}
		ev.add("PASS 6: terminal fence session_id + runtime_handle + pending_source_handle; shell allowed")
	})

	t.Run("7_post_stop_fail_blocks_launch", func(t *testing.T) {
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("dog-ps-fail")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		st.failLedgerPhase = domain.LifecyclePhasePostStop
		st.appendLedgerErr = errors.New("ledger full")
		rt := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": false}}
		m := dogfoodManager(st, rt)
		_, err := m.SwitchWorker(ctx, SwitchRequest{SessionID: id, TargetHarness: domain.HarnessCodex})
		if !errors.Is(err, ErrSwitchPostStop) {
			t.Fatalf("err=%v want ErrSwitchPostStop", err)
		}
		if rt.created != 0 {
			t.Fatalf("create=%d", rt.created)
		}
		for _, e := range st.ledger {
			if e.Phase == domain.LifecyclePhaseTargetAck {
				t.Fatal("ack without post_stop")
			}
		}
		ev.add("PASS 7: post_stop append fail → ErrSwitchPostStop create=0 no_ack")
	})

	ev.dump(t)
	// Ensure every required line was recorded (subtests must not skip silently).
	joined := strings.Join(ev.lines, "\n")
	for _, needle := range []string{
		"PASS 0:", "PASS 1:", "PASS 2:", "PASS 3:", "PASS 4:", "PASS 4b:",
		"PASS 5:", "PASS 5b:", "PASS 6:", "PASS 7:",
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("missing evidence line %q", needle)
		}
	}
}
