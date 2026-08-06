package store

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func validPause() *domain.SessionPause {
	return &domain.SessionPause{
		IncidentID:   "inc-1",
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		Harness:      domain.HarnessCodex,
		EvidenceJSON: `{"version":1,"kind":"usage_limit","scope":"account"}`,
		PausedAt:     time.Now().UTC().Truncate(time.Second),
	}
}

// The whole point of the pin is that AO stops writing on its own. A decode that
// turned unreadable durable state into "not paused" would silently re-open
// every automatic path — the single worst failure this column can have — so
// corruption must surface as an error, never as nil.
func TestDecodePauseFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"malformed json", "{not-json", "corrupt"},
		{"missing incident", `{"reason":"usage_limit","detectedBy":"structured_envelope","evidenceJson":"{}","pausedAt":"2026-01-01T00:00:00Z"}`, "incidentId"},
		{"unknown reason", `{"incidentId":"i","reason":"vibes","pausedAt":"2026-01-01T00:00:00Z"}`, "unknown reason"},
		// A usage limit whose evidence channel is not structured is exactly the
		// free-text claim MASTER_PLAN §7 rule 1 forbids. Rejecting it at decode
		// means even a hand-edited database cannot smuggle one in.
		{"usage limit without structured evidence", `{"incidentId":"i","reason":"usage_limit","detectedBy":"operator","pausedAt":"2026-01-01T00:00:00Z"}`, "detectedBy"},
		{"usage limit with no evidence body", `{"incidentId":"i","reason":"usage_limit","detectedBy":"structured_envelope","pausedAt":"2026-01-01T00:00:00Z"}`, "structured envelope"},
		// Database corruption / hand editing. The row is the last place a
		// forged limit could enter, and it bypasses every Go-side caller — so
		// the envelope has to be re-validated on the way OUT, not just in.
		{"hand-edited prose envelope", `{"incidentId":"i","reason":"usage_limit","detectedBy":"structured_envelope","evidenceJson":"I hit a limit","pausedAt":"2026-01-01T00:00:00Z"}`, "JSON object"},
		{"hand-edited JSON-string envelope", `{"incidentId":"i","reason":"usage_limit","detectedBy":"structured_envelope","evidenceJson":"\"I hit a limit\"","pausedAt":"2026-01-01T00:00:00Z"}`, "a string"},
		{"hand-edited array envelope", `{"incidentId":"i","reason":"usage_limit","detectedBy":"structured_envelope","evidenceJson":"[1]","pausedAt":"2026-01-01T00:00:00Z"}`, "an array"},
		{"hand-edited unversioned envelope", `{"incidentId":"i","reason":"usage_limit","detectedBy":"structured_envelope","evidenceJson":"{\"kind\":\"usage_limit\"}","pausedAt":"2026-01-01T00:00:00Z"}`, "version must be 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := decodePause(tc.raw)
			if err == nil {
				t.Fatalf("decoded %s as %+v; corrupt pause state must not read as a session AO may write to", tc.name, p)
			}
			if p != nil {
				t.Fatalf("returned a pause alongside the error: %+v", p)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestDecodePauseEmptyIsNotPaused(t *testing.T) {
	for _, raw := range []string{"", "  ", "{}", "null"} {
		p, err := decodePause(raw)
		if err != nil || p != nil {
			t.Fatalf("raw %q: got (%+v, %v), want (nil, nil)", raw, p, err)
		}
	}
}

func TestEncodePauseRoundTrips(t *testing.T) {
	in := validPause()
	raw, err := encodePause(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodePause(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.IncidentID != in.IncidentID || out.Reason != in.Reason ||
		out.DetectedBy != in.DetectedBy || out.Harness != in.Harness ||
		out.EvidenceJSON != in.EvidenceJSON || !out.PausedAt.Equal(in.PausedAt) {
		t.Fatalf("round trip lost fields:\n in=%+v\nout=%+v", in, out)
	}
}

func TestEncodePauseNilIsEmpty(t *testing.T) {
	raw, err := encodePause(nil)
	if err != nil || raw != "" {
		t.Fatalf("nil pause: got (%q, %v)", raw, err)
	}
}

// encodePause refuses invalid state rather than writing it. Persisting an
// invalid pin would mean the next read fails closed forever — the session would
// be permanently unwritable with no way to resume, since resume reads first.
func TestEncodePauseRejectsInvalid(t *testing.T) {
	bad := validPause()
	bad.DetectedBy = domain.PauseDetectionOperator // usage_limit demands structured
	if _, err := encodePause(bad); err == nil {
		t.Fatal("encoded a usage_limit pause with operator evidence")
	}
}
