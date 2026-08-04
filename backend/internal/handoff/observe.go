package handoff

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// GitRunner runs a git command in a worktree. Tests inject fakes.
type GitRunner func(ctx context.Context, worktree string, args ...string) (stdout string, err error)

// DefaultGitRunner executes real git in worktree.
func DefaultGitRunner(ctx context.Context, worktree string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = worktree
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// ObserveInput is the host path for building ObservedWorkspaceV1.
type ObserveInput struct {
	Worktree     string
	GenerationID string
	EventCursor  string
	// Runner defaults to DefaultGitRunner when nil.
	Runner GitRunner
	// Now defaults to time.Now when zero.
	Now time.Time
}

// ObserveWorkspace collects deterministic git facts from a worktree.
// Failures on individual git commands leave fields empty rather than failing
// the whole observation (switch can still proceed with partial facts).
func ObserveWorkspace(ctx context.Context, in ObserveInput) domain.ObservedWorkspaceV1 {
	run := in.Runner
	if run == nil {
		run = DefaultGitRunner
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := domain.ObservedWorkspaceV1{
		SchemaVersion: domain.ObservedWorkspaceSchemaVersion,
		Worktree:      in.Worktree,
		GenerationID:  in.GenerationID,
		EventCursor:   in.EventCursor,
		ObservedAt:    now,
		SHAs:          map[string]string{},
	}
	if strings.TrimSpace(in.Worktree) == "" {
		return out
	}
	if branch, err := run(ctx, in.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		out.Branch = strings.TrimSpace(branch)
	}
	if head, err := run(ctx, in.Worktree, "rev-parse", "HEAD"); err == nil {
		out.Head = strings.TrimSpace(head)
		if out.Head != "" {
			out.SHAs["HEAD"] = out.Head
		}
	}
	if porc, err := run(ctx, in.Worktree, "status", "--porcelain"); err == nil {
		out.Porcelain = strings.TrimRight(porc, "\n")
	}
	return out
}
