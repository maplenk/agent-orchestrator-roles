package handoff

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCompile_ObservedOverridesSemantic(t *testing.T) {
	out := Compile(CompileInput{
		Semantic: domain.SemanticHandoffV1{
			Objective:        "Ship feature X",
			LatestUserIntent: "finish tests",
			OpenItems:        []string{"wire API"},
			SourceGeneration: "src-gen-1",
			// Agent may claim wrong branch; compiler still prints Observed as host truth.
		},
		Observed: domain.ObservedWorkspaceV1{
			Branch:     "ao/mer-1/root",
			Head:       "abc123",
			Porcelain:  " M main.go",
			ObservedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
			VerifiedResults: []domain.VerifiedResult{
				{Command: "go test ./...", ExitCode: 0, Summary: "ok"},
			},
		},
		RoleID:           "implementor",
		TargetGeneration: "tgt-gen-2",
		FromHarness:      domain.HarnessClaudeCode,
		ToHarness:        domain.HarnessCodex,
	})
	if out.RoleID != "implementor" {
		t.Fatalf("role = %q", out.RoleID)
	}
	if out.SourceGeneration != "src-gen-1" || out.TargetGeneration != "tgt-gen-2" {
		t.Fatalf("generations = %q → %q", out.SourceGeneration, out.TargetGeneration)
	}
	for _, want := range []string{
		"provider switch",
		"claude-code",
		"codex",
		"ao/mer-1/root",
		"abc123",
		"go test ./...",
		"Ship feature X",
		"Observed",
		"authoritative",
	} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("compiled text missing %q:\n%s", want, out.Text)
		}
	}
}

func TestCompile_FreshConversationSameHarness(t *testing.T) {
	out := Compile(CompileInput{
		Semantic:    domain.SemanticHandoffV1{Objective: "continue"},
		Observed:    domain.ObservedWorkspaceV1{Branch: "main", Head: "deadbeef"},
		SameHarness: true,
		FromHarness: domain.HarnessCodex,
		ToHarness:   domain.HarnessCodex,
		RoleID:      "implementor",
	})
	if !strings.Contains(out.Text, "fresh conversation") {
		t.Fatalf("want fresh conversation wording:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "provider switch") {
		t.Fatalf("same-harness must not say provider switch:\n%s", out.Text)
	}
}

func TestCompile_UnverifiedTestsNotListedAsVerified(t *testing.T) {
	out := Compile(CompileInput{
		Semantic: domain.SemanticHandoffV1{
			ClaimedDecisions: []string{"tests pass (agent claim)"},
		},
		Observed: domain.ObservedWorkspaceV1{
			Head: "x",
		},
		FromHarness: domain.HarnessClaudeCode,
		ToHarness:   domain.HarnessCodex,
	})
	if !strings.Contains(out.Text, "none — re-run tests") {
		t.Fatalf("expected empty verified results guidance:\n%s", out.Text)
	}
	if strings.Contains(out.Text, "Verified results (AO-captured only):\n  - tests pass") {
		t.Fatal("agent claim must not appear as verified result")
	}
}
