// Package limits is the boundary between a harness adapter observing a usage
// limit and AO durably pausing a session.
//
// It exists to be the ONLY way a limit can enter the system, and its shape is
// dictated by MASTER_PLAN §7 rule 1 — "structured / reviewed envelopes only
// (never free-text 'I hit a limit')".
//
// What this package deliberately does NOT contain, and must not grow:
//
//   - terminal-text parsing or scraping of any kind;
//   - regex or heuristic inference over agent output;
//   - any scheduler, timer, backoff or automatic retry.
//
// A Detector is handed an event its own adapter already structured. It never
// sees a pane. If a harness cannot produce a structured event, it does not get
// a detector, and AO simply never learns about its limits — which is the
// correct failure, because the alternative is guessing.
//
// Every harness is unsupported today. Enabling one requires captured, sanitized
// vendor fixtures and a separate capability promotion; the registry being empty
// is the current, intended state.
package limits

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// EventVersion is the only accepted adapter event version. Exact, not minimum:
// an unrecognised version is a contract the host has not reviewed, and the
// answer to that is to refuse rather than to guess which fields still mean what.
const EventVersion = 1

// EventKind is the closed set of structured adapter events this boundary
// accepts. It is closed for the same reason the envelope's discriminator is:
// an open string is a free-text channel one indirection removed.
type EventKind string

const (
	// EventProviderLimit is an adapter reporting that its provider refused work
	// because a usage window is exhausted.
	EventProviderLimit EventKind = "provider_limit"
)

// Valid reports whether k is a known event kind.
func (k EventKind) Valid() bool { return k == EventProviderLimit }

// Event is a STRUCTURED observation produced by a harness adapter. The adapter
// is responsible for having parsed its own machine-readable channel (an exit
// code, a JSON error body, a typed SDK error) into these fields. AO does not
// parse anything on its behalf.
type Event struct {
	// Version must equal EventVersion.
	Version int
	// Kind is the discriminator.
	Kind EventKind
	// Harness that observed it. Must match the detector's own harness.
	Harness domain.AgentHarness
	// SessionID the observation belongs to.
	SessionID domain.SessionID
	// Detail is a short adapter note carried into the envelope for a human.
	// It is a LEAF: nothing branches on it, ever.
	Detail string
}

// Validate checks the event shape before any detector sees it.
func (e Event) Validate() error {
	if e.Version != EventVersion {
		return fmt.Errorf("limit event: version must be %d, got %d", EventVersion, e.Version)
	}
	if !e.Kind.Valid() {
		return fmt.Errorf("limit event: unknown kind %q", e.Kind)
	}
	if strings.TrimSpace(string(e.Harness)) == "" {
		return fmt.Errorf("limit event: harness required")
	}
	if strings.TrimSpace(string(e.SessionID)) == "" {
		return fmt.Errorf("limit event: sessionId required")
	}
	return nil
}

// Detector is what a harness adapter implements to report a usage limit.
//
// Detect returns ok=false for "this event is not a limit", which must NOT be an
// error: that is the ordinary answer and an error would make the caller treat
// routine traffic as a fault.
type Detector interface {
	// Harness the detector speaks for.
	Harness() domain.AgentHarness
	// Detect maps a structured adapter event onto a limit envelope.
	Detect(ctx context.Context, ev Event) (domain.LimitEnvelopeV1, bool, error)
}

// Registry maps a harness to its detector. Empty is the intended state:
// no production harness has one, so no production harness can report a limit.
type Registry struct {
	byHarness map[domain.AgentHarness]Detector
}

// NewRegistry builds a registry. Passing no detectors is normal.
func NewRegistry(detectors ...Detector) *Registry {
	r := &Registry{byHarness: map[domain.AgentHarness]Detector{}}
	for _, d := range detectors {
		if d == nil {
			continue
		}
		r.byHarness[d.Harness()] = d
	}
	return r
}

// For returns the detector for a harness. ok=false means the harness cannot
// report limits, which is every production harness today.
func (r *Registry) For(h domain.AgentHarness) (Detector, bool) {
	if r == nil {
		return nil, false
	}
	d, ok := r.byHarness[h]
	return d, ok
}

// IncidentID derives a STABLE incident id from the envelope alone.
//
// Stability is the whole contract: PauseSession's ledger row is written before
// the pin, so a retry must present the same id or it opens a second incident
// for one real limit. Deriving it from the envelope means a duplicate delivery
// — the normal case for a polling adapter — converges instead of duplicating.
//
// Receipt time is deliberately NOT an input. Including it would make every
// delivery of the same limit a different incident, which is precisely the bug
// this function exists to prevent.
func IncidentID(env domain.LimitEnvelopeV1) string {
	var b strings.Builder
	b.WriteString(string(env.Kind))
	b.WriteByte('|')
	b.WriteString(string(env.Harness))
	b.WriteByte('|')
	b.WriteString(env.Scope)
	b.WriteByte('|')
	if env.ResetsAt != nil {
		b.WriteString(env.ResetsAt.UTC().Format("20060102T150405Z"))
	}
	sum := sha256.Sum256([]byte(b.String()))
	// Hex only, so it satisfies domain.ValidateIncidentID's charset by
	// construction rather than by hoping the inputs were tame.
	return "limit-" + hex.EncodeToString(sum[:])[:32]
}
