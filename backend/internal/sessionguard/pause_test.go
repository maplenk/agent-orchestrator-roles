package sessionguard

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func pausedRecord(state domain.ActivityState) domain.SessionRecord {
	rec := record(state, false)
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: "inc-1",
		Reason:     domain.PauseReasonUsageLimit,
		DetectedBy: domain.PauseDetectionStructured,
		PausedAt:   time.Now().UTC(),
	}
	return rec
}

// This is the whole of "durable pause; ZERO automatic send/restart" (MASTER_PLAN
// §7 rule 2) as an executable statement. Enforcement lives at the single
// pane-write choke point precisely so it is one table, not a rule each new
// caller has to remember.
//
// The split is by ORIGIN, not by method: a user is not locked out of their own
// session, and host-owned launch injection must still land or a Phase 3B
// failover could never deliver its prompt to the replacement.
func TestPauseFencesEveryAutomaticWrite(t *testing.T) {
	for _, tc := range []struct {
		name    string
		call    func(g *Guard, ctx context.Context) (Outcome, error)
		want    Outcome
		because string
	}{
		{
			name: "Nudge",
			call: func(g *Guard, ctx context.Context) (Outcome, error) { return g.Nudge(ctx, "s1", "hi") },
			want: SuppressedPaused, because: "lifecycle reactions are unsolicited AO writes",
		},
		{
			name: "NudgeCoordination",
			call: func(g *Guard, ctx context.Context) (Outcome, error) {
				return g.NudgeCoordination(ctx, "s1", "hi", func(domain.AgentHarness) bool { return true })
			},
			want: SuppressedPaused, because: "coordination messages are unsolicited AO writes",
		},
		{
			name: "DeliverAuto",
			call: func(g *Guard, ctx context.Context) (Outcome, error) { return g.DeliverAuto(ctx, "s1", "") },
			want: SuppressedPaused,
			because: "the send-confirm Enter re-send is AO's decision, not the user's — " +
				"this is the one that looks like a user send and is not",
		},
		{
			name: "Deliver",
			call: func(g *Guard, ctx context.Context) (Outcome, error) { return g.Deliver(ctx, "s1", "hi") },
			want: Sent, because: "pause stops AO acting on its own, it does not lock the user out",
		},
		{
			name: "DeliverHost",
			call: func(g *Guard, ctx context.Context) (Outcome, error) { return g.DeliverHost(ctx, "s1", "prompt") },
			want: Sent, because: "a failover target must receive its prompt, or pause blocks its own remedy",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Idle: nothing about the ACTIVITY state suppresses any of these, so
			// the only thing under test is the pause pin.
			store := &fakeStore{rec: pausedRecord(domain.ActivityIdle), ok: true}
			msg := &fakeMessenger{}
			g := New(store, msg, nil)

			got, err := tc.call(g, context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("%s on a paused session = %v, want %v — %s", tc.name, got, tc.want, tc.because)
			}
			wroteToPane := len(msg.sent) > 0
			if wroteToPane != (tc.want == Sent) {
				t.Fatalf("%s wrote-to-pane=%v but outcome=%v; the outcome and the actual write disagree",
					tc.name, wroteToPane, got)
			}
		})
	}
}

// Not paused, everything flows. Without this the table above passes just as
// well if the guard refused these writes unconditionally.
func TestUnpausedSessionTakesAutomaticWrites(t *testing.T) {
	store := &fakeStore{rec: record(domain.ActivityIdle, false), ok: true}
	msg := &fakeMessenger{}
	g := New(store, msg, nil)

	if got, err := g.Nudge(context.Background(), "s1", "hi"); got != Sent || err != nil {
		t.Fatalf("Nudge on an unpaused idle session = %v (%v), want Sent", got, err)
	}
	if len(msg.sent) != 1 {
		t.Fatalf("wrote %d messages, want 1", len(msg.sent))
	}
}

// An operator pause fences automatic writes exactly like a usage limit. The
// reason is for humans; the fence does not read it.
func TestPauseFencesRegardlessOfReason(t *testing.T) {
	rec := record(domain.ActivityIdle, false)
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: "inc-2",
		Reason:     domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator,
		PausedAt:   time.Now().UTC(),
	}
	g := New(&fakeStore{rec: rec, ok: true}, &fakeMessenger{}, nil)
	if got, _ := g.Nudge(context.Background(), "s1", "hi"); got != SuppressedPaused {
		t.Fatalf("operator pause did not fence an automatic write: %v", got)
	}
}

// Reporting order matters for the human reading the log. A session that hit a
// usage limit very often has an exited agent too, and "agent_exited" would send
// someone looking for a crash instead of a limit they must decide about.
func TestPauseIsReportedAheadOfExited(t *testing.T) {
	g := New(&fakeStore{rec: pausedRecord(domain.ActivityExited), ok: true}, &fakeMessenger{}, nil)
	if got, _ := g.Nudge(context.Background(), "s1", "hi"); got != SuppressedPaused {
		t.Fatalf("outcome = %v, want SuppressedPaused: the pause is the actionable fact", got)
	}
}

// Terminated still wins: there is no pane to write to, so that is the more
// fundamental refusal and the one whose remedy differs.
func TestTerminatedOutranksPaused(t *testing.T) {
	rec := pausedRecord(domain.ActivityIdle)
	rec.IsTerminated = true
	g := New(&fakeStore{rec: rec, ok: true}, &fakeMessenger{}, nil)
	if got, _ := g.Nudge(context.Background(), "s1", "hi"); got != SuppressedTerminated {
		t.Fatalf("outcome = %v, want SuppressedTerminated", got)
	}
}

func TestSuppressedPausedHasAString(t *testing.T) {
	if got := SuppressedPaused.String(); got != "suppressed_paused" {
		t.Fatalf("String() = %q; an unnamed outcome logs as suppressed_unknown and hides the reason", got)
	}
}
