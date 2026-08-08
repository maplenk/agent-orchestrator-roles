package session

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type switchPRFailureStore struct{ *fakeStore }

func (s *switchPRFailureStore) ListPRFactsForSession(context.Context, domain.SessionID) ([]domain.PRFacts, error) {
	return nil, fmt.Errorf("injected PR facts failure")
}

func seedSwitchSession(st *fakeStore, id domain.SessionID, harness domain.AgentHarness) {
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{
			RoleMap: domain.RoleMap{
				SchemaVersion: domain.RoleMapSchemaVersion,
				Roles: map[string]domain.RoleBinding{
					"implementor": {
						Template: "implementor", Harness: domain.HarnessClaudeCode,
						Permissions: domain.RoleExecutionPolicy{WorkspaceWrites: true},
					},
				},
				Failover: domain.FailoverConfig{
					Mode: domain.FailoverModeManual,
					Roles: map[string][]domain.FailoverTarget{
						"implementor": {{Harness: domain.HarnessCodex}},
					},
				},
			},
		},
	}
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: harness,
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "rt-1", WorkspacePath: "/ws",
			Role: domain.SessionRoleBinding{RoleID: "implementor", ResolvedHarness: harness},
		},
	}
}

func TestSwitchWorker_AuthorizesFailoverTarget(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})

	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, Objective: "keep going",
	})
	if err != nil {
		t.Fatalf("switch: %v", err)
	}
	if cmd.switchCalls != 1 {
		t.Fatalf("switchCalls=%d", cmd.switchCalls)
	}
	if cmd.lastSwitch.TargetHarness != domain.HarnessCodex {
		t.Fatalf("target=%q", cmd.lastSwitch.TargetHarness)
	}
	if out.GenerationID != "gen-sw-1" || out.Kind != domain.LifecycleKindSwitch {
		t.Fatalf("out=%+v", out)
	}
}

func TestSwitchOutcome_HydrationFailureCannotReverseCommittedSuccess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		run   func(*Service, domain.SessionID) (SwitchWorkerOutcome, error)
		calls func(*fakeCommander) int
	}{
		{
			name: "switch",
			run: func(svc *Service, id domain.SessionID) (SwitchWorkerOutcome, error) {
				return svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
					SessionID: id, TargetHarness: domain.HarnessCodex,
				})
			},
			calls: func(cmd *fakeCommander) int { return cmd.switchCalls },
		},
		{
			name: "fresh",
			run: func(svc *Service, id domain.SessionID) (SwitchWorkerOutcome, error) {
				return svc.FreshConversation(context.Background(), id, "refresh")
			},
			calls: func(cmd *fakeCommander) int { return cmd.freshCalls },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := newFakeStore()
			id := domain.SessionID("mer-1")
			seedSwitchSession(base, id, domain.HarnessClaudeCode)
			cmd := &fakeCommander{}
			svc := NewWithDeps(Deps{Manager: cmd, Store: &switchPRFailureStore{fakeStore: base}})

			out, err := tc.run(svc, id)
			if err != nil {
				t.Fatalf("committed mutation reported failure: %v", err)
			}
			if tc.calls(cmd) != 1 {
				t.Fatalf("manager calls = %d, want exactly one", tc.calls(cmd))
			}
			if out.Session.ID != id || out.GenerationID == "" || out.Kind == "" {
				t.Fatalf("committed identity lost from fallback response: %+v", out)
			}
			if len(out.Session.PRs) != 0 {
				t.Fatalf("fallback response invented PR facts: %+v", out.Session.PRs)
			}
		})
	}
}

func TestSwitchWorker_RejectsUnauthorizedHarness(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessPi,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_TARGET_UNAUTHORIZED" {
		t.Fatalf("err=%v want SWITCH_TARGET_UNAUTHORIZED", err)
	}
}

func TestSwitchWorker_RequiresRoleMap(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{Role: domain.SessionRoleBinding{RoleID: "implementor"}},
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "ROLE_MAP_REQUIRED" {
		t.Fatalf("err=%v want ROLE_MAP_REQUIRED", err)
	}
}

func TestSwitchWorker_MapsNotSupported(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{switchErr: sessionmanager.ErrSwitchNotSupported}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_NOT_SUPPORTED" {
		t.Fatalf("err=%v want SWITCH_NOT_SUPPORTED", err)
	}
}

func TestSwitchWorker_ChatModeRefusesEveryEntryBeforeAuthorizationOrManager(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*Service, domain.SessionID) (SwitchWorkerOutcome, error)
	}{
		{
			name: "fresh endpoint",
			run: func(svc *Service, id domain.SessionID) (SwitchWorkerOutcome, error) {
				return svc.FreshConversation(context.Background(), id, "refresh")
			},
		},
		{
			name: "same harness switch",
			run: func(svc *Service, id domain.SessionID) (SwitchWorkerOutcome, error) {
				return svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
					SessionID: id, TargetHarness: domain.HarnessClaudeCode,
				})
			},
		},
		{
			name: "cross harness switch",
			run: func(svc *Service, id domain.SessionID) (SwitchWorkerOutcome, error) {
				return svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
					SessionID: id, TargetHarness: domain.HarnessCodex,
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			id := domain.SessionID("mer-1")
			// Deliberately no project config and no durable role pin. Chat-mode
			// refusal must outrank target authorization for every entry point.
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			st.sessions[id] = domain.SessionRecord{
				ID: id, ProjectID: "mer", Kind: domain.KindOrchestrator,
				Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
			}
			cmd := &fakeCommander{}
			svc := NewWithDeps(Deps{Manager: cmd, Store: st})

			_, err := tc.run(svc, id)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindConflict || apiErr.Code != "SWITCH_CHAT_UNSUPPORTED" {
				t.Fatalf("err=%v, want 409 SWITCH_CHAT_UNSUPPORTED", err)
			}
			if cmd.switchCalls != 0 || cmd.orchestratorSwitchCalls != 0 || cmd.freshCalls != 0 || cmd.orchestratorFreshCalls != 0 {
				t.Fatalf("manager calls worker/orchestrator/fresh/orchestratorFresh = %d/%d/%d/%d, want all zero",
					cmd.switchCalls, cmd.orchestratorSwitchCalls, cmd.freshCalls, cmd.orchestratorFreshCalls)
			}
		})
	}
}

func TestSwitchWorker_ChatPreflightPreservesDurableStateOrdering(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  func(domain.SessionRecord) domain.SessionRecord
		code string
	}{
		{
			name: "terminated outranks chat",
			rec: func(rec domain.SessionRecord) domain.SessionRecord {
				rec.IsTerminated = true
				return rec
			},
			code: "SESSION_TERMINATED",
		},
		{
			name: "pause outranks chat",
			rec: func(rec domain.SessionRecord) domain.SessionRecord {
				rec.Metadata.Pause = &domain.SessionPause{IncidentID: "limit-1"}
				return rec
			},
			code: "SWITCH_PAUSED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			id := domain.SessionID("mer-1")
			rec := domain.SessionRecord{
				ID: id, ProjectID: "mer", Kind: domain.KindOrchestrator,
				Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat,
			}
			st.sessions[id] = tc.rec(rec)
			cmd := &fakeCommander{}
			svc := NewWithDeps(Deps{Manager: cmd, Store: st})

			_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
				SessionID: id, TargetHarness: domain.HarnessCodex,
			})
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("err=%v, want %s", err, tc.code)
			}
			if cmd.switchCalls+cmd.orchestratorSwitchCalls+cmd.freshCalls+cmd.orchestratorFreshCalls != 0 {
				t.Fatal("durable-state refusal reached the manager")
			}
		})
	}
}

func TestFreshConversation_NoFreeFormHarness(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessCodex)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	out, err := svc.FreshConversation(context.Background(), id, "refresh context")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.freshCalls != 1 || cmd.switchCalls != 0 {
		t.Fatalf("fresh=%d switch=%d", cmd.freshCalls, cmd.switchCalls)
	}
	if out.Kind != domain.LifecycleKindFreshConversation {
		t.Fatalf("kind=%s", out.Kind)
	}
}

func TestSwitchWorker_SameHarnessIsFresh(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessClaudeCode,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.freshCalls != 1 || cmd.switchCalls != 0 {
		t.Fatalf("fresh=%d switch=%d", cmd.freshCalls, cmd.switchCalls)
	}
}

// TestSwitchWorker_OrchestratorFreshRoutesToTheGatedEntryPoint: the two entry
// points take DIFFERENT LOCKS, so dispatching by kind is a correctness
// requirement, not tidiness. Routing an orchestrator through the worker
// FreshConversation would run its saga without the project ownership gate.
func TestSwitchWorker_OrchestratorFreshRoutesToTheGatedEntryPoint(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	st.sessions[id] = rec

	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{SessionID: id, Fresh: true})
	if err != nil {
		t.Fatalf("orchestrator fresh: %v", err)
	}
	if cmd.orchestratorFreshCalls != 1 {
		t.Fatalf("orchestrator entry point called %d times: the saga would run ungated",
			cmd.orchestratorFreshCalls)
	}
	if cmd.switchCalls != 0 {
		t.Errorf("cross-harness switch path used for an orchestrator: %d", cmd.switchCalls)
	}
	if out.Kind != domain.LifecycleKindOrchestratorFresh {
		t.Errorf("outcome kind = %q, want %q — the API contract must expose the real kind",
			out.Kind, domain.LifecycleKindOrchestratorFresh)
	}
}

// TestSwitchWorker_FreshNeedsNoRolePin is the regression for 2B-1 being
// unreachable in practice.
//
// The role pin and role map authorize a TARGET harness/model. A same-harness
// refresh has no target, so requiring them gated the feature on something
// irrelevant — and gated it hardest on the sessions that need it most: an
// orchestrator is auto-bound to orchestratorRole only under strict delegation,
// so on any project without a role map it has no pin at all. Live testing hit
// ROLE_PIN_REQUIRED on a real orchestrator while every unit test passed,
// because the fixtures all seeded a role-mapped project.
func TestSwitchWorker_FreshNeedsNoRolePin(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		t.Run(string(kind), func(t *testing.T) {
			st := newFakeStore()
			id := domain.SessionID("mer-1")
			seedSwitchSession(st, id, domain.HarnessClaudeCode)
			// What a real session on a project with no role map looks like.
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			rec := st.sessions[id]
			rec.Kind = kind
			rec.Metadata.Role = domain.SessionRoleBinding{}
			st.sessions[id] = rec

			cmd := &fakeCommander{}
			svc := NewWithDeps(Deps{Manager: cmd, Store: st})
			if _, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
				SessionID: id, Fresh: true,
			}); err != nil {
				t.Fatalf("fresh conversation refused without a role pin: %v", err)
			}
			if cmd.freshCalls+cmd.orchestratorFreshCalls != 1 {
				t.Fatalf("manager not reached: fresh=%d orchestratorFresh=%d",
					cmd.freshCalls, cmd.orchestratorFreshCalls)
			}
		})
	}
}

// TestSwitchWorker_CrossHarnessStillRequiresRolePin is the other side: the role
// map remains the only source of legal targets, so dropping it there would be
// an authorization hole rather than a convenience.
func TestSwitchWorker_CrossHarnessStillRequiresRolePin(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Metadata.Role = domain.SessionRoleBinding{}
	st.sessions[id] = rec

	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "ROLE_PIN_REQUIRED" {
		t.Fatalf("err = %v, want ROLE_PIN_REQUIRED: an unpinned session has no authorized targets", err)
	}
	if cmd.switchCalls != 0 {
		t.Error("the manager was reached for an unauthorized cross-harness switch")
	}
}

// TestSwitchWorker_OrchestratorCrossHarnessIsConflict: the request is
// well-formed and the harness is real; what is unavailable is the state
// transition. Clients distinguish that from malformed input.
func TestSwitchWorker_OrchestratorCrossHarnessUsesGatedEntryPoint(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	st.sessions[id] = rec

	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})
	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	if err != nil {
		t.Fatalf("orchestrator switch: %v", err)
	}
	if cmd.orchestratorSwitchCalls != 1 || cmd.switchCalls != 0 || cmd.orchestratorFreshCalls != 0 {
		t.Fatalf("dispatch: orchestratorSwitch=%d workerSwitch=%d fresh=%d",
			cmd.orchestratorSwitchCalls, cmd.switchCalls, cmd.orchestratorFreshCalls)
	}
	if out.Kind != domain.LifecycleKindSwitch {
		t.Fatalf("kind=%q want switch", out.Kind)
	}
}

func TestSwitchWorker_EmptyConfiguredModelRejectsExplicitModel(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	// failover codex has empty model (= provider default only)
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})
	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "o3",
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SWITCH_TARGET_UNAUTHORIZED" {
		t.Fatalf("err=%v want SWITCH_TARGET_UNAUTHORIZED", err)
	}
}

func TestSwitchWorker_AmbiguousModelRequiresExplicit(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	proj := st.projects["mer"]
	proj.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "o3"},
		{Harness: domain.HarnessCodex, Model: "o4"},
	}
	st.projects["mer"] = proj
	cmd := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: cmd, Store: st})

	_, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex,
	})
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "TARGET_MODEL_REQUIRED" {
		t.Fatalf("err=%v want TARGET_MODEL_REQUIRED", err)
	}

	out, err := svc.SwitchWorker(context.Background(), SwitchWorkerRequest{
		SessionID: id, TargetHarness: domain.HarnessCodex, TargetModel: "o3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.lastSwitch.TargetModel != "o3" {
		t.Fatalf("model=%q", cmd.lastSwitch.TargetModel)
	}
	if out.GenerationID == "" {
		t.Fatal("missing generation")
	}
}

func TestSwitchPreview_WorkerDoesNotReadProject(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), st.sessions[id])
	if err != nil {
		t.Fatal(err)
	}
	if got := st.getProjectCalls.Load(); got != 0 {
		t.Fatalf("project reads=%d, want zero for ordinary worker", got)
	}
	if preview.Available || len(preview.Targets) != 0 {
		t.Fatalf("worker preview=%+v", preview)
	}
}

func TestSwitchPreview_ChatOrchestratorIsUnavailableWithoutProjectRead(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	rec.Mode = domain.SessionModeChat
	st.sessions[id] = rec
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Available || preview.Reason != SwitchPreviewReasonUnavailable || len(preview.Targets) != 0 {
		t.Fatalf("chat preview=%+v, want unavailable with no targets", preview)
	}
	if got := st.getProjectCalls.Load(); got != 0 {
		t.Fatalf("chat preview read project %d time(s), want zero", got)
	}
}

func TestSwitchPreview_ExactRoleMapModels(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	rec.Metadata.Role.ResolvedModel = "opus"
	st.sessions[id] = rec
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "o3"},
		{Harness: domain.HarnessCodex, Model: "o4"},
		// Same-harness means Fresh Conversation, not a model switch.
		{Harness: domain.HarnessClaudeCode, Model: "sonnet"},
	}
	st.projects["mer"] = project
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Available || preview.Reason != "" {
		t.Fatalf("preview=%+v", preview)
	}
	if preview.Current.Harness != domain.HarnessClaudeCode || preview.Current.Model != "opus" {
		t.Fatalf("current=%+v", preview.Current)
	}
	want := []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: "o3"},
		{Harness: domain.HarnessCodex, Model: "o4"},
	}
	if len(preview.Targets) != len(want) {
		t.Fatalf("targets=%+v want %+v", preview.Targets, want)
	}
	for i := range want {
		if preview.Targets[i] != want[i] {
			t.Fatalf("targets[%d]=%+v want %+v", i, preview.Targets[i], want[i])
		}
	}
}

func TestSwitchPreview_DoesNotAdvertiseAmbiguousProviderDefault(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	st.sessions[id] = rec
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Roles["implementor"] = []domain.FailoverTarget{
		{Harness: domain.HarnessCodex, Model: ""},
		{Harness: domain.HarnessCodex, Model: "o4"},
	}
	st.projects["mer"] = project
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.FailoverTarget{{Harness: domain.HarnessCodex, Model: "o4"}}
	if len(preview.Targets) != 1 || preview.Targets[0] != want[0] {
		t.Fatalf("preview advertised an API-unrepresentable default target: %+v", preview.Targets)
	}
	if !preview.Available {
		t.Fatalf("fixed exact target should remain available: %+v", preview)
	}
}

func TestSwitchPreview_PausedOrchestratorRefusesAllLifecycleActions(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: "limit-1"}
	st.sessions[id] = rec
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Available || preview.Reason != SwitchPreviewReasonPaused || len(preview.Targets) != 0 {
		t.Fatalf("paused preview = %+v", preview)
	}
	if got := st.getProjectCalls.Load(); got != 0 {
		t.Fatalf("paused preview read project %d time(s), want zero", got)
	}
}

func TestSwitchPreview_PendingUsesDurableTargetWithoutProjectRead(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-1")
	seedSwitchSession(st, id, domain.HarnessClaudeCode)
	rec := st.sessions[id]
	rec.Kind = domain.KindOrchestrator
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-pending", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		FromModel: "opus", ToModel: "o3", PayloadJSON: `{"secret":"not a read model"}`,
	}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st})

	preview, err := svc.SwitchPreview(context.Background(), rec)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Available || preview.Reason != SwitchPreviewReasonInProgress || preview.Pending == nil {
		t.Fatalf("preview=%+v", preview)
	}
	if got := st.getProjectCalls.Load(); got != 0 {
		t.Fatalf("project reads=%d, want zero for durable pending state", got)
	}
}
