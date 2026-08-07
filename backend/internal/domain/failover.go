package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// Helpers over the frozen manual-failover contract in failover_contract.go.
// Everything here is pure: composition rules and folds over a slice of
// attempts. They live in domain rather than the manager because the composition
// rules are shared facts (an id format the storage primary key depends on) and
// because a fold with no I/O is far cheaper to test exhaustively here.

// FailoverAttemptID composes the attempt primary key,
// "<sessionId>:<incidentId>:<seq>".
//
// The ':' separator is exactly why ValidateIncidentID excludes ':' from the
// allowed set: without that exclusion an incident id could contain a separator
// and two different (incident, seq) pairs could compose the same key, which the
// UNIQUE constraint would then report as a retry of an unrelated attempt.
func FailoverAttemptID(sessionID SessionID, incidentID string, seq int) string {
	return string(sessionID) + ":" + strings.TrimSpace(incidentID) + ":" + strconv.Itoa(seq)
}

// FailoverLedgerID composes the lifecycle ledger id for one attempt's phase:
// "<sessionId>:<incidentId>:failover:<seq>:<phase>".
//
// Deliberately NOT appendPauseLedger's "<session>:<incident>:<kind>", which
// carries no sequence and would therefore collide across attempts for one
// incident — the second continuation's requested row would dedupe against the
// first's and vanish from the audit trail. Also not appendSwitchLedger's
// "<session>:<generation>:<phase>": the switch saga writes that exact key for
// this same generation, so reusing it would make the two sagas' rows collide.
func FailoverLedgerID(sessionID SessionID, incidentID string, seq int, phase LifecycleLedgerPhase) string {
	return fmt.Sprintf("%s:%s:%s:%d:%s",
		sessionID, strings.TrimSpace(incidentID), LifecycleKindFailover, seq, phase)
}

// LatestFailoverAttempt returns the highest-seq attempt, which is the one
// idempotence considers. Callers must not assume input order: the store returns
// attempts ordered, but a fold that depends on that is a silent bug the day a
// caller passes a filtered slice.
func LatestFailoverAttempt(attempts []FailoverAttempt) (FailoverAttempt, bool) {
	var best FailoverAttempt
	found := false
	for _, a := range attempts {
		if !found || a.Seq > best.Seq {
			best, found = a, true
		}
	}
	return best, found
}

// NextFailoverSeq is the sequence number a new attempt would take: one past the
// highest seq present, and 1 for an incident with no attempts yet.
//
// Derived from the max rather than from len() so a gap in the sequence (a
// rolled-back transaction leaves none, but a future partial-delete or an audit
// export round-trip could) cannot produce a seq that collides with an existing
// row and reads as a duplicate continuation.
func NextFailoverSeq(attempts []FailoverAttempt) int {
	latest, ok := LatestFailoverAttempt(attempts)
	if !ok {
		return 1
	}
	return latest.Seq + 1
}

// UsedFailoverTargets is NextFailoverRung's `used` argument: every rung already
// spent on this incident, in ANY state.
//
// Including terminal failures is the point. A rung that failed is spent, not
// retried — re-offering it would be automatic retry wearing a manual button,
// which is the one thing this MVP refuses to build. Including non-terminal ones
// matters too: an adopted attempt has not finished, but its rung is committed.
func UsedFailoverTargets(attempts []FailoverAttempt) []FailoverTarget {
	out := make([]FailoverTarget, 0, len(attempts))
	for _, a := range attempts {
		out = append(out, a.Target())
	}
	return out
}

// CountFailoverAttempts is the number of attempts spent against
// MaxFailoversPerIncident. Every attempt counts regardless of state, for the
// same reason UsedFailoverTargets includes them all.
func CountFailoverAttempts(attempts []FailoverAttempt) int {
	return len(attempts)
}

// ActiveFailoverAttempt returns the latest attempt when it is NON-TERMINAL,
// i.e. the attempt a duplicate Continue must adopt instead of starting a new
// one.
//
// This is contract section 6a's idempotence rule, expressed once. It keys on
// FailoverAttemptState.Terminal() rather than on == FailoverAttemptRequested:
// testing for `requested` alone misses `post_stop`, and a Continue that fails
// to adopt an unrecovered post_stop launches a SECOND runtime over a switch
// whose source is already stopped, while also spending a rung nobody asked for.
//
// Only the LATEST attempt is considered. An earlier non-terminal attempt is not
// adoptable: a later attempt exists only because a human already decided that
// one was over, and reviving it would move the session backwards down the
// ladder.
func ActiveFailoverAttempt(attempts []FailoverAttempt) (FailoverAttempt, bool) {
	latest, ok := LatestFailoverAttempt(attempts)
	if !ok || latest.State.Terminal() {
		return FailoverAttempt{}, false
	}
	return latest, true
}
