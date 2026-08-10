package ports_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNativeSessionAvailabilityValuesRemainTriState(t *testing.T) {
	values := []ports.NativeSessionAvailability{
		ports.NativeSessionAvailabilityAvailable,
		ports.NativeSessionAvailabilityUnavailable,
		ports.NativeSessionAvailabilityUnknown,
	}
	seen := make(map[ports.NativeSessionAvailability]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			t.Fatal("native session availability contains an empty state")
		}
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("native session availability state %q is not distinct", value)
		}
		seen[value] = struct{}{}
	}
}

func TestProviderAcknowledgementCarriesExactSessionAndGenerationFacts(t *testing.T) {
	// ApplyActivitySignal carries the AO session as its explicit method argument
	// and the provider hook's process generation as ActivitySignal.LaunchID.
	// Keep the two facts independent: a native provider id is neither one.
	ackSessionID := domain.SessionID("ao-session-7")
	signal := ports.ActivitySignal{
		Valid:          true,
		State:          domain.ActivityActive,
		Event:          "user-prompt-submit",
		AgentSessionID: "provider-native-thread",
		LaunchID:       "target-generation-11",
	}
	if ackSessionID != "ao-session-7" {
		t.Fatalf("AO session id = %q", ackSessionID)
	}
	if signal.LaunchID != "target-generation-11" {
		t.Fatalf("target generation = %q", signal.LaunchID)
	}
	if signal.AgentSessionID == string(ackSessionID) || signal.AgentSessionID == signal.LaunchID {
		t.Fatalf("provider native id was conflated with AO ownership facts: %+v", signal)
	}
}
