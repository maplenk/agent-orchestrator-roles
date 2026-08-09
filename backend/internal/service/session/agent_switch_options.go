package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	AgentSwitchOptionsReasonWorkerRequired  = "worker_session_required"
	AgentSwitchOptionsReasonTerminated      = "terminated"
	AgentSwitchOptionsReasonPaused          = "paused"
	AgentSwitchOptionsReasonInProgress      = "agent_switch_in_progress"
	AgentSwitchOptionsReasonChatUnsupported = "switch_chat_unsupported"
	AgentSwitchOptionsReasonRolePinRequired = "role_pin_required"
	AgentSwitchOptionsReasonRoleMapRequired = "role_map_required"
	AgentSwitchOptionsReasonRoleNotInMap    = "role_not_in_map"
	AgentSwitchOptionsReasonNoTarget        = "no_target"
)

// AgentSwitchOptions is the daemon-authoritative set of exact harness/model
// pairs a worker may submit to the canonical agent-switch engine. Targets on
// the current harness are omitted because that engine is cross-provider only.
type AgentSwitchOptions struct {
	Available bool                    `json:"available"`
	RoleID    string                  `json:"roleId,omitempty"`
	Current   domain.FailoverTarget   `json:"current"`
	Targets   []domain.FailoverTarget `json:"targets"`
	Reason    string                  `json:"reason,omitempty"`
}

type agentSwitchOptionsActiveReader interface {
	GetActiveAgentSwitch(context.Context, domain.SessionID) (domain.AgentSwitch, bool, error)
}

// AgentSwitchOptions returns policy choices only; it never mutates a session
// and mutation-time authorization still re-reads the role map in SwitchAgent.
func (s *Service) AgentSwitchOptions(ctx context.Context, id domain.SessionID) (AgentSwitchOptions, error) {
	if id == "" {
		return AgentSwitchOptions{}, apierr.Invalid("SESSION_ID_REQUIRED", "Session id is required", nil)
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return AgentSwitchOptions{}, fmt.Errorf("agent switch options %s: read session: %w", id, err)
	}
	if !ok {
		return AgentSwitchOptions{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}

	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	options := AgentSwitchOptions{
		RoleID: roleID,
		Current: domain.FailoverTarget{
			Harness: rec.Harness,
			Model:   strings.TrimSpace(rec.Metadata.Role.ResolvedModel),
		},
		Targets: []domain.FailoverTarget{},
	}
	if rec.Kind != domain.KindWorker {
		options.Reason = AgentSwitchOptionsReasonWorkerRequired
		return options, nil
	}
	if rec.IsTerminated {
		options.Reason = AgentSwitchOptionsReasonTerminated
		return options, nil
	}
	if rec.Metadata.Pause != nil {
		options.Reason = AgentSwitchOptionsReasonPaused
		return options, nil
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		options.Reason = AgentSwitchOptionsReasonChatUnsupported
		return options, nil
	}
	if reader, ok := s.store.(agentSwitchOptionsActiveReader); ok {
		if _, active, readErr := reader.GetActiveAgentSwitch(ctx, id); readErr != nil {
			return AgentSwitchOptions{}, fmt.Errorf("agent switch options %s: read active switch: %w", id, readErr)
		} else if active {
			options.Reason = AgentSwitchOptionsReasonInProgress
			return options, nil
		}
	}
	if roleID == "" {
		options.Reason = AgentSwitchOptionsReasonRolePinRequired
		return options, nil
	}

	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return AgentSwitchOptions{}, fmt.Errorf("agent switch options %s: project: %w", id, err)
	}
	if !ok {
		return AgentSwitchOptions{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if roleMap.IsZero() {
		options.Reason = AgentSwitchOptionsReasonRoleMapRequired
		return options, nil
	}
	if _, ok := roleMap.Roles[roleID]; !ok {
		options.Reason = AgentSwitchOptionsReasonRoleNotInMap
		return options, nil
	}
	for _, target := range domain.RoleAuthorizedSwitchTargets(roleMap, roleID) {
		if target.Harness == rec.Harness || !agentSwitchEngineHarnessSupported(target.Harness) {
			continue
		}
		target.Model = strings.TrimSpace(target.Model)
		options.Targets = append(options.Targets, target)
	}
	if len(options.Targets) == 0 {
		options.Reason = AgentSwitchOptionsReasonNoTarget
		return options, nil
	}
	options.Available = true
	return options, nil
}

func agentSwitchEngineHarnessSupported(harness domain.AgentHarness) bool {
	switch harness {
	case domain.HarnessClaudeCode, domain.HarnessCodex:
		return true
	default:
		return false
	}
}
