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
	SchemaVersion   int              `json:"schemaVersion"`
	Branch          string           `json:"branch,omitempty"`
	Head            string           `json:"head,omitempty"`
	Worktree        string           `json:"worktree,omitempty"`
	Porcelain       string           `json:"porcelain,omitempty"`
	SHAs            map[string]string `json:"shas,omitempty"`
	ObservedAt      time.Time        `json:"observedAt,omitempty"`
	GenerationID    string           `json:"generationId,omitempty"`
	EventCursor     string           `json:"eventCursor,omitempty"`
	VerifiedResults []VerifiedResult `json:"verifiedResults,omitempty"`
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
}
