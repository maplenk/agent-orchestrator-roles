package sessionmanager

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSwitchOperationErrorIsTransientOnly(t *testing.T) {
	if errors.Is(ErrSwitchOperationInProgress, ErrSwitchRecoveryRequired) {
		t.Fatal("transient switch fence classified as durable recovery ownership")
	}
	if errors.Is(ErrSwitchOperationInProgress, domain.ErrAgentSwitchInProgress) {
		t.Fatal("transient switch fence classified as a durable agent-switch saga")
	}
}

func TestLegacySwitchRecoveryErrorCarriesGeneration(t *testing.T) {
	err := legacySwitchRecoveryError("session-1", " generation-1 ")
	if !errors.Is(err, ErrSwitchRecoveryRequired) {
		t.Fatalf("errors.Is(%v, ErrSwitchRecoveryRequired) = false", err)
	}
	if errors.Is(err, ErrSwitchOperationInProgress) || errors.Is(err, domain.ErrAgentSwitchInProgress) {
		t.Fatalf("legacy recovery error has an unrelated classification: %v", err)
	}
	var detail *LegacySwitchRecoveryError
	if !errors.As(err, &detail) || detail.SessionID != "session-1" || detail.GenerationID != "generation-1" {
		t.Fatalf("typed detail = %+v, want session-1/generation-1", detail)
	}
}

func TestAgentSwitchRecoveryErrorPreservesDurableClassifications(t *testing.T) {
	err := agentSwitchRecoveryError(domain.AgentSwitch{
		ID:        "switch-1",
		SessionID: "session-1",
		State:     domain.AgentSwitchStartingTarget,
	})
	if !errors.Is(err, ErrSwitchRecoveryRequired) || !errors.Is(err, domain.ErrAgentSwitchInProgress) {
		t.Fatalf("agent recovery classifications missing: %v", err)
	}
	if errors.Is(err, ErrSwitchOperationInProgress) {
		t.Fatalf("agent recovery error classified as transient: %v", err)
	}
	var detail *AgentSwitchRecoveryError
	if !errors.As(err, &detail) || detail.SwitchID != "switch-1" || detail.State != domain.AgentSwitchStartingTarget {
		t.Fatalf("typed detail = %+v, want switch-1/starting_target", detail)
	}
}
