package sessionmanager

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type adversarialPreflightRuntime struct {
	*fakeRestartRuntime
	preflightCalls []ports.RuntimeConfig
	finalErr       error
}

func (r *adversarialPreflightRuntime) PreflightCreate(cfg ports.RuntimeConfig) error {
	r.preflightCalls = append(r.preflightCalls, cfg)
	if r.finalErr != nil && len(r.preflightCalls) >= 2 {
		return r.finalErr
	}
	return nil
}

// The continuation can be much larger than any one source fact. The complete,
// compacted target command must be finalized and accepted by the runtime's
// exact preflight while the source still owns a live process.
func TestAgentSwitchAdversarialOversizedContextIsFinalizedAndPreflightedBeforeSourceStop(t *testing.T) {
	runtime := &adversarialPreflightRuntime{fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
		outputs: []string{strings.Repeat("terminal-history ", handoffTerminalMaxBytes)},
	}}}
	manager, store, _ := newSwitchTestManager(t, runtime)
	target := manager.agents.(switchTestAgents)[domain.HarnessCodex].(*switchTestAgent)
	rec := store.sessions["proj-1"]
	rec.Metadata.Prompt = strings.Repeat("original-task ", handoffContinuationMaxBytes)
	rec.Metadata.LatestUserPrompt = "LATEST-USER-SENTINEL " + strings.Repeat("user-context ", handoffContinuationMaxBytes)
	rec.Metadata.LatestAssistantUpdate = "LATEST-ASSISTANT-SENTINEL " + strings.Repeat("assistant-context ", handoffContinuationMaxBytes)
	store.sessions[rec.ID] = rec

	var sourceStopViolation string
	runtime.onDestroy = func(call int, handle ports.RuntimeHandle) {
		if call != 0 || handle.ID != rec.Metadata.RuntimeHandleID {
			return
		}
		continuationAtStop := target.launchSystemPrompt
		if continuationAtStop == "" {
			sourceStopViolation = "source stopped before the bounded continuation was finalized"
			return
		}
		if !strings.Contains(continuationAtStop, "LATEST-USER-SENTINEL") ||
			!strings.Contains(continuationAtStop, "LATEST-ASSISTANT-SENTINEL") {
			sourceStopViolation = "source stopped before the dynamic continuation captured the newest source facts"
			return
		}
		if start := strings.LastIndex(continuationAtStop, "<ao-continuation"); start < 0 {
			sourceStopViolation = "final target system prompt lacked the continuation at source stop"
			return
		} else if got := len(continuationAtStop[start:]); got > handoffContinuationMaxBytes {
			sourceStopViolation = "final continuation exceeded its byte ceiling before source stop"
			return
		}
		if len(runtime.preflightCalls) >= 2 {
			return
		}
		sourceStopViolation = "source stopped before the exact final target command passed runtime preflight"
	}

	sw, err := manager.SwitchAgent(context.Background(), rec.ID, SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "oversized-adversarial-context",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sourceStopViolation != "" {
		t.Fatal(sourceStopViolation)
	}
	if sw.State != domain.AgentSwitchCompleted {
		t.Fatalf("switch state = %q, want completed", sw.State)
	}
	if !strings.Contains(target.launchSystemPrompt, "LATEST-USER-SENTINEL") ||
		!strings.Contains(target.launchSystemPrompt, "LATEST-ASSISTANT-SENTINEL") {
		t.Fatalf("bounded continuation lost newest source facts: %q", target.launchSystemPrompt)
	}
	if len(runtime.preflightCalls) < 2 {
		t.Fatalf("runtime preflight calls = %d, want minimum and exact-final checks", len(runtime.preflightCalls))
	}
}

// The minimum launch may fit while the exact command rebuilt with the bounded
// continuation does not. That second failure is still a pre-stop refusal: the
// source runtime and durable source owner must remain intact.
func TestAgentSwitchAdversarialFinalContinuationCapacityFailureLeavesSourceAlive(t *testing.T) {
	runtime := &adversarialPreflightRuntime{
		fakeRestartRuntime: &fakeRestartRuntime{fakeRuntime: &fakeRuntime{
			aliveByHandle: map[string]bool{"proj-1": true},
		}},
		finalErr: ports.ErrRuntimeLaunchCommandTooLong,
	}
	manager, store, _ := newSwitchTestManager(t, runtime)
	rec := store.sessions["proj-1"]
	rec.Metadata.LatestUserPrompt = "CAPACITY-SENTINEL " + strings.Repeat("oversized-context ", handoffContinuationMaxBytes)
	store.sessions[rec.ID] = rec

	sw, err := manager.SwitchAgent(context.Background(), rec.ID, SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "final-capacity-refusal",
	})
	if !errors.Is(err, ports.ErrRuntimeLaunchCommandTooLong) {
		t.Fatalf("switch error = %v, want ErrRuntimeLaunchCommandTooLong", err)
	}
	if len(runtime.preflightCalls) < 2 {
		t.Fatalf("runtime preflight calls = %d, want minimum plus exact final command", len(runtime.preflightCalls))
	}
	if runtime.created != 0 || len(runtime.destroyedIDs) != 0 {
		t.Fatalf("capacity refusal touched runtimes: creates=%d destroys=%v", runtime.created, runtime.destroyedIDs)
	}
	if !runtime.aliveByHandle[rec.Metadata.RuntimeHandleID] {
		t.Fatal("capacity refusal stopped the source runtime")
	}
	if current := store.sessions[rec.ID]; current.Harness != rec.Harness ||
		current.Metadata.RuntimeHandleID != rec.Metadata.RuntimeHandleID ||
		current.Metadata.RuntimeLaunchID != rec.Metadata.RuntimeLaunchID {
		t.Fatalf("capacity refusal changed durable source ownership: %+v", current)
	}
	if sw.State != domain.AgentSwitchFailed || sw.TargetRuntimeHandleID != "" {
		t.Fatalf("capacity refusal retained unsafe switch state: %+v", sw)
	}
}

// Unknown resume evidence permits a separate fresh fallback, but it must not
// be collapsed into "unavailable" by replacing the retained conversation.
// The old provider id, transcript provenance, and generation remain available
// for a later probe that can reach an authoritative answer.
func TestAgentSwitchAdversarialUnknownResumeEvidenceRetainsOriginalConversation(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
	manager, store, _ := newSwitchTestManager(t, runtime)
	target := manager.agents.(switchTestAgents)[domain.HarnessCodex].(*switchTestAgent)
	now := time.Now().UTC().Add(-time.Hour)
	retained := domain.AgentNativeSession{
		ID: "native-unknown-retained", AOSessionID: "proj-1", Harness: domain.HarnessCodex,
		ConfigDir: target.configDir, NativeSessionID: "codex-uncertain-native",
		TranscriptPath: "/provider-owned/uncertain.jsonl", LastGenerationID: "old-target-generation",
		CreatedAt: now, LastUsedAt: now,
	}
	store.native[retained.ID] = retained
	target.available[retained.NativeSessionID] = ports.NativeSessionAvailabilityUnknown

	sw, err := manager.SwitchAgent(context.Background(), retained.AOSessionID, SwitchAgentConfig{
		TargetHarness: domain.HarnessCodex, IdempotencyKey: "unknown-resume-retention",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sw.State != domain.AgentSwitchCompleted || sw.TargetStartMode != domain.AgentSwitchTargetStartFresh {
		t.Fatalf("unknown evidence switch = state %q mode %q, want completed fresh fallback", sw.State, sw.TargetStartMode)
	}
	if got := store.native[retained.ID]; !reflect.DeepEqual(got, retained) {
		t.Fatalf("fresh fallback overwrote uncertain retained conversation:\nbefore: %+v\nafter:  %+v", retained, got)
	}
	if sw.TargetNativeSessionRef == nil || *sw.TargetNativeSessionRef == retained.ID {
		t.Fatalf("fresh fallback reused retained native row: %+v", sw)
	}
	fresh, ok := store.native[*sw.TargetNativeSessionRef]
	if !ok || fresh.NativeSessionID == retained.NativeSessionID || fresh.LastGenerationID != sw.TargetGenerationID {
		t.Fatalf("fresh fallback native row = %+v, want distinct identity at generation %q", fresh, sw.TargetGenerationID)
	}
}

// Recovery of an unacknowledged delivery is conservative and terminal. A
// second boot pass must neither send the ambiguous continuation again nor
// create, restart, or destroy another target process.
func TestAgentSwitchAdversarialAmbiguousDeliveryIsStableAcrossRepeatedRecovery(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
	manager, store, messenger := newSwitchTestManager(t, runtime)
	now := time.Now().UTC()
	targetNative := domain.AgentNativeSession{
		ID: "native-ambiguous-target", AOSessionID: "proj-1", Harness: domain.HarnessCodex,
		NativeSessionID: "codex-ambiguous-target", LastGenerationID: "target-generation",
		CreatedAt: now, LastUsedAt: now,
	}
	store.native[targetNative.ID] = targetNative
	targetRef := targetNative.ID
	sw := domain.AgentSwitch{
		ID: "switch-ambiguous-restart", SessionID: "proj-1", IdempotencyKey: "ambiguous-restart",
		RequestFingerprint: domain.ComputeAgentSwitchRequestFingerprint("proj-1", domain.HarnessCodex, ""),
		FromHarness:        domain.HarnessClaudeCode, TargetHarness: domain.HarnessCodex,
		TargetNativeSessionRef: &targetRef, TargetStartMode: domain.AgentSwitchTargetStartFresh,
		State: domain.AgentSwitchDelivering, AgentHandoffStatus: domain.AgentHandoffUnavailable,
		SourceGenerationID: "source-generation", TargetGenerationID: "target-generation",
		TargetRuntimeHandleID: "target-handle", RequestedAt: now, UpdatedAt: now,
	}
	store.switches[sw.ID] = sw
	rec := store.sessions[sw.SessionID]
	rec.Harness = domain.HarnessCodex
	rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}
	rec.Metadata.RuntimeHandleID = sw.TargetRuntimeHandleID
	rec.Metadata.RuntimeLaunchID = string(sw.TargetGenerationID)
	rec.Metadata.AgentSessionID = targetNative.NativeSessionID
	store.sessions[rec.ID] = rec
	runtime.aliveByHandle[sw.TargetRuntimeHandleID] = true

	if err := manager.ReconcileAgentSwitches(context.Background()); err != nil {
		t.Fatal(err)
	}
	settled := store.switches[sw.ID]
	if settled.State != domain.AgentSwitchFailed || settled.ErrorCode != domain.AgentSwitchErrorDeliveryUnconfirmed {
		t.Fatalf("first recovery = state %q code %q, want failed/delivery_unconfirmed", settled.State, settled.ErrorCode)
	}
	if err := manager.ReconcileAgentSwitches(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.switches[sw.ID]; !reflect.DeepEqual(got, settled) {
		t.Fatalf("second recovery mutated settled ownership:\nfirst:  %+v\nsecond: %+v", settled, got)
	}
	if len(messenger.msgs) != 0 {
		t.Fatalf("ambiguous continuation was redelivered: %#v", messenger.msgs)
	}
	if runtime.created != 0 || runtime.restarted != 0 || len(runtime.destroyedIDs) != 0 {
		t.Fatalf("repeated recovery changed target runtime: creates=%d restarts=%d destroys=%v",
			runtime.created, runtime.restarted, runtime.destroyedIDs)
	}
	if rec := store.sessions[sw.SessionID]; rec.Harness != domain.HarnessCodex ||
		rec.Metadata.RuntimeHandleID != sw.TargetRuntimeHandleID ||
		rec.Metadata.RuntimeLaunchID != string(sw.TargetGenerationID) {
		t.Fatalf("repeated recovery changed target ownership: %+v", rec)
	}
}
