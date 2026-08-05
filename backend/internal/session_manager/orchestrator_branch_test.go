package sessionmanager

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestDefaultSpawnBranch_OrchestratorIsCanonicalInEveryProjectKind pins the fix
// for the workspace-project orchestrator branch defect.
//
// An orchestrator worktree is canonical per project
// (<managedRoot>/<projectID>/orchestrator/<prefix>-orchestrator) in every
// project kind, including workspace projects — CreateWorkspaceProject passes
// Kind straight to managedPath. Before this fix, DefaultSpawnBranch ignored
// Kind for workspace projects and handed the orchestrator a per-session branch,
// so verifyOrchestratorReplacement — which asserts DefaultOrchestratorBranch —
// rejected every workspace-project orchestrator *after* it was already live.
func TestDefaultSpawnBranch_OrchestratorIsCanonicalInEveryProjectKind(t *testing.T) {
	const (
		prefix  = "myproj"
		dataDir = "/tmp/ao-test-data"
	)
	want := DefaultOrchestratorBranch(prefix, dataDir)
	if want == "" {
		t.Fatal("canonical orchestrator branch must not be empty")
	}

	for _, projectKind := range []domain.ProjectKind{
		domain.ProjectKindWorkspace,
		domain.ProjectKindSingleRepo,
	} {
		got := DefaultSpawnBranch("sess-abc", domain.KindOrchestrator, prefix, projectKind, dataDir)
		if got != want {
			t.Errorf("projectKind %q: orchestrator branch = %q, want canonical %q",
				projectKind, got, want)
		}
	}
}

// TestDefaultSpawnBranch_WorkerKeepsPerSessionBranch guards against
// over-correcting: only orchestrators are project-canonical. Workers must keep
// a unique per-session branch, because gitworktree cannot add two worktrees on
// the same branch.
func TestDefaultSpawnBranch_WorkerKeepsPerSessionBranch(t *testing.T) {
	const (
		prefix  = "myproj"
		dataDir = "/tmp/ao-test-data"
	)
	orchestrator := DefaultOrchestratorBranch(prefix, dataDir)

	for _, projectKind := range []domain.ProjectKind{
		domain.ProjectKindWorkspace,
		domain.ProjectKindSingleRepo,
	} {
		a := DefaultSpawnBranch("sess-a", domain.KindWorker, prefix, projectKind, dataDir)
		b := DefaultSpawnBranch("sess-b", domain.KindWorker, prefix, projectKind, dataDir)
		if a == b {
			t.Errorf("projectKind %q: workers must not share a branch (both %q)", projectKind, a)
		}
		if a == orchestrator {
			t.Errorf("projectKind %q: worker branch %q collides with the canonical orchestrator branch",
				projectKind, a)
		}
	}
}

// TestDefaultSpawnBranch_ScratchStaysUnbranched keeps the scratch short-circuit
// ahead of the orchestrator rule.
func TestDefaultSpawnBranch_ScratchStaysUnbranched(t *testing.T) {
	for _, kind := range []domain.SessionKind{domain.KindOrchestrator, domain.KindWorker} {
		if got := DefaultSpawnBranch("sess-abc", kind, "myproj", domain.ProjectKindScratch, "/tmp/ao"); got != "" {
			t.Errorf("kind %q: scratch project branch = %q, want empty", kind, got)
		}
	}
}
