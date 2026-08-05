package domain

import "time"

// SemanticHandoffSchemaVersion is the wire version for SemanticHandoffV1.
const SemanticHandoffSchemaVersion = 1

// ObservedWorkspaceSchemaVersion is the wire version for ObservedWorkspaceV1.
const ObservedWorkspaceSchemaVersion = 1

// SemanticHandoffV1 is agent-authored context for a switch or fresh conversation.
// It is untrusted for Git state and test results — the handoff compiler prefers
// ObservedWorkspaceV1 for those facts.
type SemanticHandoffV1 struct {
	SchemaVersion      int      `json:"schemaVersion"`
	Objective          string   `json:"objective,omitempty"`
	OpenItems          []string `json:"openItems,omitempty"`
	ClaimedDecisions   []string `json:"claimedDecisions,omitempty"`
	RejectedApproaches []string `json:"rejectedApproaches,omitempty"`
	Uncertainty        []string `json:"uncertainty,omitempty"`
	LatestUserIntent   string   `json:"latestUserIntent,omitempty"`
	// SourceGeneration is the runtime launch / switch generation id of the source.
	SourceGeneration string `json:"sourceGeneration,omitempty"`
	// NativeSessionID is the source agent native session pointer (if any).
	NativeSessionID string `json:"nativeSessionId,omitempty"`
}

// VerifiedResult is a test or command outcome AO captured itself (cmd + exit + provenance).
// Agent-reported results must NOT use this type; keep them in SemanticHandoff as claims.
type VerifiedResult struct {
	Command    string    `json:"command"`
	ExitCode   int       `json:"exitCode"`
	Summary    string    `json:"summary,omitempty"`
	Provenance string    `json:"provenance,omitempty"` // e.g. "ao-capture", artifact path
	CapturedAt time.Time `json:"capturedAt,omitempty"`
}

// ObservedWorkspaceV1 is host-deterministic workspace state for switch/fresh.
// Git and verified results here override conflicting SemanticHandoff claims.
type ObservedWorkspaceV1 struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Branch          string            `json:"branch,omitempty"`
	Head            string            `json:"head,omitempty"`
	Worktree        string            `json:"worktree,omitempty"`
	Porcelain       string            `json:"porcelain,omitempty"`
	SHAs            map[string]string `json:"shas,omitempty"`
	ObservedAt      time.Time         `json:"observedAt,omitempty"`
	GenerationID    string            `json:"generationId,omitempty"`
	EventCursor     string            `json:"eventCursor,omitempty"`
	VerifiedResults []VerifiedResult  `json:"verifiedResults,omitempty"`
}

// ObservedOrchestratorSchemaVersion is the wire version for ObservedOrchestratorV1.
const ObservedOrchestratorSchemaVersion = 1

// ObservedOrchestratorV1 is host-deterministic FLEET state for an orchestrator
// switch or fresh conversation: what the coordinator was actually coordinating.
//
// It is the orchestrator's analogue of ObservedWorkspaceV1, and exists for the
// same reason. A worker's untrusted claims are about git; an orchestrator's are
// about its workers — which sessions it started, what they are doing, whether
// they finished. Those claims are the ones most likely to be stale or invented
// after a long conversation, and they are exactly the facts AO holds
// authoritatively in its own session table. So the host reads them rather than
// asking, and the compiler prefers this over anything the agent asserts.
//
// Only durable, AO-owned facts belong here. Anything the orchestrator merely
// believes about a worker stays in SemanticHandoffV1 as a claim.
type ObservedOrchestratorV1 struct {
	SchemaVersion int       `json:"schemaVersion"`
	ProjectID     ProjectID `json:"projectId,omitempty"`
	ObservedAt    time.Time `json:"observedAt,omitempty"`
	GenerationID  string    `json:"generationId,omitempty"`
	// Workers is the project's non-orchestrator sessions, terminated ones
	// included: "the worker you think is still running finished an hour ago" is
	// precisely the correction this is for. Bounded — see OmittedTerminated.
	Workers []ObservedWorkerV1 `json:"workers,omitempty"`
	// OmittedTerminated counts terminated workers left out of Workers to keep
	// the handoff bounded. Reported rather than dropped silently: a truncated
	// fleet that reads as complete is worse than an explicitly partial one,
	// because the coordinator would treat absence as evidence.
	OmittedTerminated int `json:"omittedTerminated,omitempty"`
}

// ObservedWorkerV1 is one worker as AO records it, not as the orchestrator
// remembers it.
// Deliberately limited to what the session record itself holds. PR state lives
// behind a different store surface, and a field the observer cannot populate
// honestly is worse than an absent one — the whole point of Observed is that
// everything in it is a fact AO checked.
type ObservedWorkerV1 struct {
	SessionID    SessionID    `json:"sessionId"`
	Harness      AgentHarness `json:"harness,omitempty"`
	RoleID       string       `json:"roleId,omitempty"`
	Branch       string       `json:"branch,omitempty"`
	Activity     string       `json:"activity,omitempty"`
	IsTerminated bool         `json:"isTerminated,omitempty"`
}

// CompiledHandoff is the host-authored prompt fragment for the target session.
// Produced by handoff.Compile; never trust agent git/test claims over Observed.
type CompiledHandoff struct {
	// Text is the markdown (or plain) section injected into the target launch.
	Text string `json:"text"`
	// RoleID is preserved across switch (never changes on failover/switch).
	RoleID string `json:"roleId,omitempty"`
	// SourceGeneration / TargetGeneration fence input ownership at the boundary.
	SourceGeneration string `json:"sourceGeneration,omitempty"`
	TargetGeneration string `json:"targetGeneration,omitempty"`
	// Semantic / Observed copies used to build Text (audit).
	Semantic SemanticHandoffV1   `json:"semantic,omitempty"`
	Observed ObservedWorkspaceV1 `json:"observed,omitempty"`
	// ObservedOrchestrator is populated only for orchestrator handoffs; a worker
	// has no fleet to describe.
	ObservedOrchestrator *ObservedOrchestratorV1 `json:"observedOrchestrator,omitempty"`
}
