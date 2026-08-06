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

type recordingPauseManager struct {
	commander
	pauseReq    sessionmanager.PauseRequest
	pauseCalls  int
	resumeID    string
	resumeCalls int
	err         error
}

func (m *recordingPauseManager) PauseSession(_ context.Context, id domain.SessionID, req sessionmanager.PauseRequest) (domain.SessionRecord, error) {
	m.pauseCalls++
	m.pauseReq = req
	if m.err != nil {
		return domain.SessionRecord{}, m.err
	}
	rec := domain.SessionRecord{ID: id}
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: req.IncidentID, Reason: req.Reason, DetectedBy: req.DetectedBy}
	return rec, nil
}

func (m *recordingPauseManager) ResumeSession(_ context.Context, id domain.SessionID, incident string) (domain.SessionRecord, error) {
	m.resumeCalls++
	m.resumeID = incident
	if m.err != nil {
		return domain.SessionRecord{}, m.err
	}
	return domain.SessionRecord{ID: id}, nil
}

func pauseService(m *recordingPauseManager) *Service { return &Service{manager: m} }

// The operator endpoint may NEVER record a usage limit. A usage limit needs a
// structured envelope only a harness adapter can produce; accepting the reason
// from an API client would hand every caller the free-text limit claim
// MASTER_PLAN §7 rule 1 exists to close, just spelled as an enum.
func TestPauseSession_RefusesAnyReasonButOperator(t *testing.T) {
	for _, reason := range []string{"usage_limit", "UsageLimit", "limit", "anything"} {
		m := &recordingPauseManager{}
		_, err := pauseService(m).PauseSession(context.Background(), "mer-1", "inc-1", reason)

		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Code != "PAUSE_REASON_INVALID" {
			t.Fatalf("reason %q: err = %v, want PAUSE_REASON_INVALID", reason, err)
		}
		if m.pauseCalls != 0 {
			t.Errorf("reason %q reached the manager", reason)
		}
	}
}

// An empty reason is the ordinary client call and means operator.
func TestPauseSession_DefaultsToOperator(t *testing.T) {
	m := &recordingPauseManager{}
	rec, err := pauseService(m).PauseSession(context.Background(), "mer-1", "inc-1", "")
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if m.pauseReq.Reason != domain.PauseReasonOperator {
		t.Errorf("reason = %q, want operator", m.pauseReq.Reason)
	}
	if m.pauseReq.DetectedBy != domain.PauseDetectionOperator {
		t.Errorf("detectedBy = %q, want operator: the evidence channel must match who reported it",
			m.pauseReq.DetectedBy)
	}
	if m.pauseReq.EvidenceJSON != "" {
		t.Errorf("the service invented evidence: %q", m.pauseReq.EvidenceJSON)
	}
	if rec.Metadata.Pause == nil {
		t.Error("returned record is not paused")
	}
}

func TestPauseSession_RequiresAnIncident(t *testing.T) {
	m := &recordingPauseManager{}
	_, err := pauseService(m).PauseSession(context.Background(), "mer-1", "   ", "operator")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "PAUSE_INCIDENT_REQUIRED" {
		t.Fatalf("err = %v, want PAUSE_INCIDENT_REQUIRED", err)
	}
	if m.pauseCalls != 0 {
		t.Error("an incident-less pause reached the manager")
	}
}

// Resume must carry the caller's incident through untouched. If the service
// dropped or substituted it, the manager's stale-resume protection would be
// operating on a value the human never saw.
func TestResumeSession_PassesTheCallerIncidentThrough(t *testing.T) {
	m := &recordingPauseManager{}
	if _, err := pauseService(m).ResumeSession(context.Background(), "mer-1", " inc-42 "); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if m.resumeID != "inc-42" {
		t.Fatalf("manager received incident %q, want the caller's inc-42", m.resumeID)
	}
}

func TestResumeSession_RequiresAnIncident(t *testing.T) {
	m := &recordingPauseManager{}
	_, err := pauseService(m).ResumeSession(context.Background(), "mer-1", "")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "PAUSE_INCIDENT_REQUIRED" {
		t.Fatalf("err = %v, want PAUSE_INCIDENT_REQUIRED", err)
	}
	if m.resumeCalls != 0 {
		t.Error("an incident-less resume reached the manager")
	}
}

// The four pause sentinels must reach clients as stable, actionable codes —
// through the real wrapping, since the manager wraps them with context.
func TestToAPIError_PauseFamily(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
		kind apierr.Kind
	}{
		{sessionmanager.ErrIncidentRequired, "PAUSE_INCIDENT_REQUIRED", apierr.KindInvalid},
		{sessionmanager.ErrAlreadyPaused, "SESSION_ALREADY_PAUSED", apierr.KindConflict},
		{sessionmanager.ErrNotPaused, "SESSION_NOT_PAUSED", apierr.KindConflict},
		{sessionmanager.ErrIncidentMismatch, "PAUSE_INCIDENT_MISMATCH", apierr.KindConflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			wrapped := fmt.Errorf("resume mer-1: %w", tc.err)
			var apiErr *apierr.Error
			if !errors.As(toAPIError(wrapped), &apiErr) {
				t.Fatalf("%v surfaced as a 500", tc.err)
			}
			if apiErr.Code != tc.code {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.code)
			}
			if apiErr.Kind != tc.kind {
				t.Fatalf("kind = %v, want %v", apiErr.Kind, tc.kind)
			}
		})
	}
}

// The manager's typed sentinels must reach the client as their mapped codes
// THROUGH the service, not just through toAPIError in isolation.
//
// Dogfood found this: a stale-incident resume correctly refused to lift the pin
// but answered 500 INTERNAL_ERROR, because the service returned the manager's
// error unmapped. The existing tests could not catch it — one exercised
// toAPIError with no service, the other exercised the service with a fake that
// never errored. Neither joined the two.
func TestPauseResume_ManagerSentinelsSurfaceMappedThroughTheService(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"mismatch", sessionmanager.ErrIncidentMismatch, "PAUSE_INCIDENT_MISMATCH"},
		{"not paused", sessionmanager.ErrNotPaused, "SESSION_NOT_PAUSED"},
		{"already paused", sessionmanager.ErrAlreadyPaused, "SESSION_ALREADY_PAUSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Wrapped exactly as the manager wraps it.
			m := &recordingPauseManager{err: fmt.Errorf("resume mer-1: %w", tc.err)}

			_, err := pauseService(m).ResumeSession(context.Background(), "mer-1", "inc-1")
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("resume surfaced %v as an unmapped error; the client sees a 500", err)
			}
			if apiErr.Code != tc.code {
				t.Fatalf("resume code = %q, want %q", apiErr.Code, tc.code)
			}

			_, err = pauseService(m).PauseSession(context.Background(), "mer-1", "inc-1", "")
			if !errors.As(err, &apiErr) {
				t.Fatalf("pause surfaced %v as an unmapped error", err)
			}
		})
	}
}
