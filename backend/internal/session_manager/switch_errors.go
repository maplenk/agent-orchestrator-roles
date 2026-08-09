package sessionmanager

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var (
	// ErrSwitchOperationInProgress is a transient, process-local ownership
	// conflict. The caller may retry after the operation currently holding the
	// in-memory fence finishes. It deliberately does not classify as
	// ErrSwitchRecoveryRequired.
	ErrSwitchOperationInProgress = errors.New("session: switch operation already in progress")
	// ErrSwitchRecoveryRequired classifies durable switch ownership that must be
	// recovered rather than retried as a new operation.
	ErrSwitchRecoveryRequired = errors.New("session: switch recovery required")
)

// LegacySwitchRecoveryError reports the durable SwitchPending protocol used by
// the fork before agent-switch sagas were introduced. GenerationID identifies
// the one pending runtime generation that RecoverSwitchFromPostStop must drive.
type LegacySwitchRecoveryError struct {
	SessionID    domain.SessionID
	GenerationID string
}

func (e *LegacySwitchRecoveryError) Error() string {
	if generation := strings.TrimSpace(e.GenerationID); generation != "" {
		return fmt.Sprintf("session %s: legacy switch generation %s requires recovery", e.SessionID, generation)
	}
	return fmt.Sprintf("session %s: legacy switch requires recovery", e.SessionID)
}

// Is classifies legacy pending-switch ownership as recovery-required.
func (e *LegacySwitchRecoveryError) Is(target error) bool {
	return target == ErrSwitchRecoveryRequired
}

// AgentSwitchRecoveryError reports a nonterminal durable upstream agent-switch
// saga. It preserves domain.ErrAgentSwitchInProgress for callers that need the
// upstream classification while sharing ErrSwitchRecoveryRequired with legacy
// recovery ownership.
type AgentSwitchRecoveryError struct {
	SessionID domain.SessionID
	SwitchID  domain.AgentSwitchID
	State     domain.AgentSwitchState
}

func (e *AgentSwitchRecoveryError) Error() string {
	if e.SwitchID != "" {
		return fmt.Sprintf("session %s: agent switch %s (%s) requires recovery", e.SessionID, e.SwitchID, e.State)
	}
	return fmt.Sprintf("session %s: agent switch requires recovery", e.SessionID)
}

func (e *AgentSwitchRecoveryError) Unwrap() []error {
	return []error{ErrSwitchRecoveryRequired, domain.ErrAgentSwitchInProgress}
}

func legacySwitchRecoveryError(id domain.SessionID, generationID string) error {
	return &LegacySwitchRecoveryError{SessionID: id, GenerationID: strings.TrimSpace(generationID)}
}

func agentSwitchRecoveryError(sw domain.AgentSwitch) error {
	return &AgentSwitchRecoveryError{SessionID: sw.SessionID, SwitchID: sw.ID, State: sw.State}
}
