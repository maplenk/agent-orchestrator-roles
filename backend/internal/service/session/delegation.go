package session

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	delegatedTaskTitleLimit             = 20
	delegatedTaskUntitledName           = "Untitled task"
	delegatedTaskTitleRefinementTimeout = time.Minute
)

// DelegateTaskInput describes a task AO should spawn as a worker session. Brief
// may be empty to open an idle worker that the user can instruct later. Empty
// RequestedAgent means the spawn uses the project's worker-agent default.
type DelegateTaskInput struct {
	ProjectID      domain.ProjectID
	Brief          string
	RequestedAgent domain.AgentHarness
	// RoleID is passed through untouched. Whether it is required, permitted
	// alongside RequestedAgent, or known at all is the role map's decision,
	// resolved in the manager — never here, and never in the client.
	RoleID string
	Model  string
	// RequestedMode is the chat/TUI interface for the session. It is NOT a
	// role field: a role binds harness, model and policy, and the same role
	// may legitimately run in either interface. So {roleId, mode} is a valid
	// pair here, unlike {roleId, agent} or {roleId, model} — the manager
	// resolves the role first and then preflights this mode against the
	// harness and policy it resolved.
	RequestedMode domain.SessionMode
	Attachments   []ports.SpawnAttachment
}

// DelegateTaskOutcome identifies the spawned worker. OrchestratorID remains
// optional for wire compatibility; asynchronous title refinement does not wait
// to resolve the coordinator before returning.
type DelegateTaskOutcome struct {
	OrchestratorID domain.SessionID
	WorkerID       domain.SessionID
}

// DelegateTask spawns the worker directly, matching `ao spawn`, with a
// provisional display name derived from the task brief. AO then best-effort
// refines that title in the background through the project orchestrator,
// resuming or creating the coordinator when necessary.
func (s *Service) DelegateTask(ctx context.Context, in DelegateTaskInput) (DelegateTaskOutcome, error) {
	if _, err := s.requireProject(ctx, in.ProjectID); err != nil {
		return DelegateTaskOutcome{}, err
	}
	if in.RequestedAgent != "" && !in.RequestedAgent.IsKnown() {
		return DelegateTaskOutcome{}, apierr.Invalid("UNKNOWN_HARNESS", "Unknown requested agent", nil)
	}
	if in.RequestedMode != "" && !in.RequestedMode.Valid() {
		return DelegateTaskOutcome{}, apierr.Invalid("INVALID_SESSION_MODE", "mode must be chat or tui", nil)
	}
	prompt := in.Brief
	if strings.TrimSpace(prompt) == "" {
		prompt = ""
	}

	worker, _, _, err := s.manager.Spawn(ctx, ports.SpawnConfig{
		ProjectID:     in.ProjectID,
		Kind:          domain.KindWorker,
		Harness:       in.RequestedAgent,
		RoleID:        in.RoleID,
		Prompt:        prompt,
		DisplayName:   delegatedTaskDisplayName(in.Brief),
		AgentConfig:   ports.AgentConfig{Model: strings.TrimSpace(in.Model)},
		RequestedMode: in.RequestedMode,
		Attachments:   in.Attachments,
	})
	if err != nil {
		return DelegateTaskOutcome{}, toAPIError(err)
	}

	// The worker spawn is the commit point. Coordinator startup and title
	// generation must never hold the new-task response open. A promptless worker
	// stays idle with its provisional title until the user supplies instructions.
	if prompt != "" {
		s.refineDelegatedTaskTitleInBackground(worker.ID, in)
	}
	return DelegateTaskOutcome{WorkerID: worker.ID}, nil
}

func (s *Service) refineDelegatedTaskTitleInBackground(workerID domain.SessionID, in DelegateTaskInput) {
	work := func() {
		base := s.backgroundContext
		if base == nil {
			base = context.Background()
		}
		ctx, cancel := context.WithTimeout(base, delegatedTaskTitleRefinementTimeout)
		defer cancel()

		if err := s.refineDelegatedTaskTitle(ctx, workerID, in); err != nil && s.logger != nil {
			s.logger.Warn("delegated task title refinement failed",
				"projectID", in.ProjectID,
				"workerID", workerID,
				"error", err,
			)
		}
	}
	if s.runBackground != nil {
		s.runBackground(work)
		return
	}
	go work()
}

func (s *Service) refineDelegatedTaskTitle(ctx context.Context, workerID domain.SessionID, in DelegateTaskInput) error {
	orchestrator, ok, err := s.manager.ProjectOrchestrator(ctx, in.ProjectID)
	if err != nil {
		return fmt.Errorf("resolve title orchestrator for project %s: %w", in.ProjectID, err)
	}
	if !ok || orchestrator.Activity.State == domain.ActivityExited {
		// The worker spawn is already committed. A missing or exited orchestrator
		// only means the best-effort title refinement is skipped.
		return nil
	}
	if err := s.manager.WaitForMessageDeliveryReady(ctx, orchestrator.ID); err != nil {
		return fmt.Errorf("wait for title orchestrator %s: %w", orchestrator.ID, err)
	}
	if err := s.manager.Send(ctx, orchestrator.ID, taskTitleDelegationMessage(workerID, in), nil); err != nil {
		return fmt.Errorf("send title request to %s: %w", orchestrator.ID, err)
	}
	return nil
}

func delegatedTaskDisplayName(brief string) string {
	title := strings.Join(strings.Fields(brief), " ")
	if title == "" {
		return delegatedTaskUntitledName
	}
	if utf8.RuneCountInString(title) <= delegatedTaskTitleLimit {
		return title
	}
	return strings.TrimSpace(string([]rune(title)[:delegatedTaskTitleLimit]))
}

func taskTitleDelegationMessage(workerID domain.SessionID, in DelegateTaskInput) string {
	var b strings.Builder
	b.WriteString("AO TASK TITLE UPDATE\n")
	b.WriteString("A worker was already spawned directly with the user's task. Do not spawn another worker or orchestrator, and do not implement the task in this orchestrator session.\n")
	b.WriteString("Choose a concise task title from the brief and run:\n\n")
	b.WriteString("ao session rename ")
	b.WriteString(string(workerID))
	b.WriteString(" \"<title, max 20 chars>\"\n\n")
	b.WriteString("Worker session id: ")
	b.WriteString(string(workerID))
	b.WriteString("\nTask brief:\n")
	b.WriteString(in.Brief)
	if model := strings.TrimSpace(in.Model); model != "" {
		b.WriteString("\nRequested model: ")
		b.WriteString(model)
	}
	return b.String()
}
