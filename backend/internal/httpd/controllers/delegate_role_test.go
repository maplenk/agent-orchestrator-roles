package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"

	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// recordingDelegateSvc captures the input the controller builds. Everything
// else on the session service comes from spawnGateSvc unchanged.
type recordingDelegateSvc struct {
	*spawnGateSvc
	got sessionsvc.DelegateTaskInput
}

func (f *recordingDelegateSvc) DelegateTask(_ context.Context, in sessionsvc.DelegateTaskInput) (sessionsvc.DelegateTaskOutcome, error) {
	f.got = in
	return sessionsvc.DelegateTaskOutcome{WorkerID: "w-1"}, nil
}

// The decode boundary is where a field silently disappears — a body key with no
// struct field parses cleanly and the daemon then sees an empty role, which on
// a strict map is ROLE_REQUIRED and on a non-strict one is a silently wrong
// spawn. Neither reaches the client as "the role was dropped", so it is pinned
// here at the boundary itself rather than inferred downstream.
func TestDelegateDecodesRoleID(t *testing.T) {
	base, _ := newSpawnGateSvcWithToken()
	svc := &recordingDelegateSvc{spawnGateSvc: base}
	srv := spawnGateServerFor(t, svc)

	body := `{"projectId":"ao","brief":"ship it","roleId":"  implementor  "}`
	resp, err := http.Post(srv.URL+"/api/v1/orchestrators/delegate", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if svc.got.RoleID != "implementor" {
		t.Fatalf("RoleID = %q, want %q (trimmed)", svc.got.RoleID, "implementor")
	}
}

// An omitted role must stay empty rather than acquire a default. A default here
// would be a second role map living in the transport layer, and it would make
// the strict map's ROLE_REQUIRED unreachable — the one error that tells a
// person their delegation needs a target.
func TestDelegateWithoutRoleSendsNoRole(t *testing.T) {
	base, _ := newSpawnGateSvcWithToken()
	svc := &recordingDelegateSvc{spawnGateSvc: base}
	srv := spawnGateServerFor(t, svc)

	body := `{"projectId":"ao","brief":"ship it"}`
	resp, err := http.Post(srv.URL+"/api/v1/orchestrators/delegate", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if svc.got.RoleID != "" {
		t.Fatalf("RoleID = %q, want empty", svc.got.RoleID)
	}
}

// spawnGateServer takes the concrete fake; delegation needs the recording
// wrapper, so the deps are built from the interface here.
func spawnGateServerFor(t *testing.T, svc controllers.SessionService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)
	return srv
}
