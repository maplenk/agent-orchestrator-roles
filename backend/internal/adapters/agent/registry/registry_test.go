package registry

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestGetAgentHooksFootprintIsGitignored enforces a contract every shipped
// (and future) adapter must hold: any file GetAgentHooks writes into a session
// worktree must be covered by a sibling AO-managed self-ignoring .gitignore
// (hookutil.EnsureWorkspaceGitignore). Hook files are untracked, and
// `git worktree remove` (without --force) refuses on any untracked file — an
// uncovered hook file makes every one of that adapter's session workspaces
// permanently undeletable (kill/cleanup can never free them).
func TestGetAgentHooksFootprintIsGitignored(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			ws := t.TempDir()
			if ha.Harness == "autohand" {
				t.Setenv("AUTOHAND_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			}
			cfg := ports.WorkspaceHookConfig{
				SessionID:     "proj-1",
				WorkspacePath: ws,
				DataDir:       t.TempDir(),
			}
			if ha.Harness == "kimi" {
				cfg.Env = map[string]string{"KIMI_CODE_HOME": filepath.Join(cfg.DataDir, "kimi")}
			}
			if err := ha.Agent.GetAgentHooks(context.Background(), cfg); err != nil {
				t.Fatalf("GetAgentHooks: %v", err)
			}
			files := workspaceFiles(t, ws)
			for _, rel := range files {
				gitignorePath := filepath.Join(ws, filepath.Dir(rel), ".gitignore")
				data, err := os.ReadFile(gitignorePath) //nolint:gosec // test-owned temp dir
				if err != nil {
					t.Errorf("hook file %q has no sibling .gitignore (%v); it will keep the session worktree permanently dirty", rel, err)
					continue
				}
				content := string(data)
				if !strings.Contains(content, hookutil.GitignoreSentinel) {
					t.Errorf(".gitignore next to %q is not AO-managed (missing sentinel)", rel)
					continue
				}
				if entry := "/" + filepath.Base(rel); !hasLine(content, entry) {
					t.Errorf(".gitignore next to %q does not list %q", rel, entry)
				}
			}
		})
	}
}

func TestEveryHarnessReportsAuthStatus(t *testing.T) {
	authCheckerExempt := map[string]string{
		"continue": "Continue auth probes require sending a model prompt, so catalog refresh must not run them",
	}
	for _, ha := range Harnessed() {
		if reason, exempt := authCheckerExempt[string(ha.Harness)]; exempt {
			if _, ok := ha.Agent.(ports.AgentAuthChecker); ok {
				t.Errorf("%s implements ports.AgentAuthChecker but is exempt: %s", ha.Harness, reason)
			}
			continue
		}
		if _, ok := ha.Agent.(ports.AgentAuthChecker); !ok {
			t.Errorf("%s does not implement ports.AgentAuthChecker", ha.Harness)
		}
	}
}

func TestHarnessedExcludesFakeHarness(t *testing.T) {
	for _, ha := range Harnessed() {
		if ha.Harness == domain.HarnessFake {
			t.Fatal("fake harness must not be returned as a shipped selectable agent")
		}
	}
}

func TestEveryProductionHarnessReportsModelOrModeConfig(t *testing.T) {
	for _, ha := range Harnessed() {
		t.Run(string(ha.Harness), func(t *testing.T) {
			spec, err := ha.Agent.GetConfigSpec(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range spec.Fields {
				if field.Key == "model" || field.Key == "mode" {
					return
				}
			}
			t.Fatalf("%s exposes neither model nor mode configuration: %#v", ha.Harness, spec.Fields)
		})
	}
}

// workspaceFiles returns every regular file under root, relative to root.
func workspaceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk workspace: %v", err)
	}
	return files
}

func hasLine(content, line string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

// Every after-start adapter must be able to say what its own input prompt looks
// like, or AO must refuse to type into it.
//
// This is a registry test rather than a per-adapter one because the hazard is
// what a NEW adapter inherits. AO delivers an after-start prompt by pasting into
// whatever the pane is showing, and a liveness probe only proves a process
// exists — not that its screen is an input prompt. Three live sessions died when
// Grok's repository-trust screen was accepted as readiness and the pasted brief's
// "n" answered "No, quit".
//
// An adapter that offers no readiness evidence is not a bug here: the manager
// refuses its delivery (waitForPromptReadiness returns ErrPromptNotReady). This
// test exists so that refusal is a DECISION each adapter's author makes
// knowingly, rather than something discovered when a worker silently dies.
func TestAfterStartAdaptersDeclareReadinessEvidence(t *testing.T) {
	// Harnesses whose prompted spawns are knowingly refused for want of
	// readiness evidence. Removing a name means the adapter now supplies
	// hints; adding one means accepting that its prompted spawns fail.
	refusedForNoEvidence := map[domain.AgentHarness]string{
		domain.HarnessAider: "no readiness hints; prompted spawns are refused rather than pasted blind",
		domain.HarnessGoose: "no readiness hints; prompted spawns are refused rather than pasted blind",
	}

	ctx := context.Background()
	for _, adapter := range Constructors() {
		// Adapter is the minimal registry contract; the prompt-delivery and
		// readiness questions live on ports.Agent, which every shipped adapter
		// also satisfies.
		agent, ok := adapter.(ports.Agent)
		if !ok {
			t.Fatalf("%s does not satisfy ports.Agent", adapter.Manifest().ID)
		}
		harness := domain.AgentHarness(adapter.Manifest().ID)
		t.Run(string(harness), func(t *testing.T) {
			strategy, err := agent.GetPromptDeliveryStrategy(ctx, ports.LaunchConfig{
				SessionID: "probe", WorkspacePath: t.TempDir(), Prompt: "task",
			})
			if err != nil {
				// Fatal, not Skip. An adapter AO cannot classify is an adapter
				// missing from the inventory this test exists to keep — and a
				// skip is invisible in a green run, so the next harness could
				// leave by the same door the last one came in through.
				t.Fatalf("delivery strategy could not be discovered, so this adapter "+
					"cannot be classified as safe or refused: %v", err)
			}
			if strategy != ports.PromptDeliveryAfterStart {
				if _, listed := refusedForNoEvidence[harness]; listed {
					t.Fatalf("listed as refused-for-no-evidence but delivers %q; the list is stale", strategy)
				}
				return
			}

			provider, hasHints := agent.(ports.AgentPromptReadinessProvider)
			var patterns int
			if hasHints {
				hints, hintErr := provider.PromptReadinessHints(ctx, ports.LaunchConfig{
					SessionID: "probe", WorkspacePath: t.TempDir(),
				})
				if hintErr != nil {
					t.Fatalf("PromptReadinessHints: %v", hintErr)
				}
				if hints.Timeout > 0 {
					patterns = len(hints.Patterns)
				}
			}

			reason, refused := refusedForNoEvidence[harness]
			switch {
			case patterns > 0 && refused:
				t.Fatalf("supplies %d readiness pattern(s) but is still listed as refused (%q) — "+
					"drop it from the list so its prompted spawns work", patterns, reason)
			case patterns == 0 && !refused:
				t.Fatalf("delivers its prompt after start but offers no readiness evidence, so AO " +
					"would refuse every prompted spawn. Add verified hints, switch to in-command " +
					"delivery, or add it to refusedForNoEvidence with the reason")
			}
		})
	}
}
