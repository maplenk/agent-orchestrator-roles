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

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type spawnAuthFixed struct{ ok bool }

func (f spawnAuthFixed) Valid(domain.SessionID, string) bool { return f.ok }

// Minimal SessionService for canSpawn gate tests.
type spawnGateSvc struct {
	sessions map[domain.SessionID]domain.Session
	spawned  int
}

func newSpawnGateSvc() *spawnGateSvc {
	now := time.Now().UTC()
	s := domain.Session{SessionRecord: domain.SessionRecord{
		ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	}, Status: domain.StatusIdle}
	return &spawnGateSvc{sessions: map[domain.SessionID]domain.Session{s.ID: s}}
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
func (f *spawnGateSvc) SpawnOrchestrator(context.Context, domain.ProjectID, bool) (domain.Session, error) {
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

func spawnGateServer(t *testing.T, svc *spawnGateSvc, auth controllers.SpawnCapabilityValidator) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		Sessions:          svc,
		SpawnCapabilities: auth,
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func doSpawnPOST(t *testing.T, srv *httptest.Server, headers map[string]string) *http.Response {
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
	return resp
}

func readBody(t *testing.T, resp *http.Response) (int, map[string]any) {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	return resp.StatusCode, env
}

func TestSpawn_OperatorPathAllowsWithoutHeaders(t *testing.T) {
	svc := newSpawnGateSvc()
	srv := spawnGateServer(t, svc, spawnAuthFixed{ok: true})
	code, _ := readBody(t, doSpawnPOST(t, srv, nil))
	if code != http.StatusCreated {
		t.Fatalf("status = %d", code)
	}
	if svc.spawned != 1 {
		t.Fatalf("spawned = %d", svc.spawned)
	}
}

func TestSpawn_CallerSessionWithoutValidCapabilityForbidden(t *testing.T) {
	svc := newSpawnGateSvc()
	srv := spawnGateServer(t, svc, spawnAuthFixed{ok: false})
	code, env := readBody(t, doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "bad",
	}))
	if code != http.StatusForbidden {
		t.Fatalf("status = %d env=%v", code, env)
	}
	if env["code"] != "SPAWN_CAPABILITY_INVALID" {
		t.Fatalf("code = %#v", env["code"])
	}
	if svc.spawned != 0 {
		t.Fatal("must not spawn")
	}
}

func TestSpawn_CallerCanSpawnFalseForbidden(t *testing.T) {
	svc := newSpawnGateSvc()
	s := svc.sessions["ao-1"]
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID: "implementor",
		ResolvedPermissions: domain.RoleExecutionPolicy{
			WorkspaceWrites: true,
			CanSpawn:        false,
		},
	}
	svc.sessions["ao-1"] = s
	srv := spawnGateServer(t, svc, spawnAuthFixed{ok: true})
	code, env := readBody(t, doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "ok",
	}))
	if code != http.StatusForbidden {
		t.Fatalf("status = %d env=%v", code, env)
	}
	if env["code"] != "SPAWN_FORBIDDEN" {
		t.Fatalf("code = %#v", env["code"])
	}
	if svc.spawned != 0 {
		t.Fatal("must not spawn")
	}
}

func TestSpawn_CallerCanSpawnTrueAllowed(t *testing.T) {
	svc := newSpawnGateSvc()
	s := svc.sessions["ao-1"]
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID: "orchestrator",
		ResolvedPermissions: domain.RoleExecutionPolicy{
			WorkspaceWrites: false,
			CanSpawn:        true,
		},
	}
	svc.sessions["ao-1"] = s
	srv := spawnGateServer(t, svc, spawnAuthFixed{ok: true})
	code, _ := readBody(t, doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "ok",
	}))
	if code != http.StatusCreated {
		t.Fatalf("status = %d", code)
	}
}

func TestSpawn_LegacySessionWithoutRoleAllowedWithCapability(t *testing.T) {
	svc := newSpawnGateSvc()
	srv := spawnGateServer(t, svc, spawnAuthFixed{ok: true})
	code, _ := readBody(t, doSpawnPOST(t, srv, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "ok",
	}))
	if code != http.StatusCreated {
		t.Fatalf("status = %d", code)
	}
}
