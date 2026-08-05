package domain

import "time"

// OrchestratorReplacementIntent records that a project's orchestrator
// replacement began, so a zero-owner interval can always be recovered.
//
// Retire-first semantics make some zero-owner window unavoidable: the
// orchestrator worktree is canonical per project and gitworktree.Create adopts
// an existing registration rather than failing, so the predecessor must release
// it before the successor can create it. If the spawn then fails, the project
// has no coordinator. The window is inherent; being STUCK in it is the defect.
//
// So intent is written BEFORE retirement and cleared only after a successor is
// live. Its presence means "this project is owed an orchestrator" — the same
// polarity as the reap queue's "this execution is owed a confirmed death", and
// deliberately not "a replacement was requested once", which cannot express an
// outstanding obligation.
type OrchestratorReplacementIntent struct {
	ProjectID ProjectID
	// RetiredSessionID is the predecessor being replaced, empty when there was
	// none. A first spawn still records intent: a crash between deciding to
	// spawn and spawning strands the project just as thoroughly.
	RetiredSessionID SessionID
	RequestedAt      time.Time
	// AttemptCount / LastAttemptAt / LastError are recovery bookkeeping, kept
	// rather than reset so a project that cannot be recovered is visible as
	// such instead of looking freshly requested on every boot.
	AttemptCount  int
	LastAttemptAt *time.Time
	LastError     string
}
