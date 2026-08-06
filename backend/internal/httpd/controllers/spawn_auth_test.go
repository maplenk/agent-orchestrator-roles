package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/authctx"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred"
)

type opAuth struct{ tok string }

func (o opAuth) Valid(t string) bool { return o.tok != "" && t == o.tok }

type spawnGateSvc struct {
	sessions    map[domain.SessionID]domain.Session
	spawned     int
	switchCalls int
	freshCalls  int
	pauseCalls  int
	resumeCalls int
}

func newSpawnGateSvcWithToken() (*spawnGateSvc, string) {
	now := time.Now().UTC()
	plain, hash, err := spawncred.Issue()
	if err != nil {
		panic(err)
	}
	s := domain.Session{SessionRecord: domain.SessionRecord{
		ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
		Metadata: domain.SessionMetadata{SpawnCapabilityHash: hash},
	}, Status: domain.StatusIdle}
	return &spawnGateSvc{sessions: map[domain.SessionID]domain.Session{s.ID: s}}, plain
}

func (f *spawnGateSvc) List(context.Context, sessionsvc.ListFilter) ([]domain.Session, error) {
	return nil, nil
}
func (f *spawnGateSvc) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.spawned++
	now := time.Now().UTC()
	id := domain.SessionID("ao-spawned")
	s := domain.Session{SessionRecord: domain.SessionRecord{
		ID: id, ProjectID: cfg.ProjectID, Kind: cfg.Kind,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	}, Status: domain.StatusIdle}
	f.sessions[id] = s
	return s, 0, 0, nil
}
func (f *spawnGateSvc) SpawnOrchestrator(context.Context, domain.ProjectID, bool, domain.SessionMode) (domain.Session, error) {
	return domain.Session{}, nil
}
func (f *spawnGateSvc) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s, ok := f.sessions[id]
	if !ok {
		return domain.Session{}, errors.New("session not found")
	}
	return s, nil
}
func (f *spawnGateSvc) Restore(context.Context, domain.SessionID) (sessionsvc.RestoreOutcome, error) {
	return sessionsvc.RestoreOutcome{}, nil
}
func (f *spawnGateSvc) ResumeAgent(context.Context, domain.SessionID) (sessionsvc.ResumeAgentOutcome, error) {
	return sessionsvc.ResumeAgentOutcome{}, nil
}
func (f *spawnGateSvc) Kill(context.Context, domain.SessionID) (bool, error) { return false, nil }
func (f *spawnGateSvc) RollbackSpawn(context.Context, domain.SessionID) (sessionsvc.RollbackOutcome, error) {
	return sessionsvc.RollbackOutcome{}, nil
}
func (f *spawnGateSvc) Cleanup(context.Context, domain.ProjectID) (sessionsvc.CleanupOutcome, error) {
	return sessionsvc.CleanupOutcome{}, nil
}
func (f *spawnGateSvc) Rename(context.Context, domain.SessionID, string) error { return nil }
func (f *spawnGateSvc) SetPreview(context.Context, domain.SessionID, string) (domain.Session, error) {
	return domain.Session{}, nil
}
func (f *spawnGateSvc) SetTerminateOnPRMerge(context.Context, domain.SessionID, bool) (domain.Session, error) {
	return domain.Session{}, nil
}
func (f *spawnGateSvc) Send(context.Context, domain.SessionID, string) error { return nil }
func (f *spawnGateSvc) SwitchWorker(_ context.Context, req sessionsvc.SwitchWorkerRequest) (sessionsvc.SwitchWorkerOutcome, error) {
	f.switchCalls++
	s := f.sessions[req.SessionID]
	if s.ID == "" {
		s = domain.Session{SessionRecord: domain.SessionRecord{ID: req.SessionID, Kind: domain.KindWorker}}
	}
	return sessionsvc.SwitchWorkerOutcome{
		Session: s, GenerationID: "gen-test", Kind: domain.LifecycleKindSwitch,
	}, nil
}
func (f *spawnGateSvc) FreshConversation(_ context.Context, id domain.SessionID, _ string) (sessionsvc.SwitchWorkerOutcome, error) {
	f.freshCalls++
	s := f.sessions[id]
	if s.ID == "" {
		s = domain.Session{SessionRecord: domain.SessionRecord{ID: id, Kind: domain.KindWorker}}
	}
	return sessionsvc.SwitchWorkerOutcome{
		Session: s, GenerationID: "gen-fresh", Kind: domain.LifecycleKindFreshConversation,
	}, nil
}

func (f *spawnGateSvc) PauseSession(_ context.Context, id domain.SessionID, incidentID, reason string) (domain.SessionRecord, error) {
	f.pauseCalls++
	rec := domain.SessionRecord{ID: id}
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: incidentID, Reason: domain.PauseReasonOperator}
	return rec, nil
}

func (f *spawnGateSvc) ResumeSession(_ context.Context, id domain.SessionID, incidentID string) (domain.SessionRecord, error) {
	f.resumeCalls++
	return domain.SessionRecord{ID: id}, nil
}

func (f *spawnGateSvc) DelegateTask(_ context.Context, in sessionsvc.DelegateTaskInput) (sessionsvc.DelegateTaskOutcome, error) {
	return sessionsvc.DelegateTaskOutcome{}, nil
}

func (f *spawnGateSvc) SetReviewerHarness(_ context.Context, id domain.SessionID, _ domain.ReviewerHarness) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}}, nil
}

func (f *spawnGateSvc) Pin(_ context.Context, id domain.SessionID) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}}, nil
}

func (f *spawnGateSvc) Unpin(_ context.Context, id domain.SessionID) (domain.Session, error) {
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}}, nil
}
func (f *spawnGateSvc) ListPRSummaries(context.Context, domain.SessionID) ([]sessionsvc.PRSummary, error) {
	return nil, nil
}
func (f *spawnGateSvc) ClaimPR(context.Context, domain.SessionID, string, sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	return sessionsvc.ClaimPRResult{}, nil
}
func (f *spawnGateSvc) WorkspaceWatchPaths(context.Context, domain.SessionID) ([]string, error) {
	return nil, nil
}
func (f *spawnGateSvc) ListWorkspaceFiles(context.Context, domain.SessionID) (sessionsvc.WorkspaceFiles, error) {
	return sessionsvc.WorkspaceFiles{}, nil
}
func (f *spawnGateSvc) GetWorkspaceFile(context.Context, domain.SessionID, string) (sessionsvc.WorkspaceFileDetail, error) {
	return sessionsvc.WorkspaceFileDetail{}, nil
}

func spawnGateServer(t *testing.T, svc *spawnGateSvc, op opAuth) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		Sessions:      svc,
		OperatorSpawn: op,
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func doSpawnPOST(t *testing.T, srv *httptest.Server, headers map[string]string) (int, map[string]any) {
	t.Helper()
	body := `{"projectId":"ao","displayName":"task","prompt":"hi"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env
}

func TestSpawn_HeaderlessRejected(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSpawnPOST(t, srv, nil)
	if code != http.StatusForbidden || env["code"] != "SPAWN_AUTH_REQUIRED" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.spawned != 0 {
		t.Fatal("must not spawn")
	}
}

func TestSpawn_OperatorTokenAllows(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, _ := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Operator-Spawn-Token": "op-secret",
	})
	if code != http.StatusCreated {
		t.Fatalf("status=%d", code)
	}
}

func TestSpawn_OperatorTokenInvalid(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Operator-Spawn-Token": "wrong",
	})
	if code != http.StatusForbidden || env["code"] != "OPERATOR_SPAWN_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
}

func TestSpawn_AgentCanSpawnFalseForbidden(t *testing.T) {
	svc, plain := newSpawnGateSvcWithToken()
	s := svc.sessions["ao-1"]
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID: "implementor",
		ResolvedPermissions: domain.RoleExecutionPolicy{
			WorkspaceWrites: true,
			CanSpawn:        false,
		},
	}
	svc.sessions["ao-1"] = s
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusForbidden || env["code"] != "SPAWN_FORBIDDEN" {
		t.Fatalf("status=%d env=%v", code, env)
	}
}

func TestSpawn_AgentCanSpawnTrueAllowed(t *testing.T) {
	svc, plain := newSpawnGateSvcWithToken()
	s := svc.sessions["ao-1"]
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID: "orchestrator",
		ResolvedPermissions: domain.RoleExecutionPolicy{
			WorkspaceWrites: false,
			CanSpawn:        true,
		},
	}
	svc.sessions["ao-1"] = s
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, _ := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusCreated {
		t.Fatalf("status=%d", code)
	}
}

func TestSpawn_TerminatedCallerRejected(t *testing.T) {
	svc, plain := newSpawnGateSvcWithToken()
	s := svc.sessions["ao-1"]
	s.IsTerminated = true
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID:              "orchestrator",
		ResolvedPermissions: domain.RoleExecutionPolicy{CanSpawn: true},
	}
	svc.sessions["ao-1"] = s
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusForbidden || env["code"] != "SPAWN_SESSION_TERMINATED" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.spawned != 0 {
		t.Fatal("must not spawn")
	}
}

func TestSpawn_AgentBadCapabilityRejected(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "not-the-token",
	})
	if code != http.StatusForbidden || env["code"] != "SPAWN_CAPABILITY_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
}

func TestSpawn_LANAuthenticatedTrustedWithoutOperatorHeader(t *testing.T) {
	// Mobile LAN password auth sets request context; no operator bearer needed.
	svc, _ := newSpawnGateSvcWithToken()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, OperatorSpawn: opAuth{tok: "op-secret"}}
	inner := httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{})
	// Simulate LAN middleware: wrap to inject LAN context for all requests.
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(authctx.WithLANAuthenticated(r.Context())))
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	code, _ := doSpawnPOST(t, srv, nil) // no operator header
	if code != http.StatusCreated {
		t.Fatalf("LAN-auth spawn status=%d", code)
	}
}

func (f *spawnGateSvc) StageAttachments(context.Context, domain.SessionID, []ports.SpawnAttachment) ([]string, error) {
	return nil, nil
}
