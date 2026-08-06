package session

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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
