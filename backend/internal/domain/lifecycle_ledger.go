package domain

import "time"

// LifecycleLedgerKind is an append-only event class (not full chat history).
type LifecycleLedgerKind string

const (
	LifecycleKindSwitch            LifecycleLedgerKind = "switch"
	LifecycleKindPause             LifecycleLedgerKind = "pause"
	LifecycleKindResume            LifecycleLedgerKind = "resume"
	LifecycleKindFailover          LifecycleLedgerKind = "failover"
	LifecycleKindFreshConversation LifecycleLedgerKind = "fresh_conversation"
	// LifecycleKindOrchestratorFresh is an orchestrator's in-place fresh
	// conversation. It is a distinct kind rather than reusing
	// fresh_conversation because recovery and audit must be able to tell the two
	// apart without re-reading the session: an orchestrator's recovery runs
	// under the project ownership gate and its handoff carries fleet state, so
	// "which saga is this?" cannot be answered by the phase alone.
	LifecycleKindOrchestratorFresh LifecycleLedgerKind = "orchestrator_fresh_conversation"
)

// Valid reports whether k is a known ledger kind.
func (k LifecycleLedgerKind) Valid() bool {
	switch k {
	case LifecycleKindSwitch, LifecycleKindPause, LifecycleKindResume,
		LifecycleKindFailover, LifecycleKindFreshConversation,
		LifecycleKindOrchestratorFresh:
		return true
	default:
		return false
	}
}

// LifecycleLedgerPhase is a durable saga phase string for switch/fresh events.
// Empty for kinds that do not use phases (e.g. simple pause markers).
type LifecycleLedgerPhase string

const (
	// LifecyclePhaseRequested is the durable start of a switch/fresh saga.
	LifecyclePhaseRequested LifecycleLedgerPhase = "requested"
	// LifecyclePhasePreStop means source is still usable; stop not committed.
	LifecyclePhasePreStop LifecycleLedgerPhase = "pre_stop"
	// LifecyclePhasePostStop means source stopped; handoff retained for retry.
	LifecyclePhasePostStop LifecycleLedgerPhase = "post_stop"
	// LifecyclePhaseTargetAck means target owns input; session fields updated.
	LifecyclePhaseTargetAck LifecycleLedgerPhase = "target_ack"
	// LifecyclePhaseFailed records a terminal saga failure (with phase context).
	LifecyclePhaseFailed LifecycleLedgerPhase = "failed"
)

// LifecycleLedgerRecord is one append-only lifecycle event for a session.
type LifecycleLedgerRecord struct {
	ID                    string
	SessionID             SessionID
	ProjectID             ProjectID
	Kind                  LifecycleLedgerKind
	Phase                 LifecycleLedgerPhase
	GenerationID          string
	FromHarness           AgentHarness
	ToHarness             AgentHarness
	FromModel             string
	ToModel               string
	RoleID                string
	SourceNativeSessionID string
	TargetNativeSessionID string
	// PayloadJSON holds SemanticHandoff / Observed / Compiled summary as JSON.
	PayloadJSON string
	CreatedAt   time.Time
}
