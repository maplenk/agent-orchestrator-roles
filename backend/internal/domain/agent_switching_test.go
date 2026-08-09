package domain

import "testing"

func TestAgentSwitchRequestFingerprintCanonicalizesStableIntent(t *testing.T) {
	t.Parallel()

	sessionID := SessionID("session-1")
	want := ComputeAgentSwitchRequestFingerprint(sessionID, HarnessCodex, "continue here")
	for _, note := range []string{"continue here", "  continue here", "continue here\n"} {
		if got := ComputeAgentSwitchRequestFingerprint(sessionID, HarnessCodex, note); got != want {
			t.Fatalf("fingerprint with note %q = %q, want %q", note, got, want)
		}
	}
	if !want.Valid() {
		t.Fatalf("computed fingerprint %q is not valid", want)
	}
	for name, got := range map[string]AgentSwitchRequestFingerprint{
		"session": ComputeAgentSwitchRequestFingerprint("session-2", HarnessCodex, "continue here"),
		"target":  ComputeAgentSwitchRequestFingerprint(sessionID, HarnessClaudeCode, "continue here"),
		"note":    ComputeAgentSwitchRequestFingerprint(sessionID, HarnessCodex, "different intent"),
	} {
		if got == want {
			t.Fatalf("%s change did not change fingerprint %q", name, want)
		}
	}
	for _, invalid := range []AgentSwitchRequestFingerprint{
		"", "v1:short", "v2:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"v1:0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		if invalid.Valid() {
			t.Fatalf("invalid fingerprint %q reported valid", invalid)
		}
	}
}

func TestAgentSwitchRequestFingerprintBindsAuthorizedModel(t *testing.T) {
	providerDefault := ComputeAgentSwitchRequestFingerprint("session-1", HarnessCodex, "continue")
	providerDefaultExplicit := ComputeAuthorizedAgentSwitchRequestFingerprint("session-1", HarnessCodex, "", "continue")
	modelPinned := ComputeAuthorizedAgentSwitchRequestFingerprint("session-1", HarnessCodex, "o3", "continue")
	if providerDefault != providerDefaultExplicit {
		t.Fatalf("empty model changed the legacy fingerprint: %q != %q", providerDefault, providerDefaultExplicit)
	}
	if modelPinned == providerDefault {
		t.Fatal("authorized target model was omitted from the request fingerprint")
	}
}

func TestValidAgentSwitchTransitionRequiresSequentialProgress(t *testing.T) {
	t.Parallel()

	sequence := []AgentSwitchState{
		AgentSwitchPreparingHandoff,
		AgentSwitchStoppingSource,
		AgentSwitchSourceStopped,
		AgentSwitchStartingTarget,
		AgentSwitchTargetReady,
		AgentSwitchDelivering,
		AgentSwitchCompleted,
	}
	for i, state := range sequence {
		if state != AgentSwitchCompleted && !ValidAgentSwitchTransition(state, state) {
			t.Fatalf("nonterminal same-state amendment rejected for %q", state)
		}
		if i+1 < len(sequence) && !ValidAgentSwitchTransition(state, sequence[i+1]) {
			t.Fatalf("sequential transition rejected: %q -> %q", state, sequence[i+1])
		}
		if i+2 < len(sequence) && ValidAgentSwitchTransition(state, sequence[i+2]) {
			t.Fatalf("skipped transition accepted: %q -> %q", state, sequence[i+2])
		}
		if state != AgentSwitchCompleted && !ValidAgentSwitchTransition(state, AgentSwitchFailed) {
			t.Fatalf("failure transition rejected from %q", state)
		}
	}
	for _, terminal := range []AgentSwitchState{AgentSwitchCompleted, AgentSwitchFailed} {
		for _, next := range sequence {
			if ValidAgentSwitchTransition(terminal, next) {
				t.Fatalf("terminal transition accepted: %q -> %q", terminal, next)
			}
		}
	}
}

func TestAgentSwitchRequiresRecoveryUsesExactPersistedTuple(t *testing.T) {
	t.Parallel()

	base := AgentSwitch{
		State:                 AgentSwitchStartingTarget,
		ErrorCode:             AgentSwitchErrorTargetStartUnconfirmed,
		TargetRuntimeHandleID: "",
		TargetGenerationID:    "target-generation",
		TargetStartMode:       AgentSwitchTargetStartFresh,
	}
	if !base.RequiresRecovery() {
		t.Fatal("exact target-start-unconfirmed tuple did not require recovery")
	}
	withHandle := base
	withHandle.TargetRuntimeHandleID = "target-handle"
	if withHandle.RequiresRecovery() {
		t.Fatal("switch with a durable target handle requires recovery")
	}
	wrongState := base
	wrongState.State = AgentSwitchSourceStopped
	if wrongState.RequiresRecovery() {
		t.Fatal("source-stopped switch requires target-start recovery")
	}
	wrongCode := base
	wrongCode.ErrorCode = AgentSwitchErrorDaemonRestartPostStop
	if wrongCode.RequiresRecovery() {
		t.Fatal("different error code requires target-start recovery")
	}
}
