package session

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// SwitchWorkerRequest is the controller/CLI-facing worker switch input.
// Target harness must be host-authorized via project role map (binding + failover).
// Free-form harness/model pairs outside that set are rejected before the manager.
type SwitchWorkerRequest struct {
	SessionID     domain.SessionID
	TargetHarness domain.AgentHarness
	TargetModel   string
	// Objective is optional agent-authored handoff intent (untrusted for git/tests).
	Objective string
	// Fresh forces same-harness fresh conversation (ignores TargetHarness).
	Fresh bool
}

// SwitchWorkerOutcome is the API-facing switch/fresh result.
type SwitchWorkerOutcome struct {
	Session      domain.Session             `json:"session"`
	GenerationID string                     `json:"generationId"`
	Kind         domain.LifecycleLedgerKind `json:"kind"`
}

// switchCommander is the subset of the session manager used by switch/fresh.
// Kept separate so commander stays focused for existing fakes; SwitchWorker
// type-asserts when available.
type switchCommander interface {
	SwitchWorker(ctx context.Context, req sessionmanager.SwitchRequest) (sessionmanager.SwitchResult, error)
	FreshConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (sessionmanager.SwitchResult, error)
	// FreshOrchestratorConversation is the orchestrator entry point. It is
	// separate because it must take the project ownership gate BEFORE the
	// switch fence; routing an orchestrator through FreshConversation would
	// acquire those locks in the wrong order.
	FreshOrchestratorConversation(ctx context.Context, sessionID domain.SessionID, semantic domain.SemanticHandoffV1) (sessionmanager.SwitchResult, error)
}

// freshConversationFor dispatches on kind so the caller cannot pick the wrong
// lock order by accident.
func freshConversationFor(ctx context.Context, sc switchCommander, rec domain.SessionRecord, sem domain.SemanticHandoffV1) (sessionmanager.SwitchResult, error) {
	if rec.Kind == domain.KindOrchestrator {
		return sc.FreshOrchestratorConversation(ctx, rec.ID, sem)
	}
	return sc.FreshConversation(ctx, rec.ID, sem)
}

// ErrSwitchNotWired means the process commander does not implement switch (tests).
var ErrSwitchNotWired = errors.New("session: switch not wired on commander")

// SwitchWorker authorizes the target via the project role map, then runs the
// durable switch/fresh saga. Claude/Codex advertise switch_supported after
// Phase 2A promotion; other harnesses still fail closed with SWITCH_NOT_SUPPORTED.
func (s *Service) SwitchWorker(ctx context.Context, req SwitchWorkerRequest) (SwitchWorkerOutcome, error) {
	if req.SessionID == "" {
		return SwitchWorkerOutcome{}, apierr.Invalid("SESSION_ID_REQUIRED", "Session id is required", nil)
	}
	rec, ok, err := s.store.GetSession(ctx, req.SessionID)
	if err != nil {
		return SwitchWorkerOutcome{}, fmt.Errorf("switch %s: %w", req.SessionID, err)
	}
	if !ok {
		return SwitchWorkerOutcome{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	switch rec.Kind {
	case domain.KindWorker:
		// Full switch/fresh matrix.
	case domain.KindOrchestrator:
		// 2B-1: in-place FRESH conversation only. Cross-harness is 2B-3 and is
		// blocked on Claude read-only enforcement — a strict orchestrator must
		// be workspaceWrites:false and only Codex enforces that, so shipping it
		// for non-strict projects alone would create a capability strict
		// projects can never have.
		if !req.Fresh && strings.TrimSpace(string(req.TargetHarness)) != "" &&
			domain.AgentHarness(strings.TrimSpace(string(req.TargetHarness))) != rec.Harness {
			return SwitchWorkerOutcome{}, apierr.Invalid("ORCHESTRATOR_CROSS_HARNESS_UNSUPPORTED",
				"Orchestrators support in-place fresh conversation only; cross-harness switch is not available yet", nil)
		}
	default:
		return SwitchWorkerOutcome{}, apierr.Invalid("NOT_A_WORKER", "Only worker sessions support switch/fresh conversation", nil)
	}
	if rec.IsTerminated {
		return SwitchWorkerOutcome{}, apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}

	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	if roleID == "" {
		return SwitchWorkerOutcome{}, apierr.Invalid("ROLE_PIN_REQUIRED",
			"Session has no durable role pin; switch requires a role-mapped worker", nil)
	}

	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return SwitchWorkerOutcome{}, fmt.Errorf("switch %s: project: %w", req.SessionID, err)
	}
	if !ok {
		return SwitchWorkerOutcome{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if roleMap.IsZero() {
		return SwitchWorkerOutcome{}, apierr.Invalid("ROLE_MAP_REQUIRED",
			"Project has no role map; host-authorized switch targets are unavailable", nil)
	}
	if _, ok := roleMap.Roles[roleID]; !ok {
		return SwitchWorkerOutcome{}, apierr.Invalid("ROLE_NOT_IN_MAP",
			fmt.Sprintf("Role %q is not present in the project role map", roleID), nil)
	}

	sc, ok := s.manager.(switchCommander)
	if !ok {
		return SwitchWorkerOutcome{}, fmt.Errorf("%w", ErrSwitchNotWired)
	}

	sem := domain.SemanticHandoffV1{
		SchemaVersion: domain.SemanticHandoffSchemaVersion,
		Objective:     strings.TrimSpace(req.Objective),
	}

	// Fresh conversation: same harness only — never accepts free-form target.
	if req.Fresh {
		res, err := freshConversationFor(ctx, sc, rec, sem)
		if err != nil {
			return SwitchWorkerOutcome{}, toAPIError(err)
		}
		return s.switchOutcome(ctx, res)
	}

	to := domain.AgentHarness(strings.TrimSpace(string(req.TargetHarness)))
	if to == "" {
		return SwitchWorkerOutcome{}, apierr.Invalid("TARGET_HARNESS_REQUIRED",
			"targetHarness is required for cross-harness switch (or set fresh=true)", nil)
	}
	if !to.IsKnown() {
		return SwitchWorkerOutcome{}, apierr.Invalid("UNKNOWN_HARNESS", fmt.Sprintf("Unknown harness %q", to), nil)
	}
	requestedModel := strings.TrimSpace(req.TargetModel)

	// Same harness → fresh conversation path (no free-form cross-provider model).
	if to == rec.Harness {
		res, err := freshConversationFor(ctx, sc, rec, sem)
		if err != nil {
			return SwitchWorkerOutcome{}, toAPIError(err)
		}
		return s.switchOutcome(ctx, res)
	}

	model, err := domain.ResolveAuthorizedSwitchModel(roleMap, roleID, to, requestedModel)
	if err != nil {
		if errors.Is(err, domain.ErrSwitchTargetModelRequired) {
			return SwitchWorkerOutcome{}, apierr.Invalid("TARGET_MODEL_REQUIRED",
				fmt.Sprintf("Multiple models are authorized for harness %q on role %q; pass targetModel explicitly", to, roleID), nil)
		}
		return SwitchWorkerOutcome{}, apierr.Forbidden("SWITCH_TARGET_UNAUTHORIZED",
			fmt.Sprintf("Harness/model %s/%q is not an authorized switch target for role %q (roleMap binding + failover.roles)", to, requestedModel, roleID))
	}

	res, err := sc.SwitchWorker(ctx, sessionmanager.SwitchRequest{
		SessionID:     req.SessionID,
		TargetHarness: to,
		TargetModel:   model,
		Semantic:      sem,
	})
	if err != nil {
		return SwitchWorkerOutcome{}, toAPIError(err)
	}
	return s.switchOutcome(ctx, res)
}

// FreshConversation is same-harness context refresh (no free-form harness).
func (s *Service) FreshConversation(ctx context.Context, sessionID domain.SessionID, objective string) (SwitchWorkerOutcome, error) {
	return s.SwitchWorker(ctx, SwitchWorkerRequest{
		SessionID: sessionID,
		Objective: objective,
		Fresh:     true,
	})
}

func (s *Service) switchOutcome(ctx context.Context, res sessionmanager.SwitchResult) (SwitchWorkerOutcome, error) {
	sess, err := s.toSession(ctx, res.Session)
	if err != nil {
		return SwitchWorkerOutcome{}, err
	}
	return SwitchWorkerOutcome{
		Session:      sess,
		GenerationID: res.GenerationID,
		Kind:         res.Kind,
	}, nil
}
