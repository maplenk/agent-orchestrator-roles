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

// Pause is the human's control over the fleet, so its gate is STRICTER than
// switch's: LAN or operator only, and a managed agent presenting session
// capability headers is refused outright rather than falling through.
//
// The incident id is in the session read model, so a worker can see its own.
// If the capability token were accepted here, that worker could POST /resume
// and lift the pause a human put on it — and a sibling could pause a worker to
// stop it competing. Neither is a pause.
func pauseGateServer(t *testing.T) (*spawnGateSvc, string, string) {
	t.Helper()
	svc, plain := newSpawnGateSvcWithToken()
	svc.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
	}}
	srv := spawnGateServer(t, svc, opAuth{tok: "op-secret"})
	return svc, plain, srv.URL
}

func TestPause_HeaderlessRejected(t *testing.T) {
	for _, path := range []string{"/api/v1/sessions/mer-1/pause", "/api/v1/sessions/mer-1/resume"} {
		svc, _, base := pauseGateServer(t)
		code, env := doPausePOST(t, base, path, `{"incidentId":"inc-1"}`, nil)
		if code != http.StatusForbidden || env["code"] != "PAUSE_AUTH_REQUIRED" {
			t.Fatalf("%s: status=%d env=%v", path, code, env)
		}
		if svc.pauseCalls != 0 || svc.resumeCalls != 0 {
			t.Fatalf("%s: reached the service", path)
		}
	}
}

// The finding. A managed agent holding a VALID spawn capability — the exact
// credential that authorizes it to spawn — must still be refused here.
func TestPause_ManagedAgentForbiddenEvenWithValidCapability(t *testing.T) {
	for _, path := range []string{"/api/v1/sessions/mer-1/pause", "/api/v1/sessions/mer-1/resume"} {
		svc, plain, base := pauseGateServer(t)
		code, env := doPausePOST(t, base, path, `{"incidentId":"inc-1"}`, map[string]string{
			"X-AO-Caller-Session-Id": "ao-1",
			"X-AO-Spawn-Capability":  plain,
		})
		if code != http.StatusForbidden || env["code"] != "PAUSE_AGENT_FORBIDDEN" {
			t.Fatalf("%s: status=%d env=%v; a worker that can lift its own pause is not paused", path, code, env)
		}
		if svc.pauseCalls != 0 || svc.resumeCalls != 0 {
			t.Fatalf("%s: an agent reached the service", path)
		}
	}
}

// A caller-session header alone, with no capability, is the same refusal —
// the point is the principal, not the token.
func TestPause_CallerSessionHeaderAloneForbidden(t *testing.T) {
	svc, _, base := pauseGateServer(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/resume", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
	})
	if code != http.StatusForbidden || env["code"] != "PAUSE_AGENT_FORBIDDEN" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.resumeCalls != 0 {
		t.Fatal("reached the service")
	}
}

func TestPause_OperatorTokenAllows(t *testing.T) {
	svc, _, base := pauseGateServer(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/pause", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "op-secret",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.pauseCalls != 1 {
		t.Fatalf("pauseCalls=%d", svc.pauseCalls)
	}
}

func TestPause_OperatorTokenInvalid(t *testing.T) {
	svc, _, base := pauseGateServer(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/resume", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "wrong",
	})
	if code != http.StatusForbidden || env["code"] != "OPERATOR_CREDENTIAL_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.resumeCalls != 0 {
		t.Fatal("reached the service")
	}
}

// An oversized body must be refused by the reader, not buffered and then
// rejected by the id-length check downstream.
func TestPause_OversizedBodyRejected(t *testing.T) {
	svc, _, base := pauseGateServer(t)
	big := make([]byte, 64<<10)
	for i := range big {
		big[i] = 'a'
	}
	code, _ := doPausePOST(t, base, "/api/v1/sessions/mer-1/pause",
		`{"incidentId":"`+string(big)+`"}`, map[string]string{
			"X-AO-Operator-Spawn-Token": "op-secret",
		})
	if code == http.StatusOK {
		t.Fatal("an oversized pause body was accepted")
	}
	if svc.pauseCalls != 0 {
		t.Fatal("an oversized body reached the service")
	}
}

func doPausePOST(t *testing.T, base, path, body string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
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

// LAN authentication is a first-class operator path: a LAN-authenticated client
// has already proved it is the human, so it needs no token. Without this the
// gate could be "operator token only" and every LAN client would be locked out
// of pausing anything.
func TestPause_LANAuthenticatedAllowedWithoutOperatorHeader(t *testing.T) {
	for _, tc := range []struct {
		path  string
		calls func(*spawnGateSvc) int
	}{
		{"/api/v1/sessions/mer-1/pause", func(s *spawnGateSvc) int { return s.pauseCalls }},
		{"/api/v1/sessions/mer-1/resume", func(s *spawnGateSvc) int { return s.resumeCalls }},
	} {
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

		code, env := doPausePOST(t, srv.URL, tc.path, `{"incidentId":"inc-1"}`, nil)
		if code != http.StatusOK {
			t.Fatalf("%s: LAN-auth status=%d env=%v", tc.path, code, env)
		}
		if tc.calls(svc) != 1 {
			t.Fatalf("%s: service calls=%d, want 1", tc.path, tc.calls(svc))
		}
	}
}
