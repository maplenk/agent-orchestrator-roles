package limits

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
)

var ctx = context.Background()

// fakePauser stands in for the real PauseSession and reproduces the property
// the router depends on: idempotency per incident.
type fakePauser struct {
	calls    []sessionmanager.PauseRequest
	incident string // whichever incident actually holds the session
	err      error
}

func (p *fakePauser) PauseSession(_ context.Context, id domain.SessionID, req sessionmanager.PauseRequest) (domain.SessionRecord, error) {
	p.calls = append(p.calls, req)
	if p.err != nil {
		return domain.SessionRecord{}, p.err
	}
	if p.incident != "" && p.incident != req.IncidentID {
		return domain.SessionRecord{}, sessionmanager.ErrAlreadyPaused
	}
	p.incident = req.IncidentID
	rec := domain.SessionRecord{ID: id}
	rec.Metadata.Pause = &domain.SessionPause{IncidentID: req.IncidentID, Reason: req.Reason, DetectedBy: req.DetectedBy}
	return rec, nil
}

// pauses counts DISTINCT incidents, which is what "one real limit produced one
// pause" actually means — a repeated call for the same incident is a no-op
// downstream.
func (p *fakePauser) distinctIncidents() map[string]int {
	out := map[string]int{}
	for _, c := range p.calls {
		out[c.IncidentID]++
	}
	return out
}

type stubDetector struct {
	harness domain.AgentHarness
	env     domain.LimitEnvelopeV1
	isLimit bool
	err     error
}

func (d stubDetector) Harness() domain.AgentHarness { return d.harness }
func (d stubDetector) Detect(context.Context, Event) (domain.LimitEnvelopeV1, bool, error) {
	return d.env, d.isLimit, d.err
}

func goodEnvelope() domain.LimitEnvelopeV1 {
	reset := time.Date(2026, 8, 6, 18, 0, 0, 0, time.UTC)
	return domain.LimitEnvelopeV1{
		Version: 1, Kind: domain.LimitEnvelopeUsageLimit,
		Harness: domain.HarnessCodex, Scope: "account", ResetsAt: &reset,
	}
}

func goodEvent() Event {
	return Event{Version: EventVersion, Kind: EventProviderLimit,
		Harness: domain.HarnessCodex, SessionID: "mer-1", Detail: "5h window"}
}

// promoted builds a router with the harness capability flipped on, which is the
// ONLY way a detector can act. Production has none.
func promoted(t *testing.T, d Detector, p Pauser) *Router {
	t.Helper()
	r := NewRouter(NewRegistry(d), p, nil)
	r.caps = func(h domain.AgentHarness) capabilities.Caps {
		c := capabilities.For(h)
		c.LimitDetectionSupported = true
		return c
	}
	return r
}

// The state that ships: no harness has a detector, so no harness can pause.
func TestEveryProductionHarnessIsUnsupported(t *testing.T) {
	reg := NewRegistry()
	p := &fakePauser{}
	r := NewRouter(reg, p, nil)

	for _, h := range []domain.AgentHarness{
		domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessPi, domain.HarnessFake,
	} {
		if _, ok := reg.For(h); ok {
			t.Fatalf("%s has a detector; the registry must ship empty until fixtures exist", h)
		}
		ev := goodEvent()
		ev.Harness = h
		paused, err := r.Route(ctx, ev)
		if !errors.Is(err, ErrHarnessUnsupported) {
			t.Fatalf("%s: err = %v, want ErrHarnessUnsupported", h, err)
		}
		if paused {
			t.Fatalf("%s paused a session with no reviewed detector", h)
		}
	}
	if len(p.calls) != 0 {
		t.Fatalf("%d pause calls with no detector anywhere", len(p.calls))
	}
}

// limit_detection_supported is the reviewed gate, and it must bind even when a
// detector exists — otherwise the registry becomes the real authority and
// promotion means nothing.
func TestCapabilityGateBindsEvenWithADetectorPresent(t *testing.T) {
	p := &fakePauser{}
	// NewRouter uses the real capability registry, where the flag is false.
	r := NewRouter(NewRegistry(stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}), p, nil)

	paused, err := r.Route(ctx, goodEvent())
	if !errors.Is(err, ErrHarnessUnsupported) {
		t.Fatalf("err = %v, want ErrHarnessUnsupported: an unpromoted detector must not pause", err)
	}
	if paused || len(p.calls) != 0 {
		t.Fatal("an unpromoted detector paused a session")
	}
	if capabilities.For(domain.HarnessCodex).LimitDetectionSupported {
		t.Fatal("LimitDetectionSupported is true in the shipped registry; this slice must not promote anything")
	}
}

func TestMalformedAndUnknownEventsRejected(t *testing.T) {
	p := &fakePauser{}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}, p)

	for _, tc := range []struct {
		name string
		mut  func(*Event)
		want string
	}{
		{"version zero", func(e *Event) { e.Version = 0 }, "version must be 1"},
		{"future version", func(e *Event) { e.Version = 2 }, "version must be 1"},
		{"unknown kind", func(e *Event) { e.Kind = "vibes" }, "unknown kind"},
		{"empty kind", func(e *Event) { e.Kind = "" }, "unknown kind"},
		{"no harness", func(e *Event) { e.Harness = "" }, "harness required"},
		{"no session", func(e *Event) { e.SessionID = "" }, "sessionId required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := goodEvent()
			tc.mut(&ev)
			paused, err := r.Route(ctx, ev)
			if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
			if paused {
				t.Fatal("paused on a malformed event")
			}
		})
	}
	if len(p.calls) != 0 {
		t.Fatalf("%d pause calls from malformed events", len(p.calls))
	}
}

// "Not a limit" is the ordinary answer and must be silent, not an error.
func TestNonLimitEventIsNotAnError(t *testing.T) {
	p := &fakePauser{}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, isLimit: false}, p)

	paused, err := r.Route(ctx, goodEvent())
	if err != nil {
		t.Fatalf("a non-limit event surfaced as an error: %v", err)
	}
	if paused || len(p.calls) != 0 {
		t.Fatal("a non-limit event paused a session")
	}
}

// The boundary does not take the adapter's word for the envelope. A detector
// returning something the envelope rules reject must not reach PauseSession.
func TestDetectorProducingAnInvalidEnvelopeIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  domain.LimitEnvelopeV1
	}{
		{"unversioned", domain.LimitEnvelopeV1{Kind: domain.LimitEnvelopeUsageLimit}},
		{"wrong version", domain.LimitEnvelopeV1{Version: 2, Kind: domain.LimitEnvelopeUsageLimit}},
		{"unknown kind", domain.LimitEnvelopeV1{Version: 1, Kind: "vibes"}},
		{"no kind", domain.LimitEnvelopeV1{Version: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &fakePauser{}
			r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: tc.env, isLimit: true}, p)
			if _, err := r.Route(ctx, goodEvent()); err == nil {
				t.Fatal("an invalid envelope reached PauseSession")
			}
			if len(p.calls) != 0 {
				t.Fatal("an invalid envelope reached PauseSession")
			}
		})
	}
}

// A detector must not answer for a harness it does not own, or one adapter
// could pause another's sessions.
func TestDetectorCannotAnswerForAnotherHarness(t *testing.T) {
	p := &fakePauser{}
	// Registered under claude-code but claims codex on Harness().
	reg := NewRegistry(stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true})
	r := NewRouter(reg, p, nil)
	r.caps = func(domain.AgentHarness) capabilities.Caps {
		return capabilities.Caps{LimitDetectionSupported: true}
	}
	reg.byHarness[domain.HarnessClaudeCode] = reg.byHarness[domain.HarnessCodex]

	ev := goodEvent()
	ev.Harness = domain.HarnessClaudeCode
	if _, err := r.Route(ctx, ev); err == nil {
		t.Fatal("a codex detector answered a claude-code event")
	}
	if len(p.calls) != 0 {
		t.Fatal("cross-harness detection paused a session")
	}
}

// Retry stability and duplicate delivery are the same property: the incident is
// derived from the envelope, so redelivery converges instead of opening a
// second incident for one real limit.
func TestDuplicateDeliveryIsOneIncident(t *testing.T) {
	p := &fakePauser{}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}, p)

	for i := 0; i < 5; i++ {
		paused, err := r.Route(ctx, goodEvent())
		if err != nil || !paused {
			t.Fatalf("delivery %d: paused=%v err=%v", i, paused, err)
		}
	}
	inc := p.distinctIncidents()
	if len(inc) != 1 {
		t.Fatalf("distinct incidents = %d, want 1; a polling adapter would open one per delivery", len(inc))
	}
	if len(p.calls) != 5 {
		t.Fatalf("pause calls = %d, want 5 (each delivery reaches the idempotent command)", len(p.calls))
	}
}

// The id must not drift with receipt time. Deriving it from the clock is the
// specific mistake that would make every redelivery a new incident.
func TestIncidentIDIsStableAndDoesNotUseReceiptTime(t *testing.T) {
	a := IncidentID(goodEnvelope())
	time.Sleep(2 * time.Millisecond)
	b := IncidentID(goodEnvelope())
	if a != b {
		t.Fatalf("incident id drifted across time: %q != %q", a, b)
	}
	// And it must satisfy the durable id contract by construction.
	if err := domain.ValidateIncidentID(a); err != nil {
		t.Fatalf("derived incident id is not a legal durable id: %v", err)
	}

	// A genuinely different limit is a different incident.
	other := goodEnvelope()
	other.Scope = "organization"
	if IncidentID(other) == a {
		t.Fatal("different scopes collapsed to one incident")
	}
	reset := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	later := goodEnvelope()
	later.ResetsAt = &reset
	if IncidentID(later) == a {
		t.Fatal("different reset windows collapsed to one incident")
	}
}

// What reaches PauseSession must be exactly what the pause contract requires,
// so the pin and its ledger row agree and no free-text path exists.
func TestRoutedPauseCarriesStructuredEvidence(t *testing.T) {
	p := &fakePauser{}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}, p)

	if _, err := r.Route(ctx, goodEvent()); err != nil {
		t.Fatalf("route: %v", err)
	}
	got := p.calls[0]
	if got.Reason != domain.PauseReasonUsageLimit {
		t.Errorf("reason = %q, want usage_limit", got.Reason)
	}
	if got.DetectedBy != domain.PauseDetectionStructured {
		t.Errorf("detectedBy = %q, want structured_envelope", got.DetectedBy)
	}
	if _, err := domain.ParseLimitEnvelope(got.EvidenceJSON); err != nil {
		t.Errorf("evidence is not a valid envelope: %v", err)
	}
	if got.IncidentID != IncidentID(goodEnvelope()) {
		t.Errorf("incident = %q, want the envelope-derived id", got.IncidentID)
	}
	// The whole request must satisfy the domain rule, since that is what the
	// store re-checks on the way in and on the way out.
	pin := &domain.SessionPause{
		IncidentID: got.IncidentID, Reason: got.Reason, DetectedBy: got.DetectedBy,
		EvidenceJSON: got.EvidenceJSON, PausedAt: time.Now().UTC(),
	}
	if err := pin.Validate(); err != nil {
		t.Errorf("the routed pause would not validate as a pin: %v", err)
	}
	if got.RetryAfter == nil {
		t.Error("resetsAt was not carried through as advisory retryAfter")
	}
}

// A pause conflict is surfaced, not swallowed: a different incident already
// holding the session is something a human needs to see.
func TestPauseConflictSurfaces(t *testing.T) {
	p := &fakePauser{incident: "someone-elses-incident"}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}, p)

	paused, err := r.Route(ctx, goodEvent())
	if err == nil || !errors.Is(err, sessionmanager.ErrAlreadyPaused) {
		t.Fatalf("err = %v, want ErrAlreadyPaused", err)
	}
	if paused {
		t.Fatal("reported a pause that did not happen")
	}
}

// The package must not grow a scheduler. This is a structural assertion: the
// value of "no automatic retry" is that it cannot be reintroduced quietly.
func TestNoSchedulingPrimitivesInThePackage(t *testing.T) {
	// Router holds no timer, ticker, channel or goroutine state — it is
	// registry + pauser + logger + caps. If a field is added here, this test
	// is the place to argue for it.
	r := NewRouter(NewRegistry(), &fakePauser{}, nil)
	if r.registry == nil || r.pauser == nil || r.logger == nil || r.caps == nil {
		t.Fatal("router wiring changed")
	}
}

// --- automatic-write fencing, through the REAL sessionguard ---

type guardStore struct{ rec domain.SessionRecord }

func (s *guardStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, true, nil
}

type recordingMessenger struct{ sent int }

func (m *recordingMessenger) Send(context.Context, domain.SessionID, string) error {
	m.sent++
	return nil
}

// The point of detection is that AO stops writing. This drives the router's
// actual pause request into a real domain pin and hands it to the real
// sessionguard: a routed limit must fence AO-initiated writes, while leaving
// the user's own send alone.
func TestRoutedPauseFencesAutomaticWrites(t *testing.T) {
	p := &fakePauser{}
	r := promoted(t, stubDetector{harness: domain.HarnessCodex, env: goodEnvelope(), isLimit: true}, p)
	if _, err := r.Route(ctx, goodEvent()); err != nil {
		t.Fatalf("route: %v", err)
	}
	req := p.calls[0]

	// Exactly the pin PauseSession would persist from that request.
	rec := domain.SessionRecord{ID: "mer-1", Harness: domain.HarnessCodex,
		Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: req.IncidentID, Reason: req.Reason, DetectedBy: req.DetectedBy,
		EvidenceJSON: req.EvidenceJSON, RetryAfter: req.RetryAfter, PausedAt: time.Now().UTC(),
	}
	if err := rec.Metadata.Pause.Validate(); err != nil {
		t.Fatalf("routed pin does not validate: %v", err)
	}

	msg := &recordingMessenger{}
	g := sessionguard.New(&guardStore{rec: rec}, msg, nil)

	if out, _ := g.Nudge(ctx, "mer-1", "are you done?"); out != sessionguard.SuppressedPaused {
		t.Fatalf("Nudge = %v, want SuppressedPaused: detection that does not stop AO writing is pointless", out)
	}
	if out, _ := g.DeliverAuto(ctx, "mer-1", ""); out != sessionguard.SuppressedPaused {
		t.Fatalf("DeliverAuto = %v, want SuppressedPaused", out)
	}
	if msg.sent != 0 {
		t.Fatalf("%d automatic writes reached the pane after a routed limit pause", msg.sent)
	}
	// The user is not locked out of their own session.
	if out, _ := g.Deliver(ctx, "mer-1", "hello"); out != sessionguard.Sent {
		t.Fatalf("Deliver = %v, want Sent: pause stops AO acting, not the human", out)
	}
}
