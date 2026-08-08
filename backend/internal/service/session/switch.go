package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

const (
	// SwitchPreviewReasonNoRolePin means the orchestrator has no durable role.
	SwitchPreviewReasonNoRolePin = "no_role_pin"
	// SwitchPreviewReasonNoRoleMap means the project has no routing map.
	SwitchPreviewReasonNoRoleMap = "no_role_map"
	// SwitchPreviewReasonRoleAbsent means the pinned role left the routing map.
	SwitchPreviewReasonRoleAbsent = "role_not_in_map"
	// SwitchPreviewReasonNoTarget means no cross-harness target is selectable.
	SwitchPreviewReasonNoTarget = "no_target"
	// SwitchPreviewReasonInProgress means a durable switch fence is held.
	SwitchPreviewReasonInProgress = "in_progress"
	// SwitchPreviewReasonPaused means pause forbids lifecycle relaunches.
	SwitchPreviewReasonPaused = "paused"
	// SwitchPreviewReasonTerminated means the orchestrator is no longer active.
	SwitchPreviewReasonTerminated = "terminated"
	// SwitchPreviewReasonUnavailable means target resolution could not complete,
	// including when the committed session mode cannot enter the switch saga.
	SwitchPreviewReasonUnavailable = "unavailable"
)

// SwitchPreview is the read-time answer to "where may this orchestrator
// switch?". Targets are exact role-map harness/model pairs. They are advisory
// to the desktop only: SwitchWorker re-authorizes every submitted pair, and
// SwitchOrchestrator re-authorizes once more under the project ownership gate.
type SwitchPreview struct {
	Available bool
	RoleID    string
	Current   domain.FailoverTarget
	Targets   []domain.FailoverTarget
	Pending   *domain.SwitchPending
	Reason    string
}

// switchCommander is the subset of the session manager used by switch/fresh.
// Kept separate so commander stays focused for existing fakes; SwitchWorker
// type-asserts when available.
type switchCommander interface {
	SwitchWorker(ctx context.Context, req sessionmanager.SwitchRequest) (sessionmanager.SwitchResult, error)
	// SwitchOrchestrator takes the project ownership gate before the session
	// switch fence and re-authorizes the exact target under that gate.
	SwitchOrchestrator(ctx context.Context, req sessionmanager.SwitchRequest) (sessionmanager.SwitchResult, error)
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
	case domain.KindWorker, domain.KindOrchestrator:
		// Both kinds use the shared role-map authorization below. Their manager
		// entry points differ because orchestrators take the project gate first.
	default:
		// Workers and orchestrators both reach this saga; anything else does not.
		return SwitchWorkerOutcome{}, apierr.Invalid("NOT_A_WORKER", "This session kind does not support switch or fresh conversation", nil)
	}
	if rec.IsTerminated {
		return SwitchWorkerOutcome{}, apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}
	// Preserve the saga's durable-state ordering at the service boundary. A
	// pause is the more specific lifecycle refusal even when the committed mode
	// is Chat, and it must be reported before any interface-specific preflight.
	if rec.Metadata.Pause != nil {
		return SwitchWorkerOutcome{}, toAPIError(sessionmanager.ErrSwitchPaused)
	}
	// Chat controllers cannot enter the switch/fresh saga. Refuse from the
	// durable session record before same-harness dispatch, role-map reads, or a
	// manager call. Relying only on the manager was insufficient: an unpinned
	// Chat orchestrator could fail earlier with ROLE_PIN_REQUIRED, while Fresh
	// could reach a controller-less saga through the no-role path.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		return SwitchWorkerOutcome{}, toAPIError(sessionmanager.ErrSwitchChatUnsupported)
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
	//
	// The role pin and role map are NOT required here, and requiring them was a
	// real gap: those checks exist to authorize a TARGET harness/model against
	// the map, and a same-harness refresh has no target to authorize —
	// ResolveAuthorizedSwitchModel is never reached on this path. Demanding them
	// anyway made the feature unreachable for exactly the sessions that most
	// need it: an orchestrator is auto-bound to orchestratorRole only under
	// strict delegation, so on any project without a role map it has no pin, and
	// 2B-1's whole purpose — refreshing the longest-lived session in the
	// project — returned ROLE_PIN_REQUIRED.
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

	// Same harness → fresh conversation path (no free-form cross-provider model),
	// so the same reasoning applies: nothing to authorize.
	if to == rec.Harness {
		res, err := freshConversationFor(ctx, sc, rec, sem)
		if err != nil {
			return SwitchWorkerOutcome{}, toAPIError(err)
		}
		return s.switchOutcome(ctx, res)
	}

	// Cross-harness from here on, which IS a host-authorized decision: the role
	// map is the only source of legal targets, so a session without a pin, or a
	// project without a map, has no way to authorize one.
	roleID := strings.TrimSpace(rec.Metadata.Role.RoleID)
	if roleID == "" {
		return SwitchWorkerOutcome{}, apierr.Invalid("ROLE_PIN_REQUIRED",
			"Session has no durable role pin; cross-harness switch requires a role-mapped session", nil)
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

	model, err := domain.ResolveAuthorizedSwitchModel(roleMap, roleID, to, requestedModel)
	if err != nil {
		if errors.Is(err, domain.ErrSwitchTargetModelRequired) {
			return SwitchWorkerOutcome{}, apierr.Invalid("TARGET_MODEL_REQUIRED",
				fmt.Sprintf("Multiple models are authorized for harness %q on role %q; pass targetModel explicitly", to, roleID), nil)
		}
		return SwitchWorkerOutcome{}, apierr.Forbidden("SWITCH_TARGET_UNAUTHORIZED",
			fmt.Sprintf("Harness/model %s/%q is not an authorized switch target for role %q (roleMap binding + failover.roles)", to, requestedModel, roleID))
	}

	managerReq := sessionmanager.SwitchRequest{
		SessionID:     req.SessionID,
		TargetHarness: to,
		TargetModel:   model,
		Semantic:      sem,
	}
	var res sessionmanager.SwitchResult
	if rec.Kind == domain.KindOrchestrator {
		res, err = sc.SwitchOrchestrator(ctx, managerReq)
	} else {
		res, err = sc.SwitchWorker(ctx, managerReq)
	}
	if err != nil {
		return SwitchWorkerOutcome{}, toAPIError(err)
	}
	return s.switchOutcome(ctx, res)
}

// SwitchPreview resolves exact cross-harness targets for an orchestrator read
// model. It deliberately does not support workers: their existing failover
// preview owns that UI, and GET /sessions must not add a project read for every
// ordinary worker.
func (s *Service) SwitchPreview(ctx context.Context, rec domain.SessionRecord) (SwitchPreview, error) {
	current := domain.FailoverTarget{
		Harness: rec.Harness,
		Model:   strings.TrimSpace(rec.Metadata.Role.ResolvedModel),
	}
	preview := SwitchPreview{
		RoleID:  strings.TrimSpace(rec.Metadata.Role.RoleID),
		Current: current,
		Pending: rec.Metadata.SwitchPending,
	}
	if rec.Kind != domain.KindOrchestrator {
		return preview, nil
	}
	if rec.IsTerminated {
		preview.Reason = SwitchPreviewReasonTerminated
		return preview, nil
	}
	if preview.Pending != nil {
		preview.Reason = SwitchPreviewReasonInProgress
		return preview, nil
	}
	if rec.Metadata.Pause != nil {
		preview.Reason = SwitchPreviewReasonPaused
		return preview, nil
	}
	// Chat controllers cannot enter the switch/fresh saga. Keep the preview
	// non-null so the read model truthfully reports that target resolution is
	// unavailable for this orchestrator, but never read the project or advertise
	// a target the direct mutation would reject with SWITCH_CHAT_UNSUPPORTED.
	// Durable pending/paused/terminated states above retain their more specific
	// reason because they also own input fencing and recovery guidance.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		preview.Reason = SwitchPreviewReasonUnavailable
		return preview, nil
	}
	if preview.RoleID == "" {
		preview.Reason = SwitchPreviewReasonNoRolePin
		return preview, nil
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return SwitchPreview{}, fmt.Errorf("switch preview %s: project: %w", rec.ID, err)
	}
	if !ok {
		return SwitchPreview{}, fmt.Errorf("switch preview %s: project %s not found", rec.ID, rec.ProjectID)
	}
	roleMap := project.Config.RoleMap.WithDefaults()
	if roleMap.IsZero() {
		preview.Reason = SwitchPreviewReasonNoRoleMap
		return preview, nil
	}
	if _, ok := roleMap.Roles[preview.RoleID]; !ok {
		preview.Reason = SwitchPreviewReasonRoleAbsent
		return preview, nil
	}
	authorized := domain.RoleAuthorizedSwitchTargets(roleMap, preview.RoleID)
	perHarness := make(map[domain.AgentHarness]int, len(authorized))
	for _, target := range authorized {
		if target.Harness != rec.Harness {
			perHarness[target.Harness]++
		}
	}
	for _, target := range authorized {
		// The current harness is Fresh Conversation, not Switch. The service
		// intentionally ignores a same-harness model override, so do not offer
		// one here as if it were a supported model-switch operation.
		if target.Harness == rec.Harness {
			continue
		}
		target.Model = strings.TrimSpace(target.Model)
		// An empty targetModel on the current wire means "model omitted". When a
		// harness has multiple authorized models, the resolver must answer
		// TARGET_MODEL_REQUIRED and therefore cannot distinguish an explicitly
		// selected provider-default entry. Do not advertise that unusable choice;
		// fixed-model entries remain exact and selectable. A unique default stays
		// available. A future pointer/explicit-default wire can lift this filter.
		if target.Model == "" && perHarness[target.Harness] > 1 {
			continue
		}
		preview.Targets = append(preview.Targets, target)
	}
	if len(preview.Targets) == 0 {
		preview.Reason = SwitchPreviewReasonNoTarget
		return preview, nil
	}
	preview.Available = true
	return preview, nil
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
		// The saga has already committed target_ack, promoted the target and
		// cleared its pending fence. Optional PR facts cannot be allowed to turn
		// that durable success into a 500: retrying a cross-harness request after
		// promotion is interpreted as same-harness Fresh and would launch another
		// generation. Return the authoritative session record with an empty PR
		// projection and let the next ordinary read hydrate it.
		slog.Warn("switch response PR facts unavailable; returning committed session without PR facts",
			"sessionID", res.Session.ID, "generationID", res.GenerationID, "error", err)
		sess = s.sessionFromRecord(res.Session, nil)
	}
	return SwitchWorkerOutcome{
		Session:      sess,
		GenerationID: res.GenerationID,
		Kind:         res.Kind,
	}, nil
}
