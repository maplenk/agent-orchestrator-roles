package domain

// PauseGuard is the ownership the caller believes it is pausing. Empty fields
// mean "do not check", which is what an operator pause uses: a human pausing a
// session is not claiming anything about which generation is running.
//
// Structured detection MUST supply both. A limit is observed by a specific
// harness process in a specific generation, and by the time the report lands
// the session may have switched harness or been relaunched — pausing then would
// park a runtime that never hit the limit, on evidence belonging to one that is
// already gone.
type PauseGuard struct {
	ExpectHarness         AgentHarness
	ExpectRuntimeLaunchID string
	// RequireNoSwitchPending refuses while a switch saga owns the session.
	RequireNoSwitchPending bool
}
