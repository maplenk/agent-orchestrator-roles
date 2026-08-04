package domain

import (
	"fmt"
	"strings"
)

// RoleAuthorizedSwitchTargets returns host-authorized harness/model pairs for
// a role: the role binding primary plus any failover ladder entries. Callers
// must not accept free-form harnesses outside this set.
//
// Empty Model on a target means the provider default for that harness — not a
// wildcard for every client-supplied model.
//
// Same-harness fresh conversation always uses the session's current harness
// (no free-form target). Cross-harness switch requires the target to appear
// here (typically via failover.roles[roleId]).
func RoleAuthorizedSwitchTargets(m RoleMap, roleID string) []FailoverTarget {
	roleID = strings.TrimSpace(roleID)
	if roleID == "" || m.IsZero() {
		return nil
	}
	var out []FailoverTarget
	seen := map[string]struct{}{}
	add := func(t FailoverTarget) {
		if t.Harness == "" || !t.Harness.IsKnown() {
			return
		}
		key := string(t.Harness) + "\x00" + strings.TrimSpace(t.Model)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	if b, ok := m.Roles[roleID]; ok {
		add(FailoverTarget{Harness: b.Harness, Model: b.Model})
	}
	for _, t := range m.Failover.Roles[roleID] {
		add(t)
	}
	return out
}

// SwitchTargetAuthorized reports whether the exact (harness, model) pair is
// host-authorized for the role.
//
// Semantics (empty model = provider default on both sides):
//   - Requested model "" matches only a configured entry with model ""
//   - Requested non-empty model matches only an identical configured model
//   - Configured empty model is never a wildcard for arbitrary client models
func SwitchTargetAuthorized(m RoleMap, roleID string, harness AgentHarness, model string) bool {
	_, err := ResolveAuthorizedSwitchModel(m, roleID, harness, model)
	return err == nil
}

// Switch model resolution errors (service maps these to apierr codes).
var (
	// ErrSwitchTargetUnauthorized means no matching (harness, model) pair.
	ErrSwitchTargetUnauthorized = fmt.Errorf("switch target not authorized by role map")
	// ErrSwitchTargetModelRequired means multiple models are authorized for the
	// harness and the client omitted model (ambiguous default).
	ErrSwitchTargetModelRequired = fmt.Errorf("switch target model required (ambiguous)")
)

// ResolveAuthorizedSwitchModel picks the exact model for a cross-harness switch.
//
// When requestedModel is non-empty, it must match an identical configured model
// for harness (empty configured model does not match non-empty requests).
//
// When requestedModel is empty (provider default):
//   - exactly one authorized entry for harness → use that entry's model
//     (empty means provider default; non-empty means that fixed model)
//   - multiple entries → ErrSwitchTargetModelRequired
//   - zero entries → ErrSwitchTargetUnauthorized
func ResolveAuthorizedSwitchModel(m RoleMap, roleID string, harness AgentHarness, requestedModel string) (string, error) {
	harness = AgentHarness(strings.TrimSpace(string(harness)))
	requestedModel = strings.TrimSpace(requestedModel)
	if harness == "" {
		return "", ErrSwitchTargetUnauthorized
	}
	var matches []FailoverTarget
	for _, t := range RoleAuthorizedSwitchTargets(m, roleID) {
		if t.Harness != harness {
			continue
		}
		matches = append(matches, t)
	}
	if len(matches) == 0 {
		return "", ErrSwitchTargetUnauthorized
	}

	if requestedModel != "" {
		for _, t := range matches {
			if strings.TrimSpace(t.Model) == requestedModel {
				return requestedModel, nil
			}
		}
		return "", ErrSwitchTargetUnauthorized
	}

	// Omitted model: require a unique authorized entry for this harness.
	if len(matches) > 1 {
		return "", ErrSwitchTargetModelRequired
	}
	return strings.TrimSpace(matches[0].Model), nil
}
