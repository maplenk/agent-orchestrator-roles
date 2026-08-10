package controllers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred"
)

func doOutputGET(t *testing.T, srvURL, path string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return resp.StatusCode, env
}

func TestSessionOutputOperatorReadIsBoundedAndHeaderlessIsRefused(t *testing.T) {
	svc := newFakeSessionService()
	svc.output = "FINAL REPORT\nall checks passed"
	srv := newSessionTestServer(t, svc)

	status, env := doOutputGET(t, srv.URL, "/api/v1/sessions/ao-1/output?lines=73", nil)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_AUTH_REQUIRED" || svc.outputCalls != 0 {
		t.Fatalf("headerless status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/ao-1/output?lines=73", map[string]string{
		"X-AO-Operator-Spawn-Token": "test-operator-spawn-token",
	})
	if status != http.StatusOK || env["output"] != svc.output || env["lines"] != float64(73) {
		t.Fatalf("operator status=%d env=%v", status, env)
	}
	if svc.outputCalls != 1 || svc.outputLines != 73 {
		t.Fatalf("calls=%d lines=%d", svc.outputCalls, svc.outputLines)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/ao-1/output?lines=1001", map[string]string{
		"X-AO-Operator-Spawn-Token": "test-operator-spawn-token",
	})
	if status != http.StatusBadRequest || env["code"] != "INVALID_OUTPUT_LINES" || svc.outputCalls != 1 {
		t.Fatalf("invalid lines status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}
}

func TestSessionOutputSessionPrincipalIsProjectScopedAndRequiresCanSpawn(t *testing.T) {
	svc := newFakeSessionService()
	plain := "caller-capability"
	caller := svc.sessions["ao-1"]
	caller.ProjectID = "pelican"
	caller.Metadata.SpawnCapabilityHash = spawncred.Hash(plain)
	caller.Metadata.Role = domain.SessionRoleBinding{
		RoleID:              "orchestrator",
		ResolvedPermissions: domain.RoleExecutionPolicy{CanSpawn: true, WorkspaceWrites: true},
	}
	svc.sessions["ao-1"] = caller
	svc.sessions["pelican-worker"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "pelican-worker", ProjectID: "pelican", Kind: domain.KindWorker}}
	svc.sessions["pelican-orchestrator"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "pelican-orchestrator", ProjectID: "pelican", Kind: domain.KindOrchestrator}}
	svc.sessions["other-worker"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "other-worker", ProjectID: "other", Kind: domain.KindWorker}}
	svc.output = "worker report"
	srv := newSessionTestServer(t, svc)
	headers := map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	}

	status, env := doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-worker/output", headers)
	if status != http.StatusOK || env["output"] != "worker report" || svc.outputCalls != 1 {
		t.Fatalf("same-project status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/other-worker/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_PROJECT_MISMATCH" || svc.outputCalls != 1 {
		t.Fatalf("cross-project status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-orchestrator/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_FORBIDDEN" || svc.outputCalls != 1 {
		t.Fatalf("non-worker status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}

	caller = svc.sessions["ao-1"]
	caller.Metadata.Role.ResolvedPermissions.CanSpawn = false
	svc.sessions["ao-1"] = caller
	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-worker/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_FORBIDDEN" || svc.outputCalls != 1 {
		t.Fatalf("no-canSpawn status=%d env=%v calls=%d", status, env, svc.outputCalls)
	}
}

func TestSessionOutputLegacyUnpinnedOrchestratorReadsOnlySameProjectWorker(t *testing.T) {
	svc := newFakeSessionService()
	plain := "legacy-orchestrator-capability"
	caller := svc.sessions["ao-1"]
	caller.Kind = domain.KindOrchestrator
	caller.ProjectID = "pelican"
	caller.Metadata.SpawnCapabilityHash = spawncred.Hash(plain)
	caller.Metadata.Role = domain.SessionRoleBinding{}
	svc.sessions["ao-1"] = caller
	svc.sessions["pelican-worker"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "pelican-worker", ProjectID: "pelican", Kind: domain.KindWorker}}
	svc.sessions["pelican-orchestrator"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "pelican-orchestrator", ProjectID: "pelican", Kind: domain.KindOrchestrator}}
	svc.sessions["other-worker"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "other-worker", ProjectID: "other", Kind: domain.KindWorker}}
	svc.output = "legacy-readable report"
	srv := newSessionTestServer(t, svc)
	headers := map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	}

	status, env := doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-worker/output", headers)
	if status != http.StatusOK || env["output"] != svc.output {
		t.Fatalf("same-project worker status=%d env=%v", status, env)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-orchestrator/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_FORBIDDEN" {
		t.Fatalf("orchestrator target status=%d env=%v", status, env)
	}

	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/other-worker/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_PROJECT_MISMATCH" {
		t.Fatalf("cross-project status=%d env=%v", status, env)
	}

	caller = svc.sessions["ao-1"]
	caller.Kind = domain.KindWorker
	svc.sessions["ao-1"] = caller
	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-worker/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_FORBIDDEN" {
		t.Fatalf("roleless worker status=%d env=%v", status, env)
	}

	caller = svc.sessions["ao-1"]
	caller.Kind = domain.KindOrchestrator
	caller.IsTerminated = true
	svc.sessions["ao-1"] = caller
	status, env = doOutputGET(t, srv.URL, "/api/v1/sessions/pelican-worker/output", headers)
	if status != http.StatusForbidden || env["code"] != "SESSION_READ_CAPABILITY_INVALID" {
		t.Fatalf("terminated caller status=%d env=%v", status, env)
	}
}
