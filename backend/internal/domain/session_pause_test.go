package domain

import (
	"testing"
	"time"
)

// MASTER_PLAN §7 rule 1 — "structured / reviewed envelopes only (never
// free-text 'I hit a limit')" — is enforced here and nowhere else. Every layer
// that records a pause funnels through Validate, so this table IS the rule.
func TestSessionPauseValidate(t *testing.T) {
	now := time.Now().UTC()
	base := func() *SessionPause {
		return &SessionPause{
			IncidentID:   "inc-1",
			Reason:       PauseReasonUsageLimit,
			DetectedBy:   PauseDetectionStructured,
			EvidenceJSON: `{"kind":"usage_limit"}`,
			PausedAt:     now,
		}
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*SessionPause)
		wantErr bool
		why     string
	}{
		{"structured usage limit", func(*SessionPause) {}, false,
			"the only way a usage limit may be recorded"},
		{"operator pause", func(p *SessionPause) {
			p.Reason = PauseReasonOperator
			p.DetectedBy = PauseDetectionOperator
			p.EvidenceJSON = ""
		}, false, "a human authored it directly, so no envelope is needed"},

		{"usage limit claimed by an operator channel", func(p *SessionPause) {
			p.DetectedBy = PauseDetectionOperator
		}, true, "this is the free-text claim the rule forbids"},
		{"usage limit with an empty envelope", func(p *SessionPause) {
			p.EvidenceJSON = "  "
		}, true, "structured detection with nothing structured to show"},
		{"operator pause claiming structured detection", func(p *SessionPause) {
			p.Reason = PauseReasonOperator
		}, true, "the evidence channel must match who actually reported it"},
		{"unknown reason", func(p *SessionPause) {
			p.Reason = PauseReason("vibes")
		}, true, "the reason set is closed"},
		{"missing incident", func(p *SessionPause) {
			p.IncidentID = " "
		}, true, "3B's per-incident failover cap counts against this id"},
		{"missing timestamp", func(p *SessionPause) {
			p.PausedAt = time.Time{}
		}, true, "an undated pause cannot be reasoned about after the fact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			tc.mutate(p)
			err := p.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("accepted %+v — %s", p, tc.why)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("rejected a legitimate pause (%s): %v", tc.why, err)
			}
		})
	}
}

func TestSessionPauseValidateNil(t *testing.T) {
	var p *SessionPause
	if err := p.Validate(); err == nil {
		t.Fatal("nil validated successfully")
	}
}

func TestMetadataPaused(t *testing.T) {
	var m SessionMetadata
	if m.Paused() {
		t.Fatal("zero metadata reports paused")
	}
	m.Pause = &SessionPause{}
	if !m.Paused() {
		t.Fatal("metadata with a pin does not report paused")
	}
}
