package domain

import (
	"strings"
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
			EvidenceJSON: `{"version":1,"kind":"usage_limit","sourceKey":"win-1"}`,
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
		// The forgeries. Each is a way prose could pass a mere non-empty check,
		// so each must be refused at the one boundary every layer funnels
		// through. ParseLimitEnvelope covers these exhaustively; they are
		// repeated here to pin that Validate actually calls it.
		{"usage limit whose envelope is prose", func(p *SessionPause) {
			p.EvidenceJSON = "I hit a limit"
		}, true, "bare prose is not an envelope"},
		{"usage limit whose envelope is a JSON string", func(p *SessionPause) {
			p.EvidenceJSON = `"I hit a limit"`
		}, true, "valid JSON, but a string is still free text"},
		{"usage limit whose envelope is an array", func(p *SessionPause) {
			p.EvidenceJSON = `["I hit a limit"]`
		}, true, "an array is not the documented shape"},
		{"usage limit with an unversioned envelope", func(p *SessionPause) {
			p.EvidenceJSON = `{"kind":"usage_limit"}`
		}, true, "prose in braces is what an unversioned object looks like"},
		{"usage limit smuggling prose in an extra key", func(p *SessionPause) {
			p.EvidenceJSON = `{"version":1,"kind":"usage_limit","note":"I hit a limit"}`
		}, true, "unknown fields would carry free text into durable state"},
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

// The incident id is client-supplied and lands in three durable places:
// pause_json, the ledger's generation_id, and the ledger's PRIMARY key, which
// is built as "<session>:<incident>:<kind>". So "non-empty" is not a contract —
// an oversized value amplifies every write, a control character corrupts logs
// and ids, and a separator makes the key structurally ambiguous.
func TestValidateIncidentID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		ok   bool
	}{
		{"uuid", "7bd9dfc6-7fd5-4bf2-aba1-bc2d88740f25", true},
		{"slug", "incident-1", true},
		{"hash-like", "sha256.a09f7d18", true},
		{"underscored", "usage_limit_2026_08_06", true},
		{"at the cap", strings.Repeat("a", MaxIncidentIDBytes), true},

		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"over the cap", strings.Repeat("a", MaxIncidentIDBytes+1), false},
		{"newline", "inc\n1", false},
		{"null byte", "inc\x001", false},
		{"escape", "inc\x1b[31m", false},
		// The ledger primary key separator: allowing it would let the durable id
		// be read two different ways.
		{"colon", "inc:1", false},
		{"space", "inc 1", false},
		{"slash", "inc/1", false},
		{"quote", `inc"1`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateIncidentID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("rejected a legitimate id %q: %v", tc.id, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("accepted %q", tc.id)
			}
		})
	}
}

// And it binds through SessionPause.Validate, so a hand-edited row cannot carry
// one either.
func TestSessionPauseValidateBoundsTheIncident(t *testing.T) {
	p := &SessionPause{
		IncidentID: strings.Repeat("a", MaxIncidentIDBytes+1),
		Reason:     PauseReasonOperator,
		DetectedBy: PauseDetectionOperator,
		PausedAt:   time.Now().UTC(),
	}
	if err := p.Validate(); err == nil {
		t.Fatal("an oversized incident id passed Validate")
	}
}
