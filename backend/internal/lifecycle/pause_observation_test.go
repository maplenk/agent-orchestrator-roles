package lifecycle

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func pausedWorking(id domain.SessionID, kind domain.SessionKind) domain.SessionRecord {
	rec := working(id)
	rec.Kind = kind
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: "inc-1",
		Reason:     domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator,
		PausedAt:   time.Now().UTC(),
	}
	return rec
}

// A confirmed-dead PAUSED session records the death without dying.
//
// Dogfood found the gap: reconcileLive correctly skipped tearing the session
// down, and then the runtime reaper terminated it anyway through this sink —
// so the row ended terminated, with no restore marker, contradicting the
// contract's active-with-a-dead-runtime state.
func TestRuntimeObservation_PausedSessionRecordsDeathWithoutTerminating(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			m, st, _ := newManager()
			st.sessions["mer-1"] = pausedWorking("mer-1", kind)

			if err := m.ApplyRuntimeObservation(ctx, "mer-1",
				ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
				t.Fatalf("observe: %v", err)
			}

			got := st.sessions["mer-1"]
			if got.IsTerminated {
				t.Fatal("a paused session was terminated by the runtime reaper; for an orchestrator " +
					"that also releases migration 0057's active slot, so a replacement could be spawned " +
					"while the paused owner is still restartable")
			}
			if got.Activity.State != domain.ActivityExited {
				t.Fatalf("activity = %q, want exited: the death must still be RECORDED, or the read "+
					"model says paused AND working and the UI cannot offer a restart", got.Activity.State)
			}
			if got.Metadata.Pause == nil || got.Metadata.Pause.IncidentID != "inc-1" {
				t.Fatalf("pause pin = %+v, want incident inc-1 retained", got.Metadata.Pause)
			}
			if _, stillInFlight := m.flights["mer-1"]; stillInFlight {
				t.Error("tool-flight state leaked: reaper-driven death fires no session-end hook, " +
					"so this is the last chance to release it")
			}
		})
	}
}

// Control: the identical observation on an UNPAUSED session still terminates.
// Without this the test above passes just as well if the sink stopped
// terminating anything.
func TestRuntimeObservation_UnpausedSessionStillTerminates(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1",
		ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("an unpaused dead session was not terminated")
	}
}

// The pause is re-read INSIDE the fence, so a resume that lands between the
// two passes terminates normally rather than being treated as still paused.
func TestRuntimeObservation_ResumeBetweenPassesTerminates(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = pausedWorking("mer-1", domain.KindWorker)

	// Clear the pin the way a resume would, between the usage-finalize pass
	// and the decision pass.
	st.onGetSession = func(rec domain.SessionRecord) domain.SessionRecord {
		rec.Metadata.Pause = nil
		return rec
	}

	if err := m.ApplyRuntimeObservation(ctx, "mer-1",
		ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("a session resumed between the passes was not terminated; the pin must be re-read under the fence")
	}
}
