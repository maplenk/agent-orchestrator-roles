package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type recordingFailoverManager struct {
	commander
	continueID    domain.SessionID
	continueReq   sessionmanager.ContinueFailoverRequest
	continueCalls int
	continueRes   sessionmanager.ContinueFailoverResult
	continueErr   error
	previewID     domain.SessionID
	previewCalls  int
	previewRes    sessionmanager.FailoverPreview
	previewErr    error
}

func (m *recordingFailoverManager) ContinueFailover(
	_ context.Context,
	id domain.SessionID,
	req sessionmanager.ContinueFailoverRequest,
) (sessionmanager.ContinueFailoverResult, error) {
	m.continueCalls++
	m.continueID = id
	m.continueReq = req
	if m.continueErr != nil {
		return sessionmanager.ContinueFailoverResult{}, m.continueErr
	}
	return m.continueRes, nil
}

func (m *recordingFailoverManager) FailoverPreview(
	_ context.Context,
	id domain.SessionID,
) (sessionmanager.FailoverPreview, error) {
	m.previewCalls++
	m.previewID = id
	if m.previewErr != nil {
		return sessionmanager.FailoverPreview{}, m.previewErr
	}
	return m.previewRes, nil
}

func failoverService(m commander) *Service {
	return &Service{manager: m, store: newFakeStore()}
}

func TestContinueFailover_PassesOnlyTrimmedIncidentToManager(t *testing.T) {
	m := &recordingFailoverManager{continueRes: sessionmanager.ContinueFailoverResult{
		Session: domain.SessionRecord{
			ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		},
		IncidentID:   "inc-42",
		GenerationID: "gen-7",
		Target:       domain.FailoverTarget{Harness: domain.HarnessCodex},
		RungIndex:    0,
		AttemptSeq:   1,
		Reused:       true,
	}}

	out, err := failoverService(m).ContinueFailover(context.Background(), "mer-1", " inc-42 ")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if m.continueCalls != 1 || m.continueID != "mer-1" {
		t.Fatalf("manager calls=%d id=%q", m.continueCalls, m.continueID)
	}
	if m.continueReq != (sessionmanager.ContinueFailoverRequest{IncidentID: "inc-42"}) {
		t.Fatalf("manager request = %#v", m.continueReq)
	}
	if out.IncidentID != "inc-42" || out.GenerationID != "gen-7" ||
		out.Target.Harness != domain.HarnessCodex || out.Target.Model != "" ||
		out.RungIndex != 0 || out.AttemptSeq != 1 || !out.Reused {
		t.Fatalf("out = %#v", out)
	}
	if out.Session.ID != "mer-1" || out.Session.Harness != domain.HarnessCodex {
		t.Fatalf("session = %#v", out.Session)
	}
}

func TestContinueFailover_ValidatesIncidentBeforeManager(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		code string
	}{
		{name: "missing", id: "   ", code: "PAUSE_INCIDENT_REQUIRED"},
		{name: "too long", id: strings.Repeat("a", 129), code: "PAUSE_INCIDENT_INVALID"},
		{name: "invalid character", id: "inc:1", code: "PAUSE_INCIDENT_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &recordingFailoverManager{}
			_, err := failoverService(m).ContinueFailover(context.Background(), "mer-1", tc.id)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if m.continueCalls != 0 {
				t.Fatal("invalid incident reached manager")
			}
		})
	}
}

func TestContinueFailover_ManagerSentinelsSurfaceMapped(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{name: "stale incident", err: sessionmanager.ErrIncidentMismatch, code: "PAUSE_INCIDENT_MISMATCH"},
		{name: "not paused", err: sessionmanager.ErrNotPaused, code: "SESSION_NOT_PAUSED"},
		{name: "no target", err: domain.ErrFailoverNoTarget, code: "FAILOVER_NO_TARGET"},
		{name: "role required", err: domain.ErrFailoverRoleRequired, code: "FAILOVER_ROLE_REQUIRED"},
		{name: "limit reached", err: domain.ErrFailoverLimitReached, code: "FAILOVER_LIMIT_REACHED"},
		{name: "recovery required", err: sessionmanager.ErrFailoverRecoveryRequired, code: "FAILOVER_RECOVERY_REQUIRED"},
		{name: "switch in progress", err: sessionmanager.ErrSwitchOperationInProgress, code: "SWITCH_IN_PROGRESS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &recordingFailoverManager{continueErr: fmt.Errorf("continue mer-1: %w", tc.err)}
			_, err := failoverService(m).ContinueFailover(context.Background(), "mer-1", "inc-1")
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestFailoverPreview_DelegatesWithoutResolving(t *testing.T) {
	want := sessionmanager.FailoverPreview{
		Available:     true,
		RoleID:        "implementor",
		NextTarget:    domain.FailoverTarget{Harness: domain.HarnessCodex, Model: "o3"},
		NextRungIndex: 2,
		AttemptsUsed:  1,
		MaxAttempts:   domain.MaxFailoversPerIncident,
		IncidentID:    "inc-1",
	}
	m := &recordingFailoverManager{previewRes: want}
	got, err := failoverService(m).FailoverPreview(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if got != want || m.previewCalls != 1 || m.previewID != "mer-1" {
		t.Fatalf("got=%#v calls=%d id=%q", got, m.previewCalls, m.previewID)
	}
}

func TestFailoverService_UnwiredManagerReturnsFrozenSentinel(t *testing.T) {
	svc := failoverService(&fakeCommander{})
	if _, err := svc.ContinueFailover(context.Background(), "mer-1", "inc-1"); !errors.Is(err, sessionmanager.ErrFailoverNotWired) {
		t.Fatalf("continue err = %v", err)
	}
	if _, err := svc.FailoverPreview(context.Background(), "mer-1"); !errors.Is(err, sessionmanager.ErrFailoverNotWired) {
		t.Fatalf("preview err = %v", err)
	}
}

func TestToAPIError_FailoverFamily(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		kind apierr.Kind
	}{
		{domain.ErrFailoverNoTarget, "FAILOVER_NO_TARGET", apierr.KindConflict},
		{domain.ErrFailoverRoleRequired, "FAILOVER_ROLE_REQUIRED", apierr.KindInvalid},
		{domain.ErrFailoverLimitReached, "FAILOVER_LIMIT_REACHED", apierr.KindConflict},
		{sessionmanager.ErrFailoverRecoveryRequired, "FAILOVER_RECOVERY_REQUIRED", apierr.KindConflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			var apiErr *apierr.Error
			if !errors.As(toAPIError(fmt.Errorf("wrapped: %w", tc.err)), &apiErr) {
				t.Fatalf("%v surfaced unmapped", tc.err)
			}
			if apiErr.Code != tc.code || apiErr.Kind != tc.kind {
				t.Fatalf("api error = %#v, want code=%s kind=%v", apiErr, tc.code, tc.kind)
			}
		})
	}
}
