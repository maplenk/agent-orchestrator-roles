package sessionmanager

import (
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ORCHESTRATOR-FROZEN. See docs/roles/PHASE3B_MVP_CONTRACT.md §11.
//
// These are the manager-side types the service, HTTP and CLI layers compile
// against while the manager implementation is still being written in parallel.
// Agent B declares its own narrow commander interface over the two methods
// below — the same pattern service/session/pause.go already uses for pause —
// so neither agent blocks the other. Signatures here are the contract: if an
// implementation wants a different one, it stops and reports.

// ContinueFailoverRequest is the whole of a Continue request.
//
// One field, on purpose. There is no TargetHarness and no TargetModel: the host
// selects the next unused authorized rung, so a free-form target must be
// structurally impossible rather than validated away. Any future field that
// lets a caller influence the destination breaks the MVP's central guarantee.
type ContinueFailoverRequest struct {
	// IncidentID names the pause being answered. Required. It is NOT re-read
	// from the current pin: an action raised for incident A landing after B
	// replaced it must fail, not continue B on evidence nobody looked at.
	IncidentID string
}

// ContinueFailoverResult is the outcome of a completed continuation.
type ContinueFailoverResult struct {
	Session      domain.SessionRecord
	IncidentID   string
	GenerationID string
	// Target is the host-resolved destination. The caller learns it here; it
	// never supplies it.
	Target     domain.FailoverTarget
	RungIndex  int
	AttemptSeq int
	// Reused is true when this call adopted an attempt that was already in
	// flight (duplicate request, or a crash-recovered post_stop) instead of
	// starting a new one. The caller sees the same generation and sequence, and
	// no second attempt row or runtime was created.
	Reused bool
}

// FailoverPreviewReason explains an unavailable preview in a machine-readable
// way, so the desktop can disable the control and say why without inventing
// prose or re-deriving the ladder in React.
type FailoverPreviewReason string

const (
	FailoverReasonNone              FailoverPreviewReason = ""
	FailoverReasonNoRolePin         FailoverPreviewReason = "no_role_pin"
	FailoverReasonNoLadder          FailoverPreviewReason = "no_ladder"
	FailoverReasonLadderExhausted   FailoverPreviewReason = "ladder_exhausted"
	FailoverReasonLimitReached      FailoverPreviewReason = "limit_reached"
	FailoverReasonNotPaused         FailoverPreviewReason = "not_paused"
	FailoverReasonSwitchUnsupported FailoverPreviewReason = "switch_unsupported"
)

// FailoverPreview is the read-model answer to "what would Continue do?".
//
// Derived at read time and never stored — status stays derived from durable
// facts (AGENTS.md). It is computed by the manager rather than the service so
// that the ladder is resolved in exactly one place, by the same code that will
// execute it; a second resolution in the service is a second source of truth
// that can disagree with the button it labels.
type FailoverPreview struct {
	Available     bool
	RoleID        string
	NextTarget    domain.FailoverTarget
	NextRungIndex int
	AttemptsUsed  int
	MaxAttempts   int
	// IncidentID is the pause the preview was computed against. The surface
	// must submit THIS id, not re-read the pin at click time.
	IncidentID string
	Reason     FailoverPreviewReason
}

// Manager-level failover sentinels. domain owns the resolution errors
// (ErrFailoverNoTarget, ErrFailoverRoleRequired, ErrFailoverLimitReached);
// these are the ones only the saga can raise.
var (
	// ErrFailoverRecoveryRequired means an incomplete post_stop exists that does
	// NOT belong to this incident's latest attempt. Finishing it is a separate,
	// explicit act: driving a new continuation over an unrecovered switch is how
	// a session ends up with two runtimes.
	ErrFailoverRecoveryRequired = errors.New("session: incomplete switch recovery required before failover")
	// ErrFailoverNotWired reports a manager that does not implement the failover
	// surface, so the service answers explicitly instead of panicking on a type
	// assertion.
	ErrFailoverNotWired = errors.New("session: failover not wired on commander")
)
