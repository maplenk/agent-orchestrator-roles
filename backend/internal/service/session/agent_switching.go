package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type agentSwitchRecoverer interface {
	RecoverAgentSwitch(context.Context, domain.SessionID, domain.AgentSwitchID) (domain.AgentSwitch, error)
}

// SwitchAgentInput is the controller-facing command for replacing the active
// provider while retaining the logical AO session.
type SwitchAgentInput struct {
	TargetHarness  domain.AgentHarness
	TargetModel    string
	Note           string
	IdempotencyKey string
}

// SwitchAgent starts or resumes a durable agent-switch saga for a session.
func (s *Service) SwitchAgent(ctx context.Context, id domain.SessionID, in SwitchAgentInput) (domain.AgentSwitch, error) {
	if err := s.authorizeAgentSwitch(ctx, id, in.TargetHarness); err != nil {
		return domain.AgentSwitch{}, err
	}
	switchRecord, err := s.manager.SwitchAgent(ctx, id, sessionmanager.SwitchAgentConfig{
		TargetHarness:  in.TargetHarness,
		Note:           in.Note,
		IdempotencyKey: in.IdempotencyKey,
	})
	return switchRecord, toAPIError(err)
}

type authorizedAgentSwitchIntent struct {
	TargetHarness              domain.AgentHarness
	TargetModel                string
	ExpectedSourceGenerationID domain.AgentGenerationID
	RoleSnapshot               domain.SessionRoleBinding
}

type agentSwitchIdempotencyReader interface {
	GetAgentSwitchByIdempotencyKey(context.Context, domain.SessionID, string) (domain.AgentSwitch, bool, error)
}

// authorizeAgentSwitch is the fork-policy adapter in front of the canonical
// durable engine. The engine owns runtime and saga safety, but it must never be
// asked to interpret a caller-selected harness as authorization. Only a worker
// with a durable role pin may enter it, and the target must resolve to one
// unique host-configured harness/model pair before the manager can mutate
// anything.
//
// TargetModel is not a free-form override: when supplied it must match an exact
// role-map entry. When omitted, the harness must resolve to one unique model.
func (s *Service) authorizeAgentSwitch(ctx context.Context, id domain.SessionID, in SwitchAgentInput) (authorizedAgentSwitchIntent, error) {
	if id == "" {
		return authorizedAgentSwitchIntent{}, apierr.Invalid("SESSION_ID_REQUIRED", "Session id is required", nil)
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return authorizedAgentSwitchIntent{}, fmt.Errorf("switch agent %s: read session: %w", id, err)
	}
	if !ok {
		return authorizedAgentSwitchIntent{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.Kind != domain.KindWorker {
		return authorizedAgentSwitchIntent{}, toAPIError(sessionmanager.ErrUnsupportedSwitchKind)
	}

	target := domain.AgentHarness(strings.TrimSpace(string(in.TargetHarness)))
	requestedModel := strings.TrimSpace(in.TargetModel)
	intent := authorizedAgentSwitchIntent{
		TargetHarness:              target,
		TargetModel:                requestedModel,
		ExpectedSourceGenerationID: domain.AgentGenerationID(strings.TrimSpace(rec.Metadata.RuntimeLaunchID)),
	}
	// An exact idempotent retry adopts the already-authorized immutable intent.
	// It must not be denied because the role map, pause, or source generation
	// changed after the saga became durable. The manager compares the caller's
	// exact target/model/note fingerprint before returning the existing saga.
	if key := strings.TrimSpace(in.IdempotencyKey); key != "" {
		if reader, ok := s.store.(agentSwitchIdempotencyReader); ok {
			existing, found, readErr := reader.GetAgentSwitchByIdempotencyKey(ctx, id, key)
			if readErr != nil {
				return authorizedAgentSwitchIntent{}, fmt.Errorf("switch agent %s: idempotency lookup: %w", id, readErr)
			}
			if found {
				intent.ExpectedSourceGenerationID = existing.SourceGenerationID
				intent.RoleSnapshot = existing.RoleSnapshot
				return intent, nil
			}
		}
	}
	if rec.IsTerminated {
		return authorizedAgentSwitchIntent{}, toAPIError(sessionmanager.ErrTerminated)
	}
	if rec.Metadata.Pause != nil {
		return authorizedAgentSwitchIntent{}, toAPIError(sessionmanager.ErrSwitchPaused)
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return authorizedAgentSwitchIntent{}, toAPIError(sessionmanager.ErrSwitchChatUnsupported)
	}

	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	if roleID == "" {
		return authorizedAgentSwitchIntent{}, apierr.Invalid("ROLE_PIN_REQUIRED",
			"Session has no durable role pin and cannot authorize an agent switch", nil)
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return authorizedAgentSwitchIntent{}, fmt.Errorf("switch agent %s: project: %w", id, err)
	}
	if !ok {
		return authorizedAgentSwitchIntent{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if roleMap.IsZero() {
		return authorizedAgentSwitchIntent{}, apierr.Invalid("ROLE_MAP_REQUIRED",
			"Project has no role map; host-authorized switch targets are unavailable", nil)
	}
	roleBinding, ok := roleMap.Roles[roleID]
	if !ok {
		return authorizedAgentSwitchIntent{}, apierr.Invalid("ROLE_NOT_IN_MAP",
			fmt.Sprintf("Role %q is not present in the project role map", roleID), nil)
	}
	model, err := domain.ResolveAuthorizedSwitchModel(roleMap, roleID, target, requestedModel)
	if err != nil {
		if errors.Is(err, domain.ErrSwitchTargetModelRequired) {
			return authorizedAgentSwitchIntent{}, apierr.Invalid("TARGET_MODEL_REQUIRED",
				fmt.Sprintf("Multiple models are authorized for harness %q on role %q", target, roleID), nil)
		}
		return authorizedAgentSwitchIntent{}, apierr.Forbidden("SWITCH_TARGET_UNAUTHORIZED",
			fmt.Sprintf("Harness %q is not an authorized switch target for role %q", target, roleID))
	}
	roleMapSHA, err := roleMap.SHA256()
	if err != nil {
		return authorizedAgentSwitchIntent{}, fmt.Errorf("switch agent %s: snapshot role map: %w", id, err)
	}
	snapshot := rec.Metadata.Role
	snapshot.RoleMapSchemaVersion = roleMap.SchemaVersion
	snapshot.RoleMapSHA256 = roleMapSHA
	snapshot.ResolvedHarness = target
	snapshot.ResolvedModel = model
	snapshot.ResolvedPermissions = roleBinding.Permissions
	intent.TargetModel = model
	intent.RoleSnapshot = snapshot
	return intent, nil
}

// RecoverAgentSwitch safely reconciles exactly one durable switch. The switch
// identifier fences stale clients from acting on a newer saga for the session.
func (s *Service) RecoverAgentSwitch(
	ctx context.Context,
	id domain.SessionID,
	switchID domain.AgentSwitchID,
) (domain.AgentSwitch, error) {
	recoverer, ok := s.manager.(agentSwitchRecoverer)
	if !ok {
		return domain.AgentSwitch{}, apierr.Internal(
			"AGENT_SWITCH_RECOVERY_UNAVAILABLE",
			"This build does not provide the agent-switch recovery engine",
		)
	}
	switchRecord, err := recoverer.RecoverAgentSwitch(ctx, id, switchID)
	return switchRecord, toAPIError(err)
}

// ListAgentSwitches returns the session's durable switch history, newest first.
func (s *Service) ListAgentSwitches(ctx context.Context, id domain.SessionID) ([]domain.AgentSwitch, error) {
	switches, err := s.manager.ListAgentSwitches(ctx, id)
	return switches, toAPIError(err)
}

// SubmitAgentHandoff records a generation-fenced, source-authored JSON handoff.
func (s *Service) SubmitAgentHandoff(
	ctx context.Context,
	id domain.SessionID,
	switchID domain.AgentSwitchID,
	sourceGenerationID domain.AgentGenerationID,
	handoff json.RawMessage,
) (domain.AgentSwitch, error) {
	switchRecord, err := s.manager.SubmitAgentHandoff(ctx, id, switchID, sourceGenerationID, handoff)
	return switchRecord, toAPIError(err)
}
