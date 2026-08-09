package limits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// ErrHarnessUnsupported is returned when a harness has no reviewed detector, or
// has one but has not been promoted in the capability registry. It is the
// answer for EVERY production harness today and is not a fault.
var ErrHarnessUnsupported = errors.New("limit detection unsupported for this harness")

// SessionController is the durable pause command plus the one-shot automatic
// continuation decision. Both methods are implemented by sessionmanager.Manager:
// the router never selects a rung, relaunches a runtime, or owns attempt state.
type SessionController interface {
	PauseSession(ctx context.Context, id domain.SessionID, req sessionmanager.PauseRequest) (domain.SessionRecord, error)
	ContinueAutomaticFailover(
		ctx context.Context,
		id domain.SessionID,
		req sessionmanager.AutomaticFailoverRequest,
	) (sessionmanager.AutomaticFailoverResult, error)
}

// Router turns a structured adapter event into a durable pause, or into
// nothing. It owns no state, no timer and no retry: a caller that wants the
// event delivered again simply delivers it again, and the stable incident id
// makes that converge.
type Router struct {
	registry *Registry
	sessions SessionController
	logger   *slog.Logger
	// caps is the capability lookup, injectable so a test can promote a
	// harness without mutating the production registry.
	caps func(domain.AgentHarness) capabilities.Caps
}

// NewRouter builds a Router. A nil logger falls back to slog.Default.
func NewRouter(reg *Registry, sessions SessionController, logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{registry: reg, sessions: sessions, logger: logger, caps: capabilities.For}
}

// Route validates an adapter event, asks that harness's detector whether it is
// a limit, and pauses the session if it is.
//
// paused=false with a nil error is the ordinary outcome: the harness has no
// detector, or the detector says this event is not a limit. Neither is a fault.
func (r *Router) Route(ctx context.Context, ev Event) (paused bool, err error) {
	if err := ev.Validate(); err != nil {
		return false, err
	}
	if r == nil || r.sessions == nil {
		return false, fmt.Errorf("limit router: not wired")
	}

	// Capability gate FIRST, before the detector is consulted at all. A
	// detector that exists but has not been promoted must not be able to pause
	// anything — promotion is the reviewed step, and gating after detection
	// would make the registry the real authority instead of the capability
	// matrix.
	if !r.caps(ev.Harness).LimitDetectionSupported {
		return false, fmt.Errorf("%w: %s", ErrHarnessUnsupported, ev.Harness)
	}
	det, ok := r.registry.For(ev.Harness)
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrHarnessUnsupported, ev.Harness)
	}
	if det.Harness() != ev.Harness {
		// A detector answering for a harness it does not own would let one
		// adapter pause another's sessions.
		return false, fmt.Errorf("limit router: detector for %s answered an event for %s", det.Harness(), ev.Harness)
	}

	env, isLimit, err := det.Detect(ctx, ev)
	if err != nil {
		return false, fmt.Errorf("limit router: detect %s: %w", ev.Harness, err)
	}
	if !isLimit {
		return false, nil
	}

	// The ENVELOPE's harness must match too. The detector-vs-event check above
	// only proves the right adapter answered; this proves the adapter did not
	// then describe some other harness's limit, which is the value that ends up
	// durable in the pin and the ledger.
	if env.Harness != ev.Harness {
		return false, fmt.Errorf("limit router: envelope harness %q does not match the event's %q",
			env.Harness, ev.Harness)
	}

	// Re-validate the detector's output. The detector is adapter code; this
	// boundary does not take its word for the envelope being well formed, and
	// the envelope rules (versioned, object-only, bounded, closed kind) are
	// what stop prose from becoming evidence.
	raw, err := json.Marshal(env)
	if err != nil {
		return false, fmt.Errorf("limit router: encode envelope: %w", err)
	}
	if _, err := domain.ParseLimitEnvelope(string(raw)); err != nil {
		return false, fmt.Errorf("limit router: detector produced an invalid envelope: %w", err)
	}

	incident := IncidentID(env)
	rec, err := r.sessions.PauseSession(ctx, ev.SessionID, sessionmanager.PauseRequest{
		IncidentID:   incident,
		Reason:       domain.PauseReasonUsageLimit,
		DetectedBy:   domain.PauseDetectionStructured,
		EvidenceJSON: string(raw),
		// RetryAfter is carried for a human to read. Nothing schedules against
		// it — see domain.SessionPause.
		RetryAfter: env.ResetsAt,
		// Ownership, enforced atomically by the pin write. A limit belongs to
		// ONE harness process in ONE generation; by the time the report lands
		// the session may have switched, been relaunched, or entered a switch
		// saga, and pausing then would park a runtime that never hit it.
		Guard: domain.PauseGuard{
			ExpectHarness:          ev.Harness,
			ExpectRuntimeLaunchID:  ev.RuntimeLaunchID,
			RequireNoSwitchPending: true,
		},
	})
	if err != nil {
		return false, fmt.Errorf("limit router: pause %s: %w", ev.SessionID, err)
	}
	r.logger.Info("session paused by structured limit detection",
		"sessionID", ev.SessionID, "harness", ev.Harness,
		"incident", incident, "kind", env.Kind)

	// Automatic mode is a single immediate handoff to the accepted Continue
	// transaction. The manager reads the current opt-in, suppresses every later
	// automatic action for an incident with an attempt, and leaves failures
	// paused for manual Continue. There is intentionally no retry-after timer or
	// background worker here.
	auto, err := r.sessions.ContinueAutomaticFailover(ctx, ev.SessionID,
		sessionmanager.AutomaticFailoverRequest{IncidentID: incident})
	if err != nil {
		paused := rec.Metadata.Pause != nil
		if auto.Session.ID != "" {
			paused = auto.Session.Metadata.Pause != nil
		}
		return paused, fmt.Errorf("limit router: automatic failover %s: %w", ev.SessionID, err)
	}
	if auto.Enabled && auto.Attempted {
		r.logger.Info("session continued by opt-in automatic failover",
			"sessionID", ev.SessionID, "harness", ev.Harness,
			"incident", incident, "targetHarness", auto.Continue.Target.Harness,
			"targetModel", auto.Continue.Target.Model, "rung", auto.Continue.RungIndex)
	}
	return auto.Session.Metadata.Pause != nil, nil
}
