package domain

import (
	"errors"
	"strings"
	"time"
)

// This file is the ORCHESTRATOR-FROZEN half of the Phase 3B manual-failover
// contract (docs/roles/PHASE3B_MVP_CONTRACT.md). It exists so the four parallel
// agents compile against one another without waiting: it holds only the types
// and the pure resolution rule that cross an agent boundary. Durable storage,
// the manager saga, the service/API surface and the desktop all build on top of
// it and none of them may edit it.

// MaxFailoversPerIncident bounds continuations for one incident.
//
// Deliberately a constant and not a RoleMap field. A new field in FailoverConfig
// changes RoleMap.SHA256()'s hash document, and that hash is durably pinned on
// every existing session as role_map_sha256 — so making the bound configurable
// would silently drift every live session's pin. The ladder length already
// dominates this number (rungs are single-use per incident, so the effective
// bound is min(len(ladder), MaxFailoversPerIncident)); configurability can come
// later, with a migration story for the pinned hash.
const MaxFailoversPerIncident = 8

// FailoverAttemptState is the durable lifecycle of one continuation attempt.
type FailoverAttemptState string

const (
	// FailoverAttemptRequested means the attempt is durable and the switch saga
	// either is running or died mid-flight. Recovery keys on this state.
	FailoverAttemptRequested FailoverAttemptState = "requested"
	// FailoverAttemptAcked means the target owns input (switch target_ack) and
	// the pause has been cleared for this incident.
	FailoverAttemptAcked FailoverAttemptState = "acked"
	// FailoverAttemptFailed means the continuation did not complete. The pause
	// survives, the rung is spent, and nothing is retried automatically.
	FailoverAttemptFailed FailoverAttemptState = "failed"
)

// Valid reports whether s is a known attempt state. The storage CHECK
// constraint mirrors this set; a state Go accepts but SQLite rejects is a
// feature that cannot run.
func (s FailoverAttemptState) Valid() bool {
	switch s {
	case FailoverAttemptRequested, FailoverAttemptAcked, FailoverAttemptFailed:
		return true
	default:
		return false
	}
}

// FailoverAttempt is one durable continuation attempt for one incident.
//
// It is deliberately separate from the lifecycle ledger. The ledger is an
// append-only audit of saga phases; this is the authoritative answer to "which
// rungs has this incident already spent, and is one in flight right now?" —
// a question that must be answerable by a single indexed read at Continue time
// and must survive a crash between the ledger write and the launch.
type FailoverAttempt struct {
	// ID is "<sessionId>:<incidentId>:<seq>". The separator is why
	// ValidateIncidentID excludes ':' — the same composition rule the ledger
	// primary key uses.
	ID          string
	SessionID   SessionID
	ProjectID   ProjectID
	IncidentID  string
	Seq         int
	RoleID      string
	FromHarness AgentHarness
	FromModel   string
	ToHarness   AgentHarness
	ToModel     string
	// RungIndex is the position in roleMap.failover.roles[roleId] that was
	// selected. Kept durable so an audit can tell which ladder entry was spent
	// even after the role map is edited.
	RungIndex int
	// GenerationID is the switch saga's generation (== RuntimeLaunchID). Empty
	// until the saga has begun; recovery matches on it.
	GenerationID string
	State        FailoverAttemptState
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Target returns the attempt's destination as a ladder target.
func (a FailoverAttempt) Target() FailoverTarget {
	return FailoverTarget{Harness: a.ToHarness, Model: strings.TrimSpace(a.ToModel)}
}

// Failover resolution errors. The service maps these to the frozen API codes;
// the manager returns them unwrapped-enough that errors.Is holds.
var (
	// ErrFailoverNoTarget means the role has no ladder, or every rung is
	// already spent for this incident. The pause is NOT lifted by this.
	ErrFailoverNoTarget = errors.New("failover: no unused authorized target for this role")
	// ErrFailoverRoleRequired means the session carries no durable role pin, so
	// there is no ladder to consult and no host-authorized target exists.
	ErrFailoverRoleRequired = errors.New("failover: session has no durable role pin")
	// ErrFailoverLimitReached means MaxFailoversPerIncident is exhausted. The
	// session stays paused — that is what the bound is for.
	ErrFailoverLimitReached = errors.New("failover: attempt bound reached for this incident")
)

// sameTarget compares two ladder targets exactly.
//
// Empty model means "provider default" on BOTH sides and is never a wildcard,
// matching ResolveAuthorizedSwitchModel. Harness comparison is exact too: the
// role map is validated at config-save, so a rung's harness is already
// canonical and lower-casing here would invent an equivalence the authorization
// path does not share.
func sameTarget(a, b FailoverTarget) bool {
	return a.Harness == b.Harness && strings.TrimSpace(a.Model) == strings.TrimSpace(b.Model)
}

// NextFailoverRung picks the host-authorized target for the next continuation.
//
// current is the session's live (harness, model); used is every rung already
// spent on THIS incident, in any state. The rules, in order:
//
//   - candidates are m.Failover.Roles[roleID] in declared order — the primary
//     binding is not a rung, so a first continuation cannot re-select the
//     current target (FailoverConfig's own documented rule);
//   - a rung equal to current is skipped, which also covers a ladder that
//     redundantly lists the primary;
//   - a rung already in used is skipped: a rung that FAILED is spent, not
//     retried. Automatic retry is exactly what this MVP refuses to build, and
//     re-offering a rung that just failed is that feature wearing a manual
//     button;
//   - the first survivor wins; none leaves ErrFailoverNoTarget.
//
// The returned index is the position in the configured ladder, not the position
// among survivors, so it stays meaningful in a durable audit record.
//
// Every rung it can return is by construction inside
// RoleAuthorizedSwitchTargets, so capability validation on both sides of the
// move applies unchanged. This function does not consult capabilities itself:
// a rung that is authorized but whose harness cannot switch must fail loudly at
// the saga (ErrSwitchNotSupported), not be silently skipped over here — DoD
// invariant 9, no silent degrade.
func NextFailoverRung(m RoleMap, roleID string, current FailoverTarget, used []FailoverTarget) (FailoverTarget, int, error) {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return FailoverTarget{}, -1, ErrFailoverRoleRequired
	}
	m = m.WithDefaults()
	if m.IsZero() {
		return FailoverTarget{}, -1, ErrFailoverNoTarget
	}
	if _, ok := m.Roles[roleID]; !ok {
		return FailoverTarget{}, -1, ErrFailoverNoTarget
	}
	for i, rung := range m.Failover.Roles[roleID] {
		if rung.Harness == "" || !rung.Harness.IsKnown() {
			continue
		}
		if sameTarget(rung, current) {
			continue
		}
		spent := false
		for _, u := range used {
			if sameTarget(rung, u) {
				spent = true
				break
			}
		}
		if spent {
			continue
		}
		return FailoverTarget{Harness: rung.Harness, Model: strings.TrimSpace(rung.Model)}, i, nil
	}
	return FailoverTarget{}, -1, ErrFailoverNoTarget
}
