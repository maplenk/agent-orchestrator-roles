package domain

import "time"

// OrchestratorReapEntry is one outstanding obligation to confirm the death of a
// superseded orchestrator's execution surfaces.
//
// Rows are created by migration 0046, which reconciles databases holding more
// than one active orchestrator per project. Reconciliation terminates the
// losers and clears their workspace claim, which erases the only authoritative
// probe target — so the migration captures each loser's exact pre-reconciliation
// execution identity here first.
//
// Semantics are delete-on-authoritative-reap: the row's presence means "this
// execution is still owed a confirmed death". It is removed only once death is
// confirmed, never optimistically, and the schema enforces that with
// ON DELETE RESTRICT so the session cannot disappear while the obligation
// stands.
type OrchestratorReapEntry struct {
	SessionID ProjectScopedSessionID
	ProjectID ProjectID
	// RuntimeHandleID is the exact handle recorded before reconciliation.
	// Empty means the row genuinely had none: callers MUST fall back explicitly
	// rather than reading "no handle" as "already dead".
	RuntimeHandleID string
	RuntimeLaunchID string
	// WorkspacePath is informational — the canonical workspace the loser was
	// executing in. The reaper never removes it: it belongs to the survivor.
	WorkspacePath string
	QueuedAt      time.Time
	LastAttemptAt *time.Time
	AttemptCount  int
}

// ProjectScopedSessionID is the session this obligation refers to. Aliased for
// readability at call sites that also carry a ProjectID.
type ProjectScopedSessionID = SessionID
