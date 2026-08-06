package domain

import (
	"errors"
	"time"
)

// ErrActiveOrchestratorExists is returned by the session store when a write
// would leave a project with two active orchestrators, which migration 0057's
// partial unique index (idx_sessions_one_active_orchestrator) forbids.
//
// The index is a backstop, not the primary mechanism: ownership is normally
// serialized in-process by the per-project gate. It still fires for writes that
// bypass the gate (boot RestoreAll) and for databases reconciled by 0046 itself,
// and without this mapping it surfaces as an opaque 500. Callers that create or
// reactivate an orchestrator must treat it as a conflict, and — critically —
// must clean up any runtime they created before the losing write, or the
// project ends up with an untracked process holding its canonical worktree.
var ErrActiveOrchestratorExists = errors.New("domain: project already has an active orchestrator")

// These ID types are distinct string types so they can't be swapped at a call
// site by accident.
type (
	// SessionID identifies a session.
	SessionID string
	// ProjectID identifies a project.
	ProjectID string
	// IssueID identifies a tracker issue.
	IssueID string
)

// SessionKind distinguishes a worker session from an orchestrator session.
type SessionKind string

// Session kinds.
const (
	KindWorker       SessionKind = "worker"
	KindOrchestrator SessionKind = "orchestrator"
)

// SessionMetadata is the typed, off-status metadata for a session: operational
// handles and seed inputs used by Session Manager and reaper.
type SessionMetadata struct {
	Branch            string `json:"branch,omitempty"`
	WorkspacePath     string `json:"workspacePath,omitempty"`
	WorkspaceRepoPath string `json:"workspaceRepoPath,omitempty"`
	DiffBaseSHA       string `json:"diffBaseSha,omitempty"`
	DiffBaseRef       string `json:"diffBaseRef,omitempty"`
	RuntimeHandleID   string `json:"runtimeHandleId,omitempty"`
	RuntimeLaunchID   string `json:"runtimeLaunchId,omitempty"`
	AgentSessionID    string `json:"agentSessionId,omitempty"`
	Prompt            string `json:"prompt,omitempty"`
	// ProviderConversationID is the opaque handle a Chat driver needs to resume
	// this session's provider conversation after a restart (a Codex thread id
	// today). Normally empty for TUI sessions. It remains a distinct field from
	// AgentSessionID because most harnesses do not prove those protocol identities
	// interchangeable; the interface-transition coordinator copies one value into
	// both only after the adapter explicitly declares that equivalence.
	ProviderConversationID string `json:"providerConversationId,omitempty"`
	// ControllerGeneration is rotated each time a Chat controller is started for
	// this session. Events carrying an older generation are rejected, so a
	// controller that is dying cannot mutate the session that replaced it. Not
	// the same fence as RuntimeLaunchID, which covers terminal runtimes.
	ControllerGeneration string `json:"controllerGeneration,omitempty"`
	// PreviewURL is the browser preview target the desktop app opens for this
	// session. Set via `ao preview` (POST /sessions/{id}/preview); persisted so
	// it survives a daemon restart. Empty means no preview has been requested.
	PreviewURL string `json:"previewUrl,omitempty"`
	// PreviewRevision is a monotonic counter bumped on every `ao preview` call,
	// even when PreviewURL is unchanged. The desktop browser panel keys
	// navigation on it so a repeated `ao preview <same-url>` still refreshes.
	PreviewRevision int64 `json:"previewRevision,omitempty"`

	// Role is the durable multi-sub role identity pinned at spawn (optional).
	// Persisted in session metadata columns once migration 0053 is applied;
	// until then it is carried in-memory for the spawn path and tests.
	Role SessionRoleBinding `json:"role,omitempty"`

	// SwitchPending is set while a worker switch/fresh saga has stopped the
	// source and not yet durable target_ack. Current Harness / Role.Resolved*
	// remain the SOURCE until ack promotes the pending target. Non-nil pending
	// gates session input (sessionguard).
	SwitchPending *SwitchPending `json:"switchPending,omitempty"`

	// Pause is the durable pause pin (Phase 3A). Non-nil gates every
	// AO-initiated pane write in sessionguard and is cleared only by an
	// explicit resume.
	Pause *SessionPause `json:"pause,omitempty"`

	// SpawnCapabilityHash is SHA-256 hex of the random per-session spawn
	// capability. The plaintext token is never stored — only injected as
	// AO_SPAWN_CAPABILITY for the owning process.
	SpawnCapabilityHash string `json:"-"`
}

// SwitchPending is the durable in-flight target pin for a worker switch/fresh.
// Promoted into Harness / Role only after lifecycle target_ack is persisted.
// Must be persisted before source destruction so boot recovery can re-drive.
type SwitchPending struct {
	GenerationID string              `json:"generationId"`
	Kind         LifecycleLedgerKind `json:"kind"`
	FromHarness  AgentHarness        `json:"fromHarness,omitempty"`
	ToHarness    AgentHarness        `json:"toHarness"`
	FromModel    string              `json:"fromModel,omitempty"`
	ToModel      string              `json:"toModel,omitempty"`
	// OriginalTask is the immutable user task prompt without compiled handoffs.
	OriginalTask string `json:"originalTask,omitempty"`
	// RoleID preserved across switch (never changes).
	RoleID string `json:"roleId,omitempty"`
	// PayloadJSON is the full switchPayload (semantic/observed/compiled) so
	// recovery does not depend on a durable post_stop ledger row.
	PayloadJSON string `json:"payloadJson,omitempty"`
	// SourceRuntimeHandleID is the pre-stop handle (for diagnostics/recovery).
	SourceRuntimeHandleID string `json:"sourceRuntimeHandleId,omitempty"`
}

// SessionRecord is the persistence shape. It intentionally stores only durable
// facts: identity, agent harness, activity_state, is_terminated, and operational
// metadata. The user-facing Status is derived from these facts plus PR facts.
type SessionRecord struct {
	ID        SessionID    `json:"id"`
	ProjectID ProjectID    `json:"projectId"`
	IssueID   IssueID      `json:"issueId,omitempty"`
	Kind      SessionKind  `json:"kind"`
	Harness   AgentHarness `json:"harness,omitempty"`
	// ReviewerHarness is this session's preferred reviewer. Empty delegates to
	// the project configuration.
	ReviewerHarness ReviewerHarness `json:"reviewerHarness,omitempty" enum:"claude-code,codex,opencode"`
	DisplayName     string          `json:"displayName,omitempty"`
	// Mode is the session's currently committed conversation controller. Every
	// send, restore, kill, and reaper decision dispatches from it. Only the
	// durable interface-transition coordinator may change it; the daemon default
	// never changes an existing session. Rows written before Chat mode existed
	// read back as SessionModeTUI.
	Mode     SessionMode `json:"mode" enum:"chat,tui"`
	Activity Activity    `json:"activity"`
	// FirstSignalAt is when the FIRST agent hook callback arrived for the
	// current spawn/restore: raw signal receipt, independent of the derived
	// activity state. Zero means no hook has ever reported, which deriveStatus
	// surfaces as StatusNoSignal after a grace period. Internal fact, not part
	// of the API read model.
	FirstSignalAt time.Time `json:"-"`
	IsTerminated  bool      `json:"isTerminated"`
	// TerminateOnPRMerge is a user-controlled lifecycle policy. When enabled,
	// completing the session's PR set through a merge tears down the session.
	TerminateOnPRMerge bool            `json:"terminateOnPrMerge"`
	Metadata           SessionMetadata `json:"-"`
	// CleanupGeneration is a monotonic counter bumped each time the session is
	// un-terminated (spawn/restore). The terminal-resource reconciler stamps its
	// durable cleanup facts with the generation they were written for so a
	// finalize started under an earlier terminal episode cannot satisfy a later
	// one. Internal fact, not part of the API read model.
	CleanupGeneration int64      `json:"-"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	IsPinned          bool       `json:"isPinned"`
	PinnedAt          *time.Time `json:"pinnedAt,omitempty"`
}

// Session is the read-model returned across the API boundary: a SessionRecord
// plus derived display facts. Neither Status nor SCMStatus is persisted.
type Session struct {
	SessionRecord
	Status           SessionStatus `json:"status" enum:"working,pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged,needs_input,exited,idle,terminated,no_signal"`
	SCMStatus        SessionStatus `json:"scmStatus,omitempty" enum:"pr_open,draft,ci_failed,review_pending,changes_requested,approved,mergeable,merged"`
	TerminalHandleID string        `json:"terminalHandleId,omitempty"`
	// PRs are the session's attributed pull requests (one session can own many).
	// They feed status derivation and are surfaced on the API read model. Not
	// serialized here: the HTTP boundary maps them to the curated wire shape.
	PRs []PRFacts `json:"-"`
}
