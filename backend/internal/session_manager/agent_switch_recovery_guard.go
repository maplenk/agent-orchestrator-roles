package sessionmanager

import (
	"context"
	"errors"
	"fmt"
)

// ErrActiveAgentSwitchRequiresEngine classifies a boot refusal caused by
// durable worker ownership that this binary cannot safely recover.
var ErrActiveAgentSwitchRequiresEngine = errors.New("session: active agent switch requires recovery-capable engine")

// ActiveAgentSwitchRequiresEngineError retains a typed error for daemon/API
// diagnostics while supporting errors.Is classification.
type ActiveAgentSwitchRequiresEngineError struct{}

func (*ActiveAgentSwitchRequiresEngineError) Error() string {
	return "ACTIVE_AGENT_SWITCH_REQUIRES_ENGINE: a nonterminal agent switch requires a recovery-capable engine"
}

func (*ActiveAgentSwitchRequiresEngineError) Unwrap() error {
	return ErrActiveAgentSwitchRequiresEngine
}

type agentSwitchRecoveryInspector interface {
	HasNonterminalAgentSwitch(context.Context) (bool, error)
}

func (m *Manager) validateAgentSwitchRecoveryCapability(ctx context.Context) error {
	inspector, ok := m.store.(agentSwitchRecoveryInspector)
	if !ok {
		// Narrow embedders created before the recovery guard cannot inspect the
		// table. Production SQLite implements this interface; test fakes without
		// durable switch storage retain their historical no-op behavior.
		return nil
	}
	active, err := inspector.HasNonterminalAgentSwitch(ctx)
	if err != nil {
		return fmt.Errorf("inspect nonterminal agent switches: %w", err)
	}
	if active && !m.canRecoverNonterminalAgentSwitch {
		return &ActiveAgentSwitchRequiresEngineError{}
	}
	return nil
}
