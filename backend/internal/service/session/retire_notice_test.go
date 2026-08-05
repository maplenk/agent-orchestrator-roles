package session

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// TestOrchestratorRetireNotice_DoesNotPromiseANewWorkspace pins the retire
// notice against what replacement actually does.
//
// The orchestrator worktree and branch are canonical per project, so
// RetireForReplacement releases the workspace and the successor re-creates it
// at the same path on the same branch. The notice previously told the outgoing
// orchestrator a fresh one would "take over in a new workspace", which is
// false and would mislead an agent reasoning about its own worktree.
func TestOrchestratorRetireNotice_DoesNotPromiseANewWorkspace(t *testing.T) {
	if strings.Contains(strings.ToLower(orchestratorRetireNotice), "new workspace") {
		t.Fatalf("retire notice claims a new workspace, but the successor reuses the "+
			"canonical orchestrator worktree and branch: %q", orchestratorRetireNotice)
	}
	if !strings.Contains(strings.ToLower(orchestratorRetireNotice), "stop coordinating") {
		t.Fatalf("retire notice must tell the outgoing orchestrator to stop coordinating: %q",
			orchestratorRetireNotice)
	}
}

// TestOrchestratorReplacementReusesCanonicalBranch is the fact the notice
// depends on: the successor's expected branch is derived per project, not per
// session, so it is identical across a retire-and-replace.
func TestOrchestratorReplacementReusesCanonicalBranch(t *testing.T) {
	const (
		prefix  = "myproj"
		dataDir = "/tmp/ao-test-data"
	)
	before := sessionmanager.DefaultSpawnBranch(
		"orch-session-1", domain.KindOrchestrator, prefix, domain.ProjectKindSingleRepo, dataDir)
	after := sessionmanager.DefaultSpawnBranch(
		"orch-session-2", domain.KindOrchestrator, prefix, domain.ProjectKindSingleRepo, dataDir)

	if before != after {
		t.Fatalf("orchestrator branch must be session-independent: %q then %q", before, after)
	}
	if want := sessionmanager.DefaultOrchestratorBranch(prefix, dataDir); before != want {
		t.Fatalf("orchestrator branch = %q, want the branch verifyOrchestratorReplacement asserts (%q)",
			before, want)
	}
}
