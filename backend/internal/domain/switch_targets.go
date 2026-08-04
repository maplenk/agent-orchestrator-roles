package domain

import "strings"

// RoleAuthorizedSwitchTargets returns host-authorized harness/model pairs for
// a role: the role binding primary plus any failover ladder entries. Callers
// must not accept free-form harnesses outside this set.
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

// SwitchTargetAuthorized reports whether harness (+ optional model) is host-
// authorized for the role. Empty model matches any authorized entry for that
// harness (including entries with empty model = provider default).
func SwitchTargetAuthorized(m RoleMap, roleID string, harness AgentHarness, model string) bool {
	harness = AgentHarness(strings.TrimSpace(string(harness)))
	model = strings.TrimSpace(model)
	if harness == "" {
		return false
	}
	for _, t := range RoleAuthorizedSwitchTargets(m, roleID) {
		if t.Harness != harness {
			continue
		}
		if model == "" {
			return true
		}
		tm := strings.TrimSpace(t.Model)
		if tm == "" || tm == model {
			return true
		}
	}
	return false
}
