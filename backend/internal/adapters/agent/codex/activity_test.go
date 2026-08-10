package codex

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		want   domain.ActivityState
		wantOK bool
	}{
		{"user prompt -> active", "user-prompt-submit", domain.ActivityActive, true},
		{"permission request -> waiting_input", "permission-request", domain.ActivityWaitingInput, true},
		{"stop -> idle", "stop", domain.ActivityIdle, true},
		{"session start -> no signal", "session-start", "", false},
		{"unknown event -> no signal", "frobnicate", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(`{}`))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)",
					tt.event, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestContinuationAcknowledgementHookContract(t *testing.T) {
	p := &Plugin{}
	if !p.EmitsSubmitActivity() {
		t.Fatal("Codex continuation requires submit activity acknowledgement")
	}
	state, ok := DeriveActivityState("user-prompt-submit", nil)
	if !ok || state != domain.ActivityActive {
		t.Fatalf("user-prompt-submit = (%q, %v), want (active, true)", state, ok)
	}
	wantCommand := "ao hooks codex user-prompt-submit"
	for _, spec := range codexManagedHooks {
		if spec.Event == "UserPromptSubmit" && spec.Command == wantCommand {
			return
		}
	}
	t.Fatalf("managed hooks do not contain exact acknowledgement command %q", wantCommand)
}
