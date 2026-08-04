package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/authctx"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func doSwitchPOST(t *testing.T, srv *httptest.Server, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
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

func TestSwitch_HeaderlessRejected(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	// Seed target session id for path
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, nil)
	if code != http.StatusForbidden || env["code"] != "SWITCH_AUTH_REQUIRED" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.switchCalls != 0 {
		t.Fatal("must not switch")
	}
}

func TestSwitch_FreshHeaderlessRejected(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/fresh-conversation", `{}`, nil)
	if code != http.StatusForbidden || env["code"] != "SWITCH_AUTH_REQUIRED" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.freshCalls != 0 {
		t.Fatal("must not fresh")
	}
}

func TestSwitch_OperatorTokenAllows(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, _ := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "op-secret",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if svc.switchCalls != 1 {
		t.Fatalf("switchCalls=%d", svc.switchCalls)
	}
}

func TestSwitch_OperatorTokenInvalid(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "wrong",
	})
	if code != http.StatusForbidden || env["code"] != "OPERATOR_SWITCH_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
}

func TestSwitch_ManagedWorkerCanSpawnFalseForbidden(t *testing.T) {
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
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusForbidden || env["code"] != "SWITCH_FORBIDDEN" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.switchCalls != 0 {
		t.Fatal("must not switch")
	}
}

func TestSwitch_ManagedOrchestratorCanSpawnAllowed(t *testing.T) {
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
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, _ := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/fresh-conversation", `{"objective":"x"}`, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if svc.freshCalls != 1 {
		t.Fatalf("freshCalls=%d", svc.freshCalls)
	}
}

func TestSwitch_BadCapabilityRejected(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	s := svc.sessions["ao-1"]
	s.Metadata.Role = domain.SessionRoleBinding{
		RoleID:              "orchestrator",
		ResolvedPermissions: domain.RoleExecutionPolicy{CanSpawn: true},
	}
	svc.sessions["ao-1"] = s
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	code, env := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  "not-the-token",
	})
	if code != http.StatusForbidden || env["code"] != "SWITCH_CAPABILITY_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
}

func TestSwitch_LANAuthenticatedTrustedWithoutOperatorHeader(t *testing.T) {
	svc, _ := newSpawnGateSvcWithToken()
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: svc, OperatorSpawn: opAuth{tok: "op-secret"}}
	inner := httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(authctx.WithLANAuthenticated(r.Context())))
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	code, _ := doSwitchPOST(t, srv, "/api/v1/sessions/mer-1/switch", `{"targetHarness":"codex"}`, nil)
	if code != http.StatusOK {
		t.Fatalf("LAN-auth switch status=%d", code)
	}
}
