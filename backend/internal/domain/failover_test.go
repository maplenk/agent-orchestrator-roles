package domain_test

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func attempt(seq int, state domain.FailoverAttemptState, harness domain.AgentHarness, model string) domain.FailoverAttempt {
	return domain.FailoverAttempt{
		Seq: seq, State: state, ToHarness: harness, ToModel: model,
		GenerationID: "gen-" + string(rune('a'+seq)),
	}
}

func TestFailoverAttemptID_UsesTheSeparatorIncidentIDsExclude(t *testing.T) {
	got := domain.FailoverAttemptID("sess-1", "inc-9", 3)
	if got != "sess-1:inc-9:3" {
		t.Fatalf("id = %q, want sess-1:inc-9:3", got)
	}
	// The composition is only unambiguous because ':' cannot appear in an
	// incident id. If that ever stops holding, this key stops being unique.
	if err := domain.ValidateIncidentID("inc:9"); err == nil {
		t.Fatal("ValidateIncidentID accepted ':', which makes FailoverAttemptID ambiguous")
	}
}

func TestFailoverLedgerID_DistinctPerAttemptAndPhase(t *testing.T) {
	a := domain.FailoverLedgerID("s", "inc", 1, domain.LifecyclePhaseRequested)
	b := domain.FailoverLedgerID("s", "inc", 2, domain.LifecyclePhaseRequested)
	c := domain.FailoverLedgerID("s", "inc", 1, domain.LifecyclePhaseTargetAck)
	if a == b {
		t.Fatalf("two attempts for one incident share a ledger id (%q): the second would dedupe away", a)
	}
	if a == c {
		t.Fatalf("two phases of one attempt share a ledger id (%q)", a)
	}
	if a != "s:inc:failover:1:requested" {
		t.Fatalf("id = %q", a)
	}
}

func TestActiveFailoverAttempt_AdoptsNonTerminalIncludingPostStop(t *testing.T) {
	cases := []struct {
		name  string
		state domain.FailoverAttemptState
		adopt bool
	}{
		{"requested is adoptable", domain.FailoverAttemptRequested, true},
		// The whole point of contract 6a: keying on `requested` alone missed
		// this one, and a Continue that fails to adopt an unrecovered post_stop
		// opens a second runtime.
		{"post_stop is adoptable", domain.FailoverAttemptPostStop, true},
		{"acked is closed", domain.FailoverAttemptAcked, false},
		{"failed is closed", domain.FailoverAttemptFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := domain.ActiveFailoverAttempt([]domain.FailoverAttempt{
				attempt(1, tc.state, domain.HarnessCodex, ""),
			})
			if ok != tc.adopt {
				t.Fatalf("adopt = %v, want %v for state %q", ok, tc.adopt, tc.state)
			}
			if ok && got.Seq != 1 {
				t.Fatalf("adopted seq = %d, want 1", got.Seq)
			}
			if tc.state.Terminal() != !tc.adopt {
				t.Fatalf("Terminal() disagrees with adoptability for %q", tc.state)
			}
		})
	}
}

func TestActiveFailoverAttempt_OnlyTheLatestIsAdoptable(t *testing.T) {
	// An earlier attempt left non-terminal is NOT revived: a later attempt
	// exists only because a human decided the earlier one was over.
	_, ok := domain.ActiveFailoverAttempt([]domain.FailoverAttempt{
		attempt(1, domain.FailoverAttemptPostStop, domain.HarnessCodex, ""),
		attempt(2, domain.FailoverAttemptFailed, domain.HarnessGrok, ""),
	})
	if ok {
		t.Fatal("adopted an older non-terminal attempt behind a newer terminal one")
	}
	got, ok := domain.ActiveFailoverAttempt([]domain.FailoverAttempt{
		attempt(2, domain.FailoverAttemptRequested, domain.HarnessGrok, ""),
		attempt(1, domain.FailoverAttemptFailed, domain.HarnessCodex, ""),
	})
	if !ok || got.Seq != 2 {
		t.Fatalf("latest-by-seq not chosen from unordered input: seq=%d ok=%v", got.Seq, ok)
	}
}

func TestNextFailoverSeq_DerivedFromMaxNotLength(t *testing.T) {
	if got := domain.NextFailoverSeq(nil); got != 1 {
		t.Fatalf("first seq = %d, want 1", got)
	}
	// A gap must not produce a seq that collides with an existing row.
	got := domain.NextFailoverSeq([]domain.FailoverAttempt{
		attempt(1, domain.FailoverAttemptFailed, domain.HarnessCodex, ""),
		attempt(3, domain.FailoverAttemptFailed, domain.HarnessGrok, ""),
	})
	if got != 4 {
		t.Fatalf("next seq = %d, want 4 (len-based would give 3 and collide)", got)
	}
}

func TestUsedFailoverTargets_IncludesEveryState(t *testing.T) {
	used := domain.UsedFailoverTargets([]domain.FailoverAttempt{
		attempt(1, domain.FailoverAttemptFailed, domain.HarnessCodex, ""),
		attempt(2, domain.FailoverAttemptPostStop, domain.HarnessGrok, "pro"),
	})
	if len(used) != 2 {
		t.Fatalf("used = %d, want 2: a failed rung is spent, not retried", len(used))
	}
	if used[0].Harness != domain.HarnessCodex || used[1].Model != "pro" {
		t.Fatalf("used targets lost fields: %+v", used)
	}
}

// The fold feeds NextFailoverRung, so the two must agree end to end: a rung
// whose attempt FAILED must not be re-offered.
func TestUsedTargetsFeedNextRung_FailedRungIsNotReoffered(t *testing.T) {
	m := domain.RoleMap{
		SchemaVersion: domain.RoleMapSchemaVersion,
		Roles:         map[string]domain.RoleBinding{"impl": {Harness: domain.HarnessClaudeCode}},
		Failover: domain.FailoverConfig{Roles: map[string][]domain.FailoverTarget{
			"impl": {
				{Harness: domain.HarnessCodex},
				{Harness: domain.HarnessGrok},
			},
		}},
	}
	current := domain.FailoverTarget{Harness: domain.HarnessClaudeCode}

	first, idx, err := domain.NextFailoverRung(m, "impl", current, nil)
	if err != nil || first.Harness != domain.HarnessCodex || idx != 0 {
		t.Fatalf("first rung = %+v idx=%d err=%v", first, idx, err)
	}

	spent := domain.UsedFailoverTargets([]domain.FailoverAttempt{
		attempt(1, domain.FailoverAttemptFailed, domain.HarnessCodex, ""),
	})
	second, idx, err := domain.NextFailoverRung(m, "impl", current, spent)
	if err != nil {
		t.Fatalf("second rung: %v", err)
	}
	if second.Harness != domain.HarnessGrok || idx != 1 {
		t.Fatalf("second rung = %+v idx=%d, want grok at ladder index 1", second, idx)
	}

	exhausted := domain.UsedFailoverTargets([]domain.FailoverAttempt{
		attempt(1, domain.FailoverAttemptFailed, domain.HarnessCodex, ""),
		attempt(2, domain.FailoverAttemptPostStop, domain.HarnessGrok, ""),
	})
	if _, _, err := domain.NextFailoverRung(m, "impl", current, exhausted); !errors.Is(err, domain.ErrFailoverNoTarget) {
		t.Fatalf("exhausted ladder err = %v, want ErrFailoverNoTarget", err)
	}
}

func TestCountFailoverAttempts_BoundsIncludeTerminalOnes(t *testing.T) {
	att := make([]domain.FailoverAttempt, 0, domain.MaxFailoversPerIncident)
	for i := 1; i <= domain.MaxFailoversPerIncident; i++ {
		att = append(att, attempt(i, domain.FailoverAttemptFailed, domain.HarnessCodex, ""))
	}
	if domain.CountFailoverAttempts(att) != domain.MaxFailoversPerIncident {
		t.Fatalf("count = %d, want %d", domain.CountFailoverAttempts(att), domain.MaxFailoversPerIncident)
	}
}
