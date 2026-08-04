package handoff

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestObserveWorkspace_FromGitRunner(t *testing.T) {
	run := func(_ context.Context, worktree string, args ...string) (string, error) {
		if worktree != "/ws" {
			t.Fatalf("worktree = %q", worktree)
		}
		key := strings.Join(args, " ")
		switch key {
		case "rev-parse --abbrev-ref HEAD":
			return "ao/mer-1/root\n", nil
		case "rev-parse HEAD":
			return "abc123def\n", nil
		case "status --porcelain":
			return " M main.go\n", nil
		default:
			return "", fmt.Errorf("unexpected git %s", key)
		}
	}
	obs := ObserveWorkspace(context.Background(), ObserveInput{
		Worktree:     "/ws",
		GenerationID: "g1",
		Runner:       run,
		Now:          time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC),
	})
	if obs.Branch != "ao/mer-1/root" || obs.Head != "abc123def" {
		t.Fatalf("branch/head = %q / %q", obs.Branch, obs.Head)
	}
	if obs.Porcelain != " M main.go" {
		t.Fatalf("porcelain = %q", obs.Porcelain)
	}
	if obs.SHAs["HEAD"] != "abc123def" {
		t.Fatalf("shas = %#v", obs.SHAs)
	}
	if obs.GenerationID != "g1" {
		t.Fatalf("gen = %q", obs.GenerationID)
	}
}

func TestObserveWorkspace_PartialOnError(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "rev-parse" && args[1] == "HEAD" {
			return "", fmt.Errorf("not a git repo")
		}
		if args[0] == "rev-parse" {
			return "main", nil
		}
		return "", nil
	}
	obs := ObserveWorkspace(context.Background(), ObserveInput{
		Worktree: "/ws",
		Runner:   run,
	})
	if obs.Branch != "main" {
		t.Fatalf("branch = %q", obs.Branch)
	}
	if obs.Head != "" {
		t.Fatalf("head should be empty on error, got %q", obs.Head)
	}
}
