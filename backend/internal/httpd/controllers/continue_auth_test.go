package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Controller-level coverage for POST /sessions/{id}/continue.
//
// It had none. The service layer was tested against a fake commander and the
// CLI against a fake daemon, but nothing exercised the HTTP surface — which is
// where authorizeOperatorPause actually runs, where the strict decoder rejects
// a caller-supplied target, and where a failed follow-up read decides whether a
// completed continuation is reported as a success or a failure. An operator-only
// endpoint whose gate is never executed by a test is one edit away from being
// open, and the tests below are the ones that would notice.

// failoverGateSvc adds the optional failover surface to the shared spawn-gate
// double, with injectable failures so the degrade paths are reachable.
type failoverGateSvc struct {
	*spawnGateSvc
	continueCalls int
	previewCalls  int
	continueErr   error
	previewErr    error
	preview       sessionmanager.FailoverPreview
}

func (f *failoverGateSvc) ContinueFailover(
	_ context.Context, id domain.SessionID, incidentID string,
) (sessionsvc.ContinueFailoverOutcome, error) {
	f.continueCalls++
	if f.continueErr != nil {
		return sessionsvc.ContinueFailoverOutcome{}, f.continueErr
	}
	return sessionsvc.ContinueFailoverOutcome{
		IncidentID:   incidentID,
		GenerationID: "gen-1",
		Target:       domain.FailoverTarget{Harness: domain.HarnessCodex},
		RungIndex:    0,
		AttemptSeq:   1,
		Session:      f.sessions[id],
	}, nil
}

func (f *failoverGateSvc) FailoverPreview(
	_ context.Context, _ domain.SessionID,
) (sessionmanager.FailoverPreview, error) {
	f.previewCalls++
	if f.previewErr != nil {
		return sessionmanager.FailoverPreview{}, f.previewErr
	}
	return f.preview, nil
}

// continueGateServerWired builds the router around a service that DOES
// implement the optional failover surface. The shared spawnGateServer helper
// takes the base double, which does not, so the controller's type assertion
// would fail and every request would answer 500 before the behaviour under test
// was reached.
func continueGateServerWired(t *testing.T) (*failoverGateSvc, string, string) {
	t.Helper()
	base, plain := newSpawnGateSvcWithToken()
	base.sessions["mer-1"] = domain.Session{SessionRecord: domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
	}}
	svc := &failoverGateSvc{
		spawnGateSvc: base,
		preview: sessionmanager.FailoverPreview{
			NextRungIndex: -1,
			MaxAttempts:   domain.MaxFailoversPerIncident,
			Reason:        sessionmanager.FailoverReasonNotPaused,
		},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions:      svc,
		OperatorSpawn: opAuth{tok: "op-secret"},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return svc, plain, srv.URL
}

// The gate is the same one pause and resume use, and it must refuse the same
// principals. Continue is strictly more powerful than resume — it moves the
// session to another harness — so anything resume refuses, this must too.
func TestContinue_HeaderlessRejected(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", `{"incidentId":"inc-1"}`, nil)
	if code != http.StatusForbidden || env["code"] != "PAUSE_AUTH_REQUIRED" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.continueCalls != 0 {
		t.Fatal("a headerless request reached the service")
	}
}

// A managed agent holding a VALID spawn capability is still refused. A worker
// that can continue itself onto another harness has no pause at all, and a
// sibling that can do it to a competitor is worse.
func TestContinue_ManagedAgentForbiddenEvenWithValidCapability(t *testing.T) {
	svc, plain, base := continueGateServerWired(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Caller-Session-Id": "ao-1",
		"X-AO-Spawn-Capability":  plain,
	})
	if code != http.StatusForbidden || env["code"] != "PAUSE_AGENT_FORBIDDEN" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.continueCalls != 0 {
		t.Fatal("an agent principal reached the service")
	}
}

func TestContinue_OperatorTokenInvalid(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "wrong",
	})
	if code != http.StatusForbidden || env["code"] != "OPERATOR_CREDENTIAL_INVALID" {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.continueCalls != 0 {
		t.Fatal("an invalid operator credential reached the service")
	}
}

func TestContinue_OperatorTokenAllows(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "op-secret",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d env=%v", code, env)
	}
	if svc.continueCalls != 1 {
		t.Fatalf("continueCalls=%d, want 1", svc.continueCalls)
	}
}

// Contract §3: a caller-chosen target must be structurally impossible, not
// merely ignored. The wire rejects the field rather than silently dropping it,
// so a client cannot believe it steered the destination.
func TestContinue_CallerSuppliedTargetRejectedAtTheWire(t *testing.T) {
	for _, body := range []string{
		`{"incidentId":"inc-1","targetHarness":"claude-code"}`,
		`{"incidentId":"inc-1","targetModel":"opus"}`,
		`{"incidentId":"inc-1","roleId":"reviewer"}`,
	} {
		svc, _, base := continueGateServerWired(t)
		code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", body, map[string]string{
			"X-AO-Operator-Spawn-Token": "op-secret",
		})
		if code != http.StatusBadRequest || env["code"] != "INVALID_JSON" {
			t.Fatalf("body %s: status=%d env=%v; a caller-supplied target must be refused", body, code, env)
		}
		if svc.continueCalls != 0 {
			t.Fatalf("body %s: reached the service", body)
		}
	}
}

// The P1 this round fixed. ContinueFailover has already succeeded — the rung is
// spent, the pin is cleared, the target is live — so a failure in the follow-up
// preview read must not be reported as a failed continuation. The operator's
// natural retry would then answer SESSION_NOT_PAUSED, layering confusion on top
// of a move that worked.
func TestContinue_SucceedsEvenWhenTheFollowUpPreviewFails(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	svc.previewErr = errors.New("store unavailable")

	code, env := doPausePOST(t, base, "/api/v1/sessions/mer-1/continue", `{"incidentId":"inc-1"}`, map[string]string{
		"X-AO-Operator-Spawn-Token": "op-secret",
	})
	if code != http.StatusOK {
		t.Fatalf("status=%d env=%v; a completed continuation was reported as a failure", code, env)
	}
	if svc.continueCalls != 1 {
		t.Fatalf("continueCalls=%d, want exactly 1", svc.continueCalls)
	}
	// The outcome the caller needs comes from the mutation, not the read.
	if env["generationId"] != "gen-1" {
		t.Fatalf("generationId=%v, want gen-1", env["generationId"])
	}
	sess, _ := env["session"].(map[string]any)
	raw, _ := json.Marshal(sess["failover"])
	var fv map[string]any
	_ = json.Unmarshal(raw, &fv)
	if fv["reason"] != "unavailable" {
		t.Fatalf("failover.reason=%v, want unavailable — a preview that could not be computed must say so", fv["reason"])
	}
	if fv["available"] != false {
		t.Fatal("an uncomputable preview must never degrade toward available")
	}
}

func getSessionFailover(t *testing.T, base string) (any, bool) {
	t.Helper()
	resp, err := http.Get(base + "/api/v1/sessions/mer-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	sess, ok := env["session"].(map[string]any)
	if !ok {
		t.Fatalf("no session in response: %v", env)
	}
	v, present := sess["failover"]
	return v, present
}

// Contract §9: the block is null for the ORDINARY session — not paused, no
// ladder. This was a dead branch (it compared against the zero value, which the
// manager never produces), so every ordinary worker shipped a `no_ladder` block
// the contract said would be absent.
func TestSessionRead_FailoverNullForUnpausedSessionWithNoLadder(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	svc.preview = sessionmanager.FailoverPreview{
		NextRungIndex: -1,
		MaxAttempts:   domain.MaxFailoversPerIncident,
		Reason:        sessionmanager.FailoverReasonNoLadder,
	}
	v, present := getSessionFailover(t, base)
	if !present || v != nil {
		t.Fatalf("failover=%v (present=%v), want null for an unpaused session with no ladder", v, present)
	}
}

// The same verdict on a PAUSED session is not the ordinary case: the human is
// looking at this session and needs to be told why Continue is unavailable.
func TestSessionRead_FailoverPresentWhenPausedEvenWithNoLadder(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	rec := svc.sessions["mer-1"]
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: "inc-1", Reason: domain.PauseReasonOperator}
	svc.sessions["mer-1"] = rec
	svc.preview = sessionmanager.FailoverPreview{
		NextRungIndex: -1,
		MaxAttempts:   domain.MaxFailoversPerIncident,
		Reason:        sessionmanager.FailoverReasonNoLadder,
	}
	v, _ := getSessionFailover(t, base)
	fv, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("failover=%v, want a block explaining why Continue is unavailable", v)
	}
	if fv["reason"] != "no_ladder" {
		t.Fatalf("reason=%v, want no_ladder", fv["reason"])
	}
}

// A read whose preview could not be computed degrades THAT row rather than
// failing the response. This runs for every worker in a list, so propagating
// turned one unreadable project into a 500 across the whole fleet.
func TestSessionRead_PreviewFailureDegradesTheRowNotTheResponse(t *testing.T) {
	svc, _, base := continueGateServerWired(t)
	svc.previewErr = errors.New("project unreadable")

	resp, err := http.Get(base + "/api/v1/sessions/mer-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d; one unreadable preview must not fail the read", resp.StatusCode)
	}
	var env map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	sess := env["session"].(map[string]any)
	fv, ok := sess["failover"].(map[string]any)
	if !ok {
		t.Fatalf("failover=%v, want a block saying the preview was unavailable", sess["failover"])
	}
	if fv["reason"] != "unavailable" || fv["available"] != false {
		t.Fatalf("failover=%v, want available=false reason=unavailable", fv)
	}
}
