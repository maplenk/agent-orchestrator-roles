package session

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	roleslib "github.com/aoagents/agent-orchestrator/backend/internal/roles"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// Role resolution emits five typed sentinels. Unmapped, every one became an
// opaque 500 — which is how a clean install with no shipped role templates
// reported a configuration problem as a daemon bug. These are all conditions
// the caller can fix, so each needs a stable, actionable 4xx.
//
// The wrapping matters as much as the mapping: mapRoleError returns
// `spawn: %w`, the manager wraps again, and the service sees the outermost
// error. Testing bare sentinels would pass while the real path still 500s.
func TestToAPIError_RoleFamily(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"role required", sessionmanager.ErrRoleRequired, "ROLE_REQUIRED"},
		{"role unknown", sessionmanager.ErrRoleUnknown, "ROLE_UNKNOWN"},
		{"harness override forbidden", sessionmanager.ErrHarnessOverrideForbidden, "HARNESS_OVERRIDE_FORBIDDEN"},
		{"model override forbidden", sessionmanager.ErrModelOverrideForbidden, "MODEL_OVERRIDE_FORBIDDEN"},
		{"template unavailable", sessionmanager.ErrRolePromptRequired, "ROLE_TEMPLATE_UNAVAILABLE"},
		{"read-only unsupported", sessionmanager.ErrReadOnlyUnsupported, "READ_ONLY_UNSUPPORTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Exactly how it arrives in production: wrapped by mapRoleError and
			// then by the caller.
			wrapped := fmt.Errorf("spawn mer-1: %w", fmt.Errorf("spawn: %w", tc.err))

			var apiErr *apierr.Error
			if !errors.As(toAPIError(wrapped), &apiErr) {
				t.Fatalf("%v did not map to an apierr: a configuration error surfaces as a 500", tc.err)
			}
			if apiErr.Code != tc.code {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.code)
			}
			if apiErr.Kind != apierr.KindInvalid {
				t.Fatalf("kind = %v, want Invalid (4xx): the caller can fix all of these", apiErr.Kind)
			}
			if apiErr.Message == "" {
				t.Error("empty message: a stable code without guidance is not actionable")
			}
		})
	}
}

// TestToAPIError_UnknownErrorStillFiveHundred is the negative control: the
// mapping must not swallow genuine faults into a misleading 4xx.
func TestToAPIError_UnknownErrorStillFiveHundred(t *testing.T) {
	var apiErr *apierr.Error
	if errors.As(toAPIError(errors.New("disk on fire")), &apiErr) {
		t.Fatalf("an unrecognized error was classified as %v/%s; genuine faults must stay 500",
			apiErr.Kind, apiErr.Code)
	}
}

// TestRoleErrorCodesAreDistinct: a shared code would make two different
// configuration mistakes indistinguishable to a client.
func TestRoleErrorCodesAreDistinct(t *testing.T) {
	seen := map[string]error{}
	for _, err := range []error{
		sessionmanager.ErrRoleRequired,
		sessionmanager.ErrRoleUnknown,
		sessionmanager.ErrHarnessOverrideForbidden,
		sessionmanager.ErrRolePromptRequired,
		sessionmanager.ErrReadOnlyUnsupported,
	} {
		var apiErr *apierr.Error
		if !errors.As(toAPIError(fmt.Errorf("spawn: %w", err)), &apiErr) {
			t.Fatalf("%v unmapped", err)
		}
		if prev, dup := seen[apiErr.Code]; dup {
			t.Errorf("code %q shared by %v and %v", apiErr.Code, prev, err)
		}
		seen[apiErr.Code] = err
	}
}

// The path a real template failure takes: roles.Resolve wraps the loader error
// in ErrTemplateUnavailable, mapRoleError re-wraps it as ErrRolePromptRequired,
// and the caller wraps that again. Testing the sentinel alone would not have
// caught the original defect — the sentinel mapping was always fine, it was
// never reached, because the loader error arrived bare.
func TestToAPIError_TemplateUnavailableThroughTheFullWrapping(t *testing.T) {
	fromResolve := fmt.Errorf("%w: role %q template %q: %w",
		roleslib.ErrTemplateUnavailable, "implementor", "implementor", os.ErrNotExist)
	fromManager := fmt.Errorf("spawn: %w: %w", sessionmanager.ErrRolePromptRequired, fromResolve)
	wrapped := fmt.Errorf("spawn mer-1: %w", fromManager)

	var apiErr *apierr.Error
	if !errors.As(toAPIError(wrapped), &apiErr) {
		t.Fatal("a missing role template surfaced as a 500, not an actionable API error")
	}
	if apiErr.Code != "ROLE_TEMPLATE_UNAVAILABLE" {
		t.Fatalf("code = %q, want ROLE_TEMPLATE_UNAVAILABLE", apiErr.Code)
	}
}

// The path a dead chat controller takes on /sessions/{id}/send: chat.Service
// returns the bare sentinel from its controller registry, RelayChatTurn passes
// it through, and sendChat wraps it with the session id. The conversation routes
// have answered this with 409 CHAT_CONTROLLER_NOT_READY since chat shipped;
// this route answered 500 INTERNAL_ERROR for the same fact, because the send
// path never went through writeConversationError.
func TestToAPIError_DeadChatControllerThroughTheFullWrapping(t *testing.T) {
	wrapped := fmt.Errorf("send plain-1: %w", chatsvc.ErrNoController)

	var apiErr *apierr.Error
	if !errors.As(toAPIError(wrapped), &apiErr) {
		t.Fatal("a dead chat controller surfaced as a 500, not the conversation routes' answer")
	}
	if apiErr.Code != "CHAT_CONTROLLER_NOT_READY" {
		t.Fatalf("code = %q, want CHAT_CONTROLLER_NOT_READY", apiErr.Code)
	}
	if apiErr.Kind != apierr.KindConflict {
		t.Fatalf("kind = %v, want Conflict: the session exists and a restart brings the controller back",
			apiErr.Kind)
	}
}

// The typed refusal the composer keys on: it is in CHAT_PREFLIGHT_CODES, so the
// renderer offers the TUI fallback — which keeps the role — instead of dead-ending.
func TestToAPIError_ChatModeRoleForbidden(t *testing.T) {
	wrapped := fmt.Errorf("spawn mer-1: %w",
		fmt.Errorf("spawn: %w", sessionmanager.ErrChatModeReadOnlyUnsupported))
	var apiErr *apierr.Error
	if !errors.As(toAPIError(wrapped), &apiErr) {
		t.Fatal("a read-only role in chat mode surfaced as a 500")
	}
	if apiErr.Code != "SESSION_MODE_ROLE_FORBIDDEN" {
		t.Fatalf("code = %q, want SESSION_MODE_ROLE_FORBIDDEN", apiErr.Code)
	}
}

// Distinct lifecycle fences keep distinct codes. The switch and interface
// fences were one code for a while: upstream's merged
// INTERFACE_TRANSITION_IN_PROGRESS case matched ErrSwitchInProgress and sat
// ABOVE the fork's SWITCH_IN_PROGRESS case, so the first branch answered for
// both and every ordinary switch, fresh-conversation and input-fence conflict
// told the client it was "already switching interfaces". SWITCH_IN_PROGRESS was
// unreachable. Pause has a different remedy again: explicitly resume rather
// than waiting for either saga. A code with no input is indistinguishable from
// a code that works, which is why this pins every direction.
func TestToAPIError_SwitchAndInterfaceFencesKeepSeparateCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"switch saga fence", sessionmanager.ErrSwitchInProgress, "SWITCH_IN_PROGRESS"},
		{"durable pause fence", sessionmanager.ErrSwitchPaused, "SWITCH_PAUSED"},
		{"interface transition fence", sessionmanager.ErrInterfaceTransitionInProgress, "INTERFACE_TRANSITION_IN_PROGRESS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("switch mer-1: %w", tc.err)
			var apiErr *apierr.Error
			if !errors.As(toAPIError(wrapped), &apiErr) {
				t.Fatalf("%v did not map to an apierr", tc.err)
			}
			if apiErr.Code != tc.code {
				t.Fatalf("code = %q, want %q — the two fences clear on different events and need different remedies", apiErr.Code, tc.code)
			}
		})
	}
}

// The path an oversized role-pinned spawn takes: the tmux adapter wraps the
// sentinel with the measured sizes, and the manager wraps that again with the
// session id. Testing the bare sentinel would pass while the real path still
// 500s — which is what it did, because the composed system prompt is unbounded
// (AO caps only the task prompt) and a harness that delivers its prompt in argv
// pushes the whole thing past what the terminal runtime can launch.
func TestToAPIError_LaunchCommandTooLongThroughTheFullWrapping(t *testing.T) {
	fromRuntime := fmt.Errorf("%w: launch command is %d bytes, limit is %d",
		ports.ErrRuntimeLaunchCommandTooLong, 21387, 15360)
	wrapped := fmt.Errorf("spawn mer-1: runtime: %w", fromRuntime)

	var apiErr *apierr.Error
	if !errors.As(toAPIError(wrapped), &apiErr) {
		t.Fatal("an oversized launch command surfaced as a 500, not an actionable API error")
	}
	if apiErr.Code != "LAUNCH_COMMAND_TOO_LONG" {
		t.Fatalf("code = %q, want LAUNCH_COMMAND_TOO_LONG", apiErr.Code)
	}
	if apiErr.Kind != apierr.KindInvalid {
		t.Fatalf("kind = %v, want Invalid (4xx): the caller controls both prompt sizes", apiErr.Kind)
	}
	// Naming the two inputs is the whole remedy; a stable code that does not say
	// what to shorten is no more actionable than the 500 it replaced.
	if !strings.Contains(apiErr.Message, "system prompt") || !strings.Contains(apiErr.Message, "task prompt") {
		t.Errorf("message %q names neither the system prompt nor the task prompt", apiErr.Message)
	}
}

// And the sentinels must stay distinct values: making one an alias of the other
// would restore the shadowing while both cases still appear in the switch.
func TestSwitchAndInterfaceFenceSentinelsAreDistinct(t *testing.T) {
	if errors.Is(sessionmanager.ErrSwitchInProgress, sessionmanager.ErrInterfaceTransitionInProgress) ||
		errors.Is(sessionmanager.ErrInterfaceTransitionInProgress, sessionmanager.ErrSwitchInProgress) {
		t.Fatal("the two fences share an identity; whichever toAPIError case comes first will answer for both")
	}
}

// PROMPT_NOT_READY must not inherit AWAITING_DECISION's remedy.
//
// ErrPromptNotReady used to wrap ErrAwaitingDecision, whose message tells the
// person to answer it in the session terminal. On this path there is no such
// terminal: spawn tears the runtime and workspace down on this error, and
// relaunch parks or terminates the session. Sending someone to look for a pane
// that no longer exists is worse than a generic answer, because they will go
// and look.
func TestToAPIError_PromptNotReadyDoesNotPointAtADestroyedTerminal(t *testing.T) {
	wrapped := fmt.Errorf("spawn mer-1: %w",
		fmt.Errorf("deliver prompt: %w", sessionmanager.ErrPromptNotReady))

	var apiErr *apierr.Error
	if !errors.As(toAPIError(wrapped), &apiErr) {
		t.Fatal("a refused prompt delivery surfaced as a 500")
	}
	if apiErr.Code != "PROMPT_NOT_READY" {
		t.Fatalf("code = %q, want PROMPT_NOT_READY", apiErr.Code)
	}
	// The remedy must describe THIS path: nothing sent, launch stopped.
	for _, want := range []string{"not sent", "stopped"} {
		if !strings.Contains(apiErr.Message, want) {
			t.Errorf("message does not say %q: %s", want, apiErr.Message)
		}
	}
	// And it must not tell them to go to a terminal that was destroyed.
	if strings.Contains(apiErr.Message, "session terminal") {
		t.Errorf("message points at a terminal this path already tore down: %s", apiErr.Message)
	}
	// The distinction has to be real, not just differently worded: the two
	// sentinels must not match each other, or whichever case comes first wins.
	if errors.Is(sessionmanager.ErrPromptNotReady, sessionmanager.ErrAwaitingDecision) {
		t.Fatal("ErrPromptNotReady still matches ErrAwaitingDecision; the AWAITING_DECISION case will claim it")
	}
}
