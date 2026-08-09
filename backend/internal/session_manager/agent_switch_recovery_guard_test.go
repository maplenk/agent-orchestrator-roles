package sessionmanager

import (
	"context"
	"errors"
	"testing"
)

type recoveryInspectionStore struct {
	Store
	active bool
	err    error
}

func (s recoveryInspectionStore) HasNonterminalAgentSwitch(context.Context) (bool, error) {
	return s.active, s.err
}

type recoveryCapableInspectionStore struct {
	*switchTestStore
	active bool
	err    error
}

func (s *recoveryCapableInspectionStore) HasNonterminalAgentSwitch(context.Context) (bool, error) {
	return s.active, s.err
}

func TestAgentSwitchRecoveryCapabilityGuard(t *testing.T) {
	t.Run("empty or terminal-only storage needs no engine", func(t *testing.T) {
		m := New(Deps{Store: recoveryInspectionStore{Store: newFakeStore()}})
		if err := m.validateAgentSwitchRecoveryCapability(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nonterminal ownership refuses a build without recovery", func(t *testing.T) {
		m := New(Deps{Store: recoveryInspectionStore{Store: newFakeStore(), active: true}})
		err := m.Reconcile(context.Background())
		if !errors.Is(err, ErrActiveAgentSwitchRequiresEngine) {
			t.Fatalf("Reconcile error = %v, want ACTIVE_AGENT_SWITCH_REQUIRES_ENGINE", err)
		}
		var typed *ActiveAgentSwitchRequiresEngineError
		if !errors.As(err, &typed) {
			t.Fatalf("Reconcile error = %T, want typed recovery-capability error", err)
		}
	})

	t.Run("drain build may disable initiation while retaining recovery", func(t *testing.T) {
		store := &recoveryCapableInspectionStore{switchTestStore: newSwitchTestStore(), active: true}
		m := New(Deps{
			Store: store,
			AgentSwitchCapabilities: &AgentSwitchCapabilities{
				InitiationEnabled: false,
				RecoveryEnabled:   true,
			},
		})
		if m.agentSwitchInitiationEnabled {
			t.Fatal("drain build unexpectedly accepts new switches")
		}
		if !m.canRecoverNonterminalAgentSwitch {
			t.Fatal("drain build lost recovery capability")
		}
		if err := m.Reconcile(context.Background()); err != nil {
			t.Fatalf("drain-mode boot was blocked: %v", err)
		}
	})

	t.Run("inspection failure is boot-fatal", func(t *testing.T) {
		inspectErr := errors.New("inspect failed")
		m := New(Deps{Store: recoveryInspectionStore{Store: newFakeStore(), err: inspectErr}})
		if err := m.Reconcile(context.Background()); !errors.Is(err, inspectErr) {
			t.Fatalf("Reconcile error = %v, want %v", err, inspectErr)
		}
	})
}
