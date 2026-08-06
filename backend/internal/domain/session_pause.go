package domain

import (
	"fmt"
	"strings"
	"time"
)

// PauseReason is why a session is durably paused. Deliberately a closed set:
// MASTER_PLAN §7 rule 1 is "structured / reviewed envelopes only (never
// free-text 'I hit a limit')", and a reason string an agent could author would
// be exactly that free-text channel wearing a struct's clothes.
type PauseReason string

const (
	// PauseReasonUsageLimit is a harness or provider usage limit, learned from a
	// structured envelope.
	PauseReasonUsageLimit PauseReason = "usage_limit"
	// PauseReasonOperator is a human deliberately parking the session.
	PauseReasonOperator PauseReason = "operator"
)

// PauseDetection records HOW the pause was learned. It is separate from the
// reason because the trust question ("who says so?") is not the same as the
// classification question ("what happened?"), and only the former decides
// whether AO may act on it.
type PauseDetection string

const (
	// PauseDetectionStructured means a harness adapter parsed a structured
	// limit envelope. The adapter must advertise limit_detection_supported.
	PauseDetectionStructured PauseDetection = "structured_envelope"
	// PauseDetectionOperator means a human asked for the pause through the API.
	PauseDetectionOperator PauseDetection = "operator"
)

// SessionPause is the durable pause pin. Non-nil means AO must not write to
// this session's pane on its own initiative — see sessionguard.
//
// Nothing in AO clears this except an explicit resume. In particular RetryAfter
// is advisory and is never scheduled against: MASTER_PLAN §7 rule 2 requires
// "durable pause; ZERO automatic send/restart", and a timer that resumes on its
// own is an automatic restart no matter which package owns the clock.
type SessionPause struct {
	// IncidentID groups this pause with the resume and (Phase 3B) failover
	// events that answer it. 3B's failover cursor and maxFailoversPerIncident
	// are per incident, so the identifier has to exist from the first pause
	// rather than be invented when failover lands.
	IncidentID string `json:"incidentId"`
	// Reason classifies the pause.
	Reason PauseReason `json:"reason"`
	// DetectedBy records the evidence channel. Validate ties it to Reason.
	DetectedBy PauseDetection `json:"detectedBy"`
	// Harness that hit the limit, recorded at pause time because a later
	// failover changes the session's current harness.
	Harness AgentHarness `json:"harness,omitempty"`
	// EvidenceJSON is the structured envelope exactly as the adapter reported
	// it, kept for audit. It is NOT parsed for control flow: 3A pauses on the
	// fact of a structured report, not on its contents.
	EvidenceJSON string `json:"evidenceJson,omitempty"`
	// RetryAfter is what the provider said, when it said anything. Advisory
	// only — see the type comment.
	RetryAfter *time.Time `json:"retryAfter,omitempty"`
	// PausedAt is when AO durably recorded the pause.
	PausedAt time.Time `json:"pausedAt"`
}

// MaxIncidentIDBytes bounds a client-supplied incident id. The id is durable
// and is copied into three places — pause_json, the ledger's generation_id, and
// the ledger's PRIMARY key — so an unbounded value is a write amplifier on all
// of them. Every real id is a uuid (36) or a short hash.
const MaxIncidentIDBytes = 128

// ValidateIncidentID bounds and constrains an externally supplied incident id.
//
// Non-empty is not enough: the value arrives from an API client and is spliced
// into the ledger primary key as "<session>:<incident>:<kind>", so control
// characters would corrupt logs and durable ids, and a separator would make the
// key structurally ambiguous. The charset is deliberately narrow rather than
// "printable" — uuids, hashes and slugs all fit, and nothing legitimate needs
// more.
func ValidateIncidentID(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("incident id: required")
	}
	if len(id) > MaxIncidentIDBytes {
		return fmt.Errorf("incident id: %d bytes exceeds the %d byte cap", len(id), MaxIncidentIDBytes)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("incident id: %q is not allowed; use [A-Za-z0-9._-]", r)
		}
	}
	return nil
}

// Validate enforces the structured-evidence rule. The load-bearing clause is
// the usage_limit one: a usage limit may ONLY be recorded from a structured
// envelope, so no free-text path can manufacture one. An operator pause is
// trusted because a human authored it directly.
func (p *SessionPause) Validate() error {
	if p == nil {
		return fmt.Errorf("pause: nil")
	}
	if err := ValidateIncidentID(p.IncidentID); err != nil {
		return fmt.Errorf("pause: %w", err)
	}
	if p.PausedAt.IsZero() {
		return fmt.Errorf("pause: pausedAt required")
	}
	switch p.Reason {
	case PauseReasonUsageLimit:
		if p.DetectedBy != PauseDetectionStructured {
			return fmt.Errorf("pause: usage_limit requires detectedBy=%s, got %q",
				PauseDetectionStructured, p.DetectedBy)
		}
		// Parsed, not merely present. "Non-empty" would admit `"I hit a limit"`
		// — a valid JSON string — which is precisely the free-text claim the
		// structured-envelope rule exists to exclude.
		if _, err := ParseLimitEnvelope(p.EvidenceJSON); err != nil {
			return fmt.Errorf("pause: usage_limit requires a structured envelope: %w", err)
		}
	case PauseReasonOperator:
		if p.DetectedBy != PauseDetectionOperator {
			return fmt.Errorf("pause: operator pause requires detectedBy=%s, got %q",
				PauseDetectionOperator, p.DetectedBy)
		}
	default:
		return fmt.Errorf("pause: unknown reason %q", p.Reason)
	}
	return nil
}

// Paused reports whether the session is durably paused. Written as a method on
// the metadata so call-sites cannot drift on what "paused" means.
func (m SessionMetadata) Paused() bool { return m.Pause != nil }
