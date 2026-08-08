// Package sessionmanager drives internal session command operations over runtime,
// agent, workspace, storage, messenger, and lifecycle dependencies.
package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/readonly"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionguard"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// Sentinel errors returned by the Session Manager; callers match them with
// errors.Is.
var (
	ErrNotFound         = errors.New("session: not found")
	ErrNotRestorable    = errors.New("session: not restorable (not terminal)")
	ErrTerminated       = errors.New("session: terminated")
	ErrAgentExited      = errors.New("session: agent exited")
	ErrAgentNotExited   = errors.New("session: agent has not exited")
	ErrIncompleteHandle = errors.New("session: incomplete teardown handle")
	// ErrProjectNotResolvable means the spawn's project has no usable repo
	// (unregistered, archived, or missing a path). The API maps it to a 400.
	ErrProjectNotResolvable = errors.New("session: project repo not resolvable")
	// ErrUnknownHarness means the requested agent harness has no registered
	// adapter. The API maps it to a 400 so a typo'd `--harness` is a validation
	// error, not an opaque 500.
	ErrUnknownHarness = errors.New("session: unknown agent harness")
	// ErrMissingHarness means neither the spawn request nor the project's role
	// config selected an agent. Worker/orchestrator spawns must be explicit.
	ErrMissingHarness = errors.New("session: agent harness required")
	// ErrScratchBranchUnsupported means a caller tried to force git branch
	// semantics onto a scratch project.
	ErrScratchBranchUnsupported = errors.New("session: scratch projects do not support branches")
	// ErrNotResumable means a terminated session cannot be relaunched: its adapter
	// cannot natively resume it AND it has no prompt to fresh-launch from, and it is
	// not an orchestrator (orchestrators are promptless by design and relaunch fresh
	// with the system prompt only). Workers without a task and without a native
	// session id have nothing meaningful to restore.
	ErrNotResumable = errors.New("session: nothing to resume from")
	// ErrSwitchInProgress means an agent switch is already running for this
	// session. The API maps it to a 409 so a double-submit does not race two
	// teardown/relaunch cycles over one worktree.
	ErrSwitchInProgress = errors.New("session: switch already in progress")
	// ErrInterfaceTransitionInProgress is the INTERFACE transition's fence, and
	// it is deliberately not ErrSwitchInProgress.
	//
	// They are different operations with different remedies: a switch fence
	// clears when the switch saga finishes or is recovered, an interface
	// transition clears when the transition settles or is cancelled. Sharing a
	// sentinel meant whichever toAPIError case came first answered for both,
	// and the one that came first was the interface one — so every ordinary
	// switch, fresh-conversation and input-fence conflict told the client it
	// was "already switching interfaces", and SWITCH_IN_PROGRESS became
	// unreachable.
	ErrInterfaceTransitionInProgress = errors.New("session: interface transition already in progress")
	// ErrSwitchChatUnsupported refuses the switch/fresh saga for a chat session.
	// The saga stops a terminal runtime, probes it for liveness, and treats an
	// empty runtime handle as confirmed death — none of which describes a chat
	// controller. Lifting this needs stop and recovery for that controller, not
	// a relaxed precondition.
	ErrSwitchChatUnsupported = errors.New("session: switch is not supported for chat sessions yet")
	// ErrSwitchNotSupported means source/target harness lacks switch_supported.
	ErrSwitchNotSupported = errors.New("session: harness does not support switch")
	// ErrSwitchPostStop means the source runtime was already stopped; the
	// compiled handoff is retained on the lifecycle ledger for target retry.
	ErrSwitchPostStop = errors.New("session: switch failed after source stop; handoff retained")
	// ErrNotWorker means this switch/fresh entry point is worker-only.
	// Orchestrators use FreshOrchestratorConversation, which gates first.
	ErrNotWorker = errors.New("session: worker kind required")
	// ErrNotOrchestrator means an orchestrator-only operation was asked for a
	// worker session.
	ErrNotOrchestrator = errors.New("session: orchestrator kind required")
	// ErrOrchestratorCrossHarness is retained for wire/error compatibility with
	// older callers. Cross-harness orchestrator switching now enters through the
	// gated SwitchOrchestrator path; the worker entry point still rejects an
	// orchestrator before any runtime effect.
	ErrOrchestratorCrossHarness = errors.New("session: cross-harness orchestrator switch is not supported yet")
	// ErrSwitchNothingToRecover means no incomplete post_stop saga exists for
	// the session (already acked, never reached post_stop, or not a switch).
	ErrSwitchNothingToRecover = errors.New("session: no incomplete post_stop switch to recover")
	// ErrSwitchUncertain means destroy/probe could not establish source/target
	// runtime liveness; recovery must not invent a second live generation.
	ErrSwitchUncertain = errors.New("session: switch runtime state uncertain")
	// ErrInterfaceHandoffUnsupported means the harness has not proven that its
	// TUI resume identity and Chat protocol identity name the same conversation.
	ErrInterfaceHandoffUnsupported = errors.New("session: interface handoff unsupported")
	// ErrNativeConversationMissing means a supported harness has not yet exposed
	// the native id required to resume it through the other controller.
	ErrNativeConversationMissing = errors.New("session: native conversation id unavailable")
	// ErrInterfaceAlreadySelected makes a stale/double switch request an explicit
	// conflict instead of leaking a generic 500 after the first switch commits.
	ErrInterfaceAlreadySelected = errors.New("session: requested interface is already selected")
	// ErrInterfaceTransitionNotFound distinguishes a missing handoff from a
	// missing session when DELETE is retried after the transition settled.
	ErrInterfaceTransitionNotFound = errors.New("session: no active interface transition")
	// ErrInterfaceTransitionNotCancellable protects the no-overlap invariant once
	// the source controller is already stopping or stopped.
	ErrInterfaceTransitionNotCancellable = errors.New("session: interface transition can no longer be cancelled")
	// ErrResumeInProgress prevents concurrent resume requests from replacing the
	// same runtime twice.
	ErrResumeInProgress = errors.New("session: agent resume already in progress")
	// ErrAwaitingDecision means the session is paused on a pending
	// permission/approval dialog. Send refuses to paste into it: the runtime
	// appends Enter after every paste, and an Enter into a decision dialog
	// would answer it on the user's behalf. The API maps it to a 409; the
	// caller retries once the user has answered in the terminal.
	ErrAwaitingDecision = errors.New("session: awaiting a user decision")
	// ErrPromptNotReady refuses an after-start task delivery AO cannot prove is
	// landing in the agent's own input prompt — the readiness wait expired, the
	// pane is showing a trust/approval screen, or the adapter offers no evidence
	// of readiness at all.
	//
	// It is deliberately NOT ErrAwaitingDecision. That sentinel's remedy is
	// "answer it in the session terminal", and by the time a caller reads this
	// there is no such terminal: spawn tears the runtime and workspace down on
	// this error, and relaunch parks or terminates the session. Pointing a
	// person at a pane that no longer exists is a worse answer than a generic
	// one, because they will go looking.
	//
	// Delivering anyway is what cost three live sessions: Grok's
	// repository-trust screen prints the same "Grok Build" banner the readiness
	// matcher accepted as proof of a prompt, and the pasted brief's "n" chose
	// "No, quit".
	ErrPromptNotReady = errors.New("session: agent is not at an input prompt")
)

// Env vars a spawned process reads to learn who it is. A worker that starts
// its own Docker containers (a database, a queue, any ad-hoc service) should
// label them `--label ao.session=$AO_SESSION_ID` so AO's container reaper
// (dockerreap) removes them on session kill/terminal state — see #2652. Add
// `--label ao.spare=true` to a deliberately shared container that must
// survive past this session.
const (
	EnvSessionID = "AO_SESSION_ID"
	EnvProjectID = "AO_PROJECT_ID"
	EnvIssueID   = "AO_ISSUE_ID"
	// EnvRuntimeLaunchID identifies the current supervised agent generation.
	EnvRuntimeLaunchID = "AO_RUNTIME_LAUNCH_ID"
	// EnvDataDir tells a spawned agent's AO hook commands where the store lives.
	EnvDataDir = "AO_DATA_DIR"
	// EnvBrowserCapability proves ownership of the session's browser target.
	EnvBrowserCapability = "AO_BROWSER_CAPABILITY"
	// EnvBrowserRuntimeToken must never be inherited by a worker. It authenticates
	// the privileged Electron runtime, not session-scoped browser callers.
	EnvBrowserRuntimeToken = "AO_BROWSER_RUNTIME_TOKEN" //nolint:gosec // Environment variable name, not a credential.
	// EnvSpawnCapability proves the calling agent session identity for spawn.
	// Combined with AO_SESSION_ID (sent as X-AO-Caller-Session-Id), the daemon
	// enforces RoleExecutionPolicy.CanSpawn. Not spoofable via AO_SESSION_ID alone.
	EnvSpawnCapability = "AO_SPAWN_CAPABILITY"
	// EnvManagedSession is injected into every AO-managed agent runtime only.
	// CLI uses it (with session id/capability) to refuse operator runfile
	// upgrade — unlike AO_DATA_DIR, which is a supported external-shell config.
	EnvManagedSession = "AO_MANAGED_SESSION"
	// EnvOperatorSpawnToken must never reach session processes (tmux/ConPTY
	// inherit os.Environ). Cleared explicitly in runtimeEnv.
	EnvOperatorSpawnToken = "AO_OPERATOR_SPAWN_TOKEN" //nolint:gosec // env name, not a secret
)

// hookBinaryName is the executable name the workspace hook commands invoke:
// every agent adapter installs a bare `ao hooks <agent> <event>`. The session
// PATH pin (hookPATH) only works when the daemon's own executable carries this
// name, since prepending its directory must change what `ao` resolves to.
const hookBinaryName = "ao"

type lifecycleRecorder interface {
	PrepareLaunch(id domain.SessionID, launchID string) error
	CancelLaunch(id domain.SessionID, launchID string)
	MarkSpawned(ctx context.Context, id domain.SessionID, metadata domain.SessionMetadata) error
	CommitControllerEpoch(ctx context.Context, id domain.SessionID, source, target domain.SessionMode, nativeConversationID string, startFresh bool) (bool, error)
	MarkTerminated(ctx context.Context, id domain.SessionID) error
}

// ShellTerminalCloser gates a session's scoped shell terminals around every
// path that releases its worktree (Kill, Cleanup, RetireForReplacement, the
// reconcile/shutdown save-and-teardown path), so none of them removes a
// worktree out from under a shell whose cwd still points into it — on Windows
// an open handle on that directory can even make the removal itself fail.
//
// BeginSessionTeardown drains the session's open shells and, on success,
// blocks any new OpenShellTerminal for that session until the returned
// release function is called — the caller MUST call it exactly once
// (typically via defer) once its own worktree work finishes, whatever the
// outcome. That release is tied to this specific acquisition (a fresh
// closure), not looked up by session id, so it can never be confused with — or
// release — a different, unrelated Begin for the same session. An error from
// BeginSessionTeardown means some scoped runtime could not be confirmed dead;
// the caller MUST NOT touch the worktree in that case, and release is nil (the
// gate already released itself on that error path).
//
// Late-bound via SetShellTerminalCloser: shellterm.Service is built after
// Session Manager during boot (see daemon.startShellTerminals), mirroring why
// lifecycle.Manager takes its completion terminator the same way.
type ShellTerminalCloser interface {
	BeginSessionTeardown(ctx context.Context, id domain.SessionID) (release func(), err error)
}

// TerminalInputGate closes the raw terminal input path while an interface
// transition drains and stops a TUI controller. It is separate from Messenger:
// xterm keystrokes travel over the terminal mux and never pass through Send.
type TerminalInputGate interface {
	// BeginInputDrain atomically blocks later writes and returns the time of the
	// newest write that was accepted before the block. Session Manager uses that
	// barrier to avoid trusting an idle hook which predates already-buffered PTY
	// input.
	BeginInputDrain(terminalID string) (lastInputAt time.Time, release func())
}

type runtimeController interface {
	Create(ctx context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error)
	Destroy(ctx context.Context, handle ports.RuntimeHandle) error
	GetOutput(ctx context.Context, handle ports.RuntimeHandle, lines int) (string, error)
	// IsAlive reports whether the handle's runtime session still exists. Used by
	// Reconcile on boot to adopt crash-surviving sessions and reap leaked ones.
	IsAlive(ctx context.Context, handle ports.RuntimeHandle) (bool, error)
}

// RestoreMode reports whether a restore continued an agent-native transcript or
// relaunched from AO's saved task prompt.
type RestoreMode string

const (
	// RestoreModeNative means AO relaunched through the agent's native transcript resume command.
	RestoreModeNative RestoreMode = "native"
	// RestoreModeSavedPrompt means AO relaunched a new conversation from the saved task prompt.
	RestoreModeSavedPrompt RestoreMode = "saved_prompt"
	// RestoreModeFresh means AO relaunched without a saved task prompt.
	RestoreModeFresh RestoreMode = "fresh"
)

// RestoreResult is the command result for a restored session.
type RestoreResult struct {
	Session domain.SessionRecord
	Mode    RestoreMode
}

// Store is the persistence surface needed by the internal session Manager.
type Store interface {
	// GetProject loads a project row so spawn can resolve its per-project agent
	// config into the launch command. ok=false means the project is unknown.
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error)
	CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error)
	UpdateSession(ctx context.Context, rec domain.SessionRecord) error
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	// GetSessionByRuntimeHandleID looks up by live runtime_handle_id (tmux name).
	GetSessionByRuntimeHandleID(ctx context.Context, handleID string) (domain.SessionRecord, bool, error)
	// GetSessionByPendingSourceHandle looks up pending.sourceRuntimeHandleId after
	// source destroy clears runtime_handle_id.
	GetSessionByPendingSourceHandle(ctx context.Context, handleID string) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	// DeleteSession removes a session row only if it is still in seed state
	// (no workspace, runtime handle, agent session id, or prompt; not
	// terminated). Returns deleted=true when removal happened; deleted=false
	// when the row had already progressed past seed state — preserving the
	// no-resurrection guarantee for live sessions.
	DeleteSession(ctx context.Context, id domain.SessionID) (bool, error)
	// UpsertSessionWorktree records or updates the worktree row for a session.
	// SaveAndTeardownAll writes the preserved_ref here (even when empty) as the
	// "shutdown-saved" marker before ForceDestroying the worktree.
	UpsertSessionWorktree(ctx context.Context, row domain.SessionWorktreeRecord) error
	// ListSessionWorktrees returns every worktree row for a session. RestoreAll
	// uses this to identify sessions saved by the last SaveAndTeardownAll: the
	// presence of any row is the marker; preserved_ref may be empty for clean
	// worktrees.
	ListSessionWorktrees(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error)
	// DeleteSessionWorktrees consumes stale shutdown-restore markers. Explicit
	// Kill and successful RestoreAll must remove these rows to prevent
	// resurrecting sessions the user intentionally terminated.
	DeleteSessionWorktrees(ctx context.Context, id domain.SessionID) error
	// PutTemplateArtifact durably stores immutable role template bytes so a
	// restored session can use the exact template pinned at spawn.
	PutTemplateArtifact(ctx context.Context, id, sha256 string, content []byte, createdAt time.Time) error
	// GetTemplateArtifact loads the immutable role template bytes pinned on a
	// session. ok=false means the artifact is not present and restore must fail.
	GetTemplateArtifact(ctx context.Context, id string) (content []byte, sha string, ok bool, err error)
	// AppendLifecycleLedger records an append-only switch/pause/fresh event.
	AppendLifecycleLedger(ctx context.Context, rec domain.LifecycleLedgerRecord) error
	// SetSessionPauseIfAbsent writes ONLY the pause column, and only while the
	// pin is still absent. Column-owned rather than part of the full-row
	// update, so a writer holding a pre-pause snapshot cannot clear a pin it
	// never read. ok=false means the precondition failed, not an error.
	SetSessionPauseIfAbsent(ctx context.Context, id domain.SessionID, pause *domain.SessionPause, guard domain.PauseGuard, updatedAt time.Time) (bool, error)
	// ClearSessionPauseIfIncident lifts the pin only while it still names this
	// incident, so a stale resume cannot lift a newer pause.
	ClearSessionPauseIfIncident(ctx context.Context, id domain.SessionID, incidentID string, updatedAt time.Time) (bool, error)
	// ListOrchestratorReapQueue returns outstanding obligations to confirm the
	// death of superseded orchestrators (migration 0057). A missing table must
	// surface as an error, never as an empty queue.
	ListOrchestratorReapQueue(ctx context.Context) ([]domain.OrchestratorReapEntry, error)
	// DeleteOrchestratorReapEntry discharges one obligation. Only ever called
	// after death is authoritatively confirmed.
	DeleteOrchestratorReapEntry(ctx context.Context, id domain.SessionID) error
	// RecordOrchestratorReapAttempt stamps a failed drain for visibility.
	RecordOrchestratorReapAttempt(ctx context.Context, id domain.SessionID, at time.Time) error
	// PutOrchestratorReplacementIntent records that a project is owed an
	// orchestrator, written BEFORE retirement so a zero-owner interval is
	// recoverable. Upsert: the project gate makes a competing intent
	// unrepresentable, so a second write is a retry of the same obligation.
	PutOrchestratorReplacementIntent(ctx context.Context, intent domain.OrchestratorReplacementIntent) error
	// ListOrchestratorReplacementIntents returns outstanding replacements. A
	// missing table must surface as an error, never as an empty list.
	ListOrchestratorReplacementIntents(ctx context.Context) ([]domain.OrchestratorReplacementIntent, error)
	// DeleteOrchestratorReplacementIntent discharges a project's obligation,
	// only once a successor is actually live.
	DeleteOrchestratorReplacementIntent(ctx context.Context, project domain.ProjectID) error
	// RecordOrchestratorReplacementAttempt stamps a failed recovery.
	RecordOrchestratorReplacementAttempt(ctx context.Context, project domain.ProjectID, at time.Time, cause string) error
	// ListLifecycleLedger returns events for a session oldest-first.
	ListLifecycleLedger(ctx context.Context, sessionID domain.SessionID) ([]domain.LifecycleLedgerRecord, error)
}

// Manager coordinates internal session spawn, restore, kill, and cleanup over
// the outbound ports. User-facing read-model assembly lives in the service package.
type Manager struct {
	runtime   runtimeController
	agents    ports.AgentResolver
	workspace ports.Workspace
	store     Store
	// messenger is a sessionguard.Guard wrapping the raw messenger, so every
	// pane write is guarded (re-read state, refuse a blocked session) without
	// each call site re-deriving the check. Send/confirmActive use Deliver for
	// its Outcome; Spawn/Restore use the interface-level Send for
	// initial-prompt delivery, where a blocked session is impossible.
	messenger *sessionguard.Guard
	// chat launches the structured controller for a chat-mode session. Nil means
	// this build cannot run chat sessions, and a chat spawn is refused rather
	// than silently downgraded to a terminal.
	// defaults resolves the daemon-owned default session interface for a spawn
	// that names no mode. Nil falls back to the compatibility default, so a build
	// without it behaves exactly as before.
	defaults            SessionModeDefaults
	chat                ChatLauncher
	lcm                 lifecycleRecorder
	preview             PreviewLifecycle
	browser             BrowserLifecycle
	browserCapabilities BrowserCapabilityIssuer
	dataDir             string
	clock               func() time.Time
	// lookPath is exec.LookPath in production; tests substitute a stub so
	// they don't need real binaries on PATH. Returns ports.ErrAgentBinaryNotFound
	// when the binary is missing so the sentinel propagates through toAPIError.
	lookPath func(string) (string, error)
	// executable resolves the daemon's own binary (os.Executable in
	// production); its directory is prepended to spawned sessions' PATH so the
	// workspace hook commands resolve back to this daemon. Tests inject a stub.
	executable  func() (string, error)
	newLaunchID func() string
	// ownershipMu protects resuming, switching, and the projectOwnership map
	// (single lock order). It is never held while blocking on a project gate.
	ownershipMu sync.Mutex
	resuming    map[domain.SessionID]struct{}
	switching   map[domain.SessionID]struct{}
	// projectOwnership serializes orchestrator ownership mutations per project.
	// Lock order is projectOwnership -> beginSwitch -> lifecycle/store; see
	// acquireProjectOwnership.
	projectOwnership map[domain.ProjectID]chan struct{}
	// switchCapsOverride is tests-only: when set, SwitchWorker uses it instead
	// of capabilities.For (e.g. force-enable cells or pin a matrix for isolation).
	switchCapsOverride func(domain.AgentHarness) capabilities.Caps
	// The fork's ownershipMu covers resuming (and switching, and the project
	// ownership map) under ONE lock order; upstream's separate resumeMu would
	// be a second lock over the same map.
	transitionMu sync.Mutex
	transitions  map[domain.SessionID]*interfaceTransitionRun
	// transitionDeliveryWake drives the durable transition-message outbox. A
	// daemon-lifetime worker is started by Reconcile; terminal transition paths
	// also make one immediate delivery attempt so tests and in-process callers do
	// not depend on the boot worker.
	transitionDeliveryMu        sync.Mutex
	transitionDeliveryRunning   bool
	transitionDeliveryWake      chan struct{}
	transitionDeliveryAttemptMu sync.Mutex
	// sendConfirm bounds the best-effort post-send confirmation that the session
	// actually became active (the agent accepted the prompt). New fills in the
	// sendConfirm* defaults; tests in this package shrink the timings directly.
	sendConfirm sendConfirmConfig
	logger      *slog.Logger

	// shellTerminalsMu guards shellTerminals: it is late-bound (see
	// ShellTerminalCloser) after Manager already exists, so a setter mutates it
	// under lock rather than through the constructor.
	shellTerminalsMu sync.Mutex
	shellTerminals   ShellTerminalCloser

	terminalInputGateMu sync.Mutex
	terminalInputGate   TerminalInputGate
}

// SetShellTerminalCloser wires every worktree-releasing path to gate the
// session's scoped shell terminals shut first. Safe to leave unset: a nil
// closer makes beginShellTerminalTeardown a no-op (release=nil, err=nil),
// which is what every test in this package that does not care about shell
// terminals relies on.
func (m *Manager) SetShellTerminalCloser(closer ShellTerminalCloser) {
	m.shellTerminalsMu.Lock()
	defer m.shellTerminalsMu.Unlock()
	m.shellTerminals = closer
}

// SetTerminalInputGate late-binds the daemon's terminal mux after Session
// Manager is constructed. Nil preserves the no-op behavior used by narrow tests.
func (m *Manager) SetTerminalInputGate(gate TerminalInputGate) {
	m.terminalInputGateMu.Lock()
	defer m.terminalInputGateMu.Unlock()
	m.terminalInputGate = gate
}

func (m *Manager) beginTerminalInputDrain(rec domain.SessionRecord) (lastInputAt time.Time, release func()) {
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return time.Time{}, nil
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return time.Time{}, nil
	}
	m.terminalInputGateMu.Lock()
	gate := m.terminalInputGate
	m.terminalInputGateMu.Unlock()
	if gate == nil {
		return time.Time{}, nil
	}
	return gate.BeginInputDrain(handle.ID)
}

// beginShellTerminalTeardown starts the shell-terminal gate for id ahead of
// releasing its worktree. release==nil, err==nil means no closer is wired
// (nothing to gate; proceed exactly as before this mechanism existed).
// err!=nil means some scoped shell terminal could not be confirmed closed —
// the caller MUST NOT touch the worktree, and release is nil (the gate
// already released itself). On success release is non-nil and tied to this
// specific acquisition; the caller MUST call it exactly once, typically via
// defer, once its own worktree work finishes.
func (m *Manager) beginShellTerminalTeardown(ctx context.Context, id domain.SessionID) (release func(), err error) {
	m.shellTerminalsMu.Lock()
	closer := m.shellTerminals
	m.shellTerminalsMu.Unlock()
	if closer == nil {
		return nil, nil
	}
	return closer.BeginSessionTeardown(ctx, id)
}

// requireShellTerminalTeardown is beginShellTerminalTeardown for callers that
// cannot proceed without a shell confirmation.
//
// The optional form above answers "no closer wired" with success, which is
// right for ordinary teardown: it degrades to the behaviour that predated the
// mechanism. It is wrong for the boot reaper, whose entire contract is that an
// unverified execution surface blocks the daemon — silently skipping the check
// would discharge an obligation nothing ever confirmed. Wiring is a boot-order
// fact (SetShellTerminalCloser must precede the drain), so an absent closer
// here is a wiring bug and is reported as one.
func (m *Manager) requireShellTerminalTeardown(ctx context.Context, id domain.SessionID) (release func(), err error) {
	m.shellTerminalsMu.Lock()
	closer := m.shellTerminals
	m.shellTerminalsMu.Unlock()
	if closer == nil {
		return nil, errors.New("shell terminal closer not wired: cannot confirm scoped shells are closed")
	}
	return closer.BeginSessionTeardown(ctx, id)
}

// PreviewLifecycle is the narrow teardown hook consumed by Session Manager.
// Keeping it here follows the consumer-owned interface boundary.
type PreviewLifecycle interface {
	StopSession(ctx context.Context, id domain.SessionID) error
}

// BrowserLifecycle is the narrow Electron-target teardown hook consumed by
// Session Manager. It must work even when no renderer panel mounted.
type BrowserLifecycle interface {
	DestroySession(ctx context.Context, id domain.SessionID) error
}

// BrowserCapabilityIssuer derives the capability injected into a worker.
type BrowserCapabilityIssuer interface {
	Token(id domain.SessionID) string
}

// sendConfirmConfig bounds the best-effort activity-confirmation loop run after
// Send. AO has no delivery ack: ao send returns 200 the moment tmux send-keys
// exits 0, and for a large multiline paste the single Enter may not submit the
// prompt — so UserPromptSubmit never fires and the orchestrator cannot tell the
// worker started. confirmActive observes the durable Activity.State (written by
// the user-prompt-submit hook) and re-sends Enter until the session is active or
// the budget is exhausted. It never fails the send.
type sendConfirmConfig struct {
	// pollInterval is the gap between activity reads.
	pollInterval time.Duration
	// attemptDeadline is how long to wait for active after each Enter.
	attemptDeadline time.Duration
	// maxAttempts bounds how many times Enter is (re)sent, counting the initial
	// Enter from Send itself.
	maxAttempts int
}

// Production sendConfirm bounds: 3 Enters total (1 from Send + 2 re-sends),
// each given 2s to flip the session active, polled every 300ms.
const (
	sendConfirmPollInterval    = 300 * time.Millisecond
	sendConfirmAttemptDeadline = 2 * time.Second
	sendConfirmMaxAttempts     = 3
)

// Deps are the collaborators a Session Manager needs; New wires them together.
type Deps struct {
	Runtime   runtimeController
	Agents    ports.AgentResolver
	Workspace ports.Workspace
	Store     Store
	Messenger ports.AgentMessenger
	// Defaults supplies the daemon-owned default session interface for spawns that
	// name no mode. Nil means always use the compatibility default.
	Defaults SessionModeDefaults
	// Chat launches the structured controller for a chat-mode session. Nil means
	// chat mode is unavailable, and a chat spawn is refused rather than silently
	// downgraded to a terminal.
	Chat                ChatLauncher
	Lifecycle           lifecycleRecorder
	Preview             PreviewLifecycle
	Browser             BrowserLifecycle
	BrowserCapabilities BrowserCapabilityIssuer
	// DataDir is exported to spawned agents as AO_DATA_DIR so their hook
	// commands can open the same store.
	DataDir string
	Clock   func() time.Time
	// LookPath overrides exec.LookPath for the pre-launch agent-binary check.
	// Production wiring leaves this nil and the manager defaults to
	// exec.LookPath; tests inject a stub so they need not seed real binaries.
	LookPath func(string) (string, error)
	// Executable overrides os.Executable for the session PATH pin (see
	// hookPATH). Production wiring leaves this nil; tests inject a stub so they
	// control what the test binary appears to be.
	Executable func() (string, error)
	// NewLaunchID overrides supervised-process generation for deterministic tests.
	NewLaunchID func() string
	// Logger receives spawn-time diagnostics (e.g. when the session PATH
	// cannot be pinned to the daemon binary). Nil defaults to slog.Default().
	Logger *slog.Logger
}

// New builds a Session Manager from its dependencies, defaulting the clock to
// time.Now when Deps.Clock is nil.
func New(d Deps) *Manager {
	m := &Manager{
		runtime:                d.Runtime,
		agents:                 d.Agents,
		workspace:              d.Workspace,
		store:                  d.Store,
		lcm:                    d.Lifecycle,
		preview:                d.Preview,
		browser:                d.Browser,
		browserCapabilities:    d.BrowserCapabilities,
		dataDir:                d.DataDir,
		clock:                  d.Clock,
		lookPath:               d.LookPath,
		executable:             d.Executable,
		newLaunchID:            d.NewLaunchID,
		resuming:               make(map[domain.SessionID]struct{}),
		switching:              make(map[domain.SessionID]struct{}),
		defaults:               d.Defaults,
		chat:                   d.Chat,
		transitions:            make(map[domain.SessionID]*interfaceTransitionRun),
		transitionDeliveryWake: make(chan struct{}, 1),
		sendConfirm: sendConfirmConfig{
			pollInterval:    sendConfirmPollInterval,
			attemptDeadline: sendConfirmAttemptDeadline,
			maxAttempts:     sendConfirmMaxAttempts,
		},
		logger: d.Logger,
	}
	if m.clock == nil {
		// UTC so spawn-stamped CreatedAt/UpdatedAt match every other session
		// write (rename, activity) — all of which use time.Now().UTC(). A local
		// default produced mixed-timezone timestamps in `ao session get`.
		m.clock = func() time.Time { return time.Now().UTC() }
	}
	if m.lookPath == nil {
		m.lookPath = exec.LookPath
	}
	if m.executable == nil {
		m.executable = os.Executable
	}
	if m.newLaunchID == nil {
		m.newLaunchID = uuid.NewString
	}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	// messenger is the raw d.Messenger wrapped in a Guard (needs m.logger, so it
	// is built after the logger default).
	m.messenger = sessionguard.New(d.Store, d.Messenger, m.logger)
	return m
}

// Spawn creates the session row (which assigns the "{project}-{n}" id), then the
// workspace and runtime, then reports completion to the LCM. If workspace
// materialization fails the still-seed row is deleted outright; a later failure
// parks the row as terminated and rolls back what was built.
// Spawn creates a session. Orchestrator spawns take the project ownership gate
// so they cannot interleave with a concurrent retirement, restore, or another
// orchestrator spawn; worker spawns are unaffected and stay fully concurrent.
func (m *Manager) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	if cfg.Kind != domain.KindOrchestrator {
		return m.spawnUnderOwnership(ctx, cfg)
	}
	release, err := m.acquireProjectOwnership(ctx, cfg.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", err)
	}
	defer release()
	return m.spawnUnderOwnership(ctx, cfg)
}

// spawnUnderOwnership is Spawn's body. Callers must already hold the project
// ownership gate when cfg.Kind is KindOrchestrator.
func (m *Manager) spawnUnderOwnership(ctx context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, int, int, error) {
	project, err := m.loadProject(ctx, cfg.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", err)
	}
	projectKind := project.Kind.WithDefault()
	if projectKind == domain.ProjectKindScratch && strings.TrimSpace(cfg.Branch) != "" {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", ErrScratchBranchUnsupported)
	}
	// Multi-sub role map (host-authoritative). Resolve once; prompt + launch
	// must consume this result (never re-resolve, never ignore errors).
	roleResult, roleErr := applyRoleMap(&cfg, project, m.dataDir)
	if roleErr != nil {
		return domain.SessionRecord{}, 0, 0, mapRoleError(roleErr)
	}
	// A per-project role override picks the harness when the spawn names none,
	// so a project can default workers to one agent and orchestrators to another.
	// Skip when applyRoleMap already set harness from a role binding.
	//
	// This runs BEFORE the chat preflight below, because the preflight asks the
	// adapter about a specific harness. Preflighting first passed an EMPTY
	// harness for any request that relies on the project default — a valid
	// non-role chat spawn — and rejected it.
	if cfg.RoleBinding.RoleID == "" {
		cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)
	}
	if cfg.Harness == "" {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w: configure project %s.agent or pass --harness / --role", ErrMissingHarness, roleConfigName(cfg.Kind))
	}

	// Reject an unknown harness before any durable state is created. Doing this
	// after CreateSession would leave a terminated orphan row and waste a
	// worktree on a spawn that can never launch.
	if _, ok := m.agents.Agent(cfg.Harness); !ok {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w: %q", ErrUnknownHarness, cfg.Harness)
	}

	// Resolve the controller mode after the harness is known and validated, and
	// before ANYTHING durable is written — including the role template CAS
	// below. Both halves of that sentence are load-bearing: preflight needs a
	// real harness to ask about, and a refused request must leave nothing. A chat request AO cannot honour
	// should cost nothing: no terminated row, no worktree, and no content-
	// addressed template row for a session that never existed. It never falls
	// back to TUI, which would put the user in a terminal they did not ask for.
	//
	// This sits above persistRoleTemplateArtifact deliberately. The CAS write is
	// harmless residue on its own (content-addressed, deduplicated, reused by
	// the next spawn of the same template), but "refused before anything
	// durable" should be true rather than nearly true.
	mode := m.resolveSessionMode(ctx, cfg.RequestedMode)
	if mode == domain.SessionModeChat {
		if m.chat == nil {
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w: chat mode is not available in this build", ports.ErrChatUnsupported)
		}
		if err := m.chat.PreflightChat(ctx, cfg.Harness); err != nil {
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", err)
		}
		// The ROLE preflight, alongside the harness one and for the same
		// reason. applyRoleMap has already run, so cfg.RoleBinding is the
		// host's resolved policy, not anything the client sent. A read-only
		// role cannot be enforced by the Chat controller — read_only_enforced
		// is a property of the terminal argv, which Chat never receives.
		//
		// This was missing at first and only a live spawn found it: the helper
		// existed, relaunch called it, and a read-only role still started in
		// chat on the FIRST try.
		if err := requireChatModeAllowed(cfg.RoleBinding); err != nil {
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", err)
		}
	}
	cfg.RequestedMode = mode

	if err := m.persistRoleTemplateArtifact(ctx, roleResult); err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: persist role template artifact: %w", err)
	}

	// A chat session runs no agent inside a terminal runtime, so the terminal
	// prerequisites are not its concern.
	if mode == domain.SessionModeTUI {
		if err := m.validateRuntimePrerequisites(); err != nil {
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: %w", err)
		}
	}

	prompt, systemPrompt, err := m.buildSpawnTexts(ctx, cfg, roleResult)
	if err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: prompt: %w", err)
	}
	promptBytes := len(prompt)
	systemPromptBytes := len(systemPrompt)

	// Random per-session spawn capability: only the hash is durable; plaintext
	// goes solely into the process env (never a global mint key under dataDir).
	spawnToken, spawnHash, err := spawncred.Issue()
	if err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: spawn capability: %w", err)
	}
	seed := seedRecord(cfg, m.clock())
	seed.Metadata.SpawnCapabilityHash = spawnHash
	rec, err := m.store.CreateSession(ctx, seed)
	if err != nil {
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn: create: %w", err)
	}
	id := rec.ID
	systemPromptFile, err := m.prepareSystemPromptFile(id, cfg.Harness, systemPrompt)
	if err != nil {
		m.rollbackSpawnSeedRow(ctx, id)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: system prompt file: %w", id, err)
	}

	branch := cfg.Branch
	if branch == "" {
		branch = DefaultSpawnBranch(id, cfg.Kind, sessionPrefix(project), projectKind, m.dataDir)
	}
	ws, workspaceProject, err := m.createSessionWorkspace(ctx, project, cfg, id, branch)
	if err != nil {
		// Nothing observable exists yet — no worktree, no runtime — so the seed
		// row is deleted outright instead of accumulating as a terminated orphan
		// in session lists (e.g. when gitworktree refuses the branch).
		m.rollbackSpawnSeedRow(ctx, id)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: workspace: %w", id, err)
	}

	// Per-project workspace provisioning: symlink shared files, then run any
	// post-create commands (e.g. `pnpm install`) before the agent launches.
	if err := m.provisionWorkspace(ctx, project, ws.Path); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, false)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: provision: %w", id, err)
	}

	// CLI agents receive the prompt as text and cannot consume inline binary
	// data, so any pasted/dropped images are written into the worktree and
	// referenced by path in the prompt. Done after provisioning (so the worktree
	// exists) and before the launch command is built (so the references reach
	// the agent).
	if len(cfg.Attachments) > 0 {
		refs, err := writeSpawnAttachments(ws.Path, cfg.Attachments)
		if err != nil {
			m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, false)
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: attachments: %w", id, err)
		}
		// Keep the attachments dir out of git status. Best-effort: the images are
		// already written and usable, so an exclude failure must not fail the spawn.
		if err := m.workspace.AddExclude(ctx, ws, "/"+attachmentsDir+"/"); err != nil {
			m.logger.Warn("spawn: exclude attachments dir", "sessionID", id, "error", err)
		}
		prompt = appendAttachmentReferences(prompt, refs)
	}

	// Composed, and the ORDER is the invariant: project base, then upstream's
	// per-spawn override, then the host-resolved role LAST. A role pin is
	// host-authoritative — resolve already refuses a caller-supplied harness or
	// model alongside a role — so letting the override land after the role
	// would invert that gate for anything resolve did not reject outright.
	//
	// Computed BEFORE the mode branch: both controllers must launch from the
	// same resolved values. Chat previously read effectiveAgentConfig(project)
	// directly, which silently dropped the role's model and permissions.
	agentConfig := mergeAgentConfig(
		applySpawnAgentConfig(effectiveAgentConfig(cfg.Kind, project.Config), cfg.AgentConfig),
		roleResult.AgentConfigPatch, roleResult.Policy, roleResult.Applied)

	// Everything above is shared: project, harness, prompts, seed row, worktree,
	// provisioning, attachments. From here the two modes launch different
	// controllers, and exactly one of them runs.
	if mode == domain.SessionModeChat {
		rec, err = m.launchChatController(ctx, chatSpawn{
			cfg:              cfg,
			project:          project,
			projectKind:      projectKind,
			record:           rec,
			workspace:        ws,
			workspaceProject: workspaceProject,
			prompt:           prompt,
			systemPrompt:     systemPrompt,
			agentConfig:      agentConfig,
			spawnToken:       spawnToken,
		})
		if err != nil {
			return domain.SessionRecord{}, 0, 0, err
		}
		return rec, promptBytes, systemPromptBytes, nil
	}

	agent, ok := m.agents.Agent(cfg.Harness)
	if !ok {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, false)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: no agent adapter for harness %q", id, cfg.Harness)
	}
	// Composed, and the ORDER is the invariant: project base, then upstream's
	// per-spawn override, then the host-resolved role LAST. A role pin is
	// host-authoritative — resolve already refuses a caller-supplied harness or
	// model alongside a role (ErrHarnessOverrideForbidden) — so letting the
	// override land after the role would invert that gate for anything resolve
	// did not reject outright.
	// spawnToken is ours: the session-scoped spawn capability injected as
	// AO_SPAWN_CAPABILITY. Upstream's signature has no such parameter.
	env := m.runtimeEnv(id, cfg.ProjectID, cfg.IssueID, project.Config.Env, spawnToken)
	m.augmentAgentRuntimeEnv(agent, env)
	if err := m.prepareWorkspace(ctx, agent, id, ws.Path, systemPrompt, systemPromptFile, agentConfig, env); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, false)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: %w", id, err)
	}
	launchCfg := ports.LaunchConfig{
		DataDir:          m.dataDir,
		SessionID:        string(id),
		WorkspacePath:    ws.Path,
		Kind:             cfg.Kind,
		Prompt:           prompt,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		IssueID:          string(cfg.IssueID),
		Config:           agentConfig,
		Permissions:      agentConfig.Permissions,
	}
	// Adapter-level RO for workspaceWrites=false (tools/sandbox — not prompt-only).
	if roleResult.Applied && !roleResult.Policy.WorkspaceWrites {
		// ApplyLaunch mutates launchCfg in place; everything downstream reads
		// launchCfg, so copying it back into agentConfig assigned a value
		// nothing goes on to use.
		readonly.ApplyLaunch(cfg.Harness, &launchCfg)
	}
	delivery, err := agent.GetPromptDeliveryStrategy(ctx, launchCfg)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: prompt delivery: %w", id, err)
	}
	if delivery == ports.PromptDeliveryAfterStart {
		launchCfg.Prompt = ""
	}
	argv, err := agent.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: launch command: %w", id, err)
	}
	// Pre-flight: confirm argv[0] actually exists on PATH (or as an absolute
	// path the adapter returned) BEFORE handing the launch to the runtime.
	// tmux happily creates a session+pane around a missing command, so an
	// unresolved binary would leak through as a "live" session that never ran.
	if err := m.validateAgentBinary(argv); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: %w", id, err)
	}
	m.augmentRuntimePATHForLaunchBinary(ctx, env, argv)
	argv, launchID, err := m.superviseAgentProcess(agent, id, env, argv)
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: supervisor: %w", id, err)
	}
	if err := m.lcm.PrepareLaunch(id, launchID); err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: prepare launch: %w", id, err)
	}
	defer m.lcm.CancelLaunch(id, launchID)
	handle, err := m.runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     id,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           env,
	})
	if err != nil {
		m.rollbackSeedSpawnWorkspace(ctx, rec, ws, workspaceProject, true)
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: runtime: %w", id, err)
	}

	metadata := domain.SessionMetadata{
		Branch:            ws.Branch,
		WorkspacePath:     ws.Path,
		WorkspaceRepoPath: ws.RepoPath,
		RuntimeHandleID:   handle.ID,
		RuntimeLaunchID:   launchID,
		Prompt:            prompt,
	}
	if projectKind == domain.ProjectKindSingleRepo {
		metadata.DiffBaseSHA, metadata.DiffBaseRef = resolveSpawnDiffBase(ctx, ws.Path, project.Config.WithDefaults().DefaultBranch)
	}
	if err := m.lcm.MarkSpawned(ctx, id, metadata); err != nil {
		runtimeDestroyed, cleanupErr := m.reapFailedLaunchRuntime(ctx, "spawn", id, handle, launchID)
		m.rollbackPreparedSpawnWorkspace(ctx, rec, ws, workspaceProject, runtimeDestroyed)
		cleanupErr = errors.Join(cleanupErr, m.markSpawnFailedTerminated(ctx, id))
		return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: completed: %w", id, errors.Join(err, cleanupErr))
	}
	if delivery == ports.PromptDeliveryAfterStart && prompt != "" {
		if err := m.deliverAfterStartPrompt(ctx, agent, launchCfg, handle, id, launchID, prompt); err != nil {
			runtimeDestroyed, cleanupErr := m.reapFailedLaunchRuntime(ctx, "spawn", id, handle, launchID)
			workspaceDestroyed := m.rollbackPreparedSpawnWorkspace(ctx, rec, ws, workspaceProject, runtimeDestroyed)
			if runtimeDestroyed && workspaceDestroyed {
				cleanupErr = errors.Join(cleanupErr, m.markSpawnFailedTerminatedWithoutWorkspace(ctx, id))
			} else {
				cleanupErr = errors.Join(cleanupErr, m.markSpawnFailedTerminated(ctx, id))
			}
			return domain.SessionRecord{}, 0, 0, fmt.Errorf("spawn %s: deliver prompt: %w", id, errors.Join(err, cleanupErr))
		}
	}
	rec, err = m.getRecord(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, 0, 0, err
	}
	return rec, promptBytes, systemPromptBytes, nil
}

// loadProject loads the project record so spawn can resolve its per-project
// config (harness/agent overrides, env, branch, rules, provisioning). A missing
// project yields a zero record rather than an error: the project may be
// unregistered yet still have live sessions, and an empty config simply means
// every field falls back to its default.
func (m *Manager) loadProject(ctx context.Context, projectID domain.ProjectID) (domain.ProjectRecord, error) {
	row, ok, err := m.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.ProjectRecord{}, fmt.Errorf("load project: %w", err)
	}
	if !ok {
		return domain.ProjectRecord{}, nil
	}
	return row, nil
}

func (m *Manager) createSessionWorkspace(ctx context.Context, project domain.ProjectRecord, cfg ports.SpawnConfig, id domain.SessionID, branch string) (ports.WorkspaceInfo, *ports.WorkspaceProjectInfo, error) {
	projectKind := project.Kind.WithDefault()
	if projectKind != domain.ProjectKindWorkspace {
		baseBranch := project.Config.WithDefaults().DefaultBranch
		if projectKind == domain.ProjectKindScratch {
			baseBranch = ""
		}
		ws, err := m.workspace.Create(ctx, ports.WorkspaceConfig{
			ProjectID:     cfg.ProjectID,
			SessionID:     id,
			Kind:          cfg.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        branch,
			BaseBranch:    baseBranch,
		})
		return ws, nil, err
	}
	workspaceProject, ok := m.workspace.(ports.WorkspaceProject)
	if !ok {
		return ports.WorkspaceInfo{}, nil, errors.New("workspace project materialization is not supported by workspace adapter")
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}
	childRepos := make([]ports.WorkspaceProjectRepoConfig, 0, len(repos))
	for _, repo := range repos {
		childRepos = append(childRepos, ports.WorkspaceProjectRepoConfig{
			Name:         repo.Name,
			RelativePath: repo.RelativePath,
			RepoPath:     filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
		})
	}
	info, err := workspaceProject.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     id,
		Kind:          cfg.Kind,
		SessionPrefix: sessionPrefix(project),
		Branch:        branch,
		RootRepoPath:  project.Path,
		BaseBranch:    project.Config.WithDefaults().DefaultBranch,
		Repos:         childRepos,
	})
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}
	for _, wt := range info.Worktrees {
		if err := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    id,
			RepoName:     wt.RepoName,
			Branch:       wt.Branch,
			BaseSHA:      wt.BaseSHA,
			WorktreePath: wt.Path,
			State:        "active",
		}); err != nil {
			_ = workspaceProject.DestroyWorkspaceProject(ctx, info)
			return ports.WorkspaceInfo{}, nil, fmt.Errorf("record workspace worktree %q: %w", wt.RepoName, err)
		}
	}
	return info.Root, &info, nil
}

func resolveSpawnDiffBase(ctx context.Context, root, defaultBranch string) (string, string) {
	for _, ref := range spawnDiffBaseRefCandidates(defaultBranch) {
		if sha, ok := spawnGitSingleLine(ctx, root, "merge-base", "HEAD", ref); ok {
			return sha, ref
		}
	}
	if sha, ok := spawnGitSingleLine(ctx, root, "rev-parse", "HEAD"); ok {
		return sha, "HEAD"
	}
	return "", ""
}

func spawnDiffBaseRefCandidates(defaultBranch string) []string {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	if !strings.HasPrefix(defaultBranch, "origin/") && !strings.HasPrefix(defaultBranch, "refs/") {
		add("origin/" + defaultBranch)
		add("refs/remotes/origin/" + defaultBranch)
	}
	add(defaultBranch)
	return refs
}

func spawnGitSingleLine(ctx context.Context, root string, args ...string) (string, bool) {
	cmd := aoprocess.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	value := strings.TrimSpace(string(out))
	return value, value != ""
}

func (m *Manager) destroySpawnWorkspace(ctx context.Context, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo) bool {
	if workspaceProject != nil {
		if adapter, ok := m.workspace.(ports.WorkspaceProject); ok {
			err := adapter.DestroyWorkspaceProject(ctx, *workspaceProject)
			_ = m.store.DeleteSessionWorktrees(ctx, ws.SessionID)
			return err == nil
		}
	}
	err := m.workspace.Destroy(ctx, ws)
	_ = m.store.DeleteSessionWorktrees(ctx, ws.SessionID)
	return err == nil
}

// rollbackPreparedSpawnWorkspace unwinds a spawn that failed AFTER its runtime
// existed. Both callers pass whether that runtime is confirmed dead, and an
// unconfirmed one blocks the workspace teardown outright: removing a worktree
// while an agent may still be executing inside it is the same mistake the
// retirement paths guard against, and here it would additionally destroy the
// only directory the surviving process is writing to.
func (m *Manager) rollbackPreparedSpawnWorkspace(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, runtimeDestroyed bool) bool {
	if runtimeDestroyed && m.destroySpawnWorkspace(ctx, ws, workspaceProject) {
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		return true
	}
	m.preserveFailedSpawnWorkspace(ctx, rec.ID, ws, runtimeDestroyed)
	return false
}

func (m *Manager) rollbackSeedSpawnWorkspace(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo, workspaceProject *ports.WorkspaceProjectInfo, prepared bool) {
	if m.destroySpawnWorkspace(ctx, ws, workspaceProject) {
		if prepared {
			m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		}
		m.rollbackSpawnSeedRow(ctx, rec.ID)
		return
	}
	m.preserveFailedSpawnWorkspace(ctx, rec.ID, ws, true)
	if err := m.markSpawnFailedTerminated(ctx, rec.ID); err != nil {
		// Best-effort by design — the spawn already failed — but a row left
		// active with no runtime holds its slot, so it must not be silent.
		m.logger.Error("spawn rollback: could not mark session terminated", "sessionID", rec.ID, "error", err)
	}
}

func (m *Manager) preserveFailedSpawnWorkspace(ctx context.Context, id domain.SessionID, ws ports.WorkspaceInfo, runtimeDestroyed bool) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		m.logger.Warn("spawn rollback: failed to load session for preserved workspace", "sessionID", id, "workspacePath", ws.Path, "error", err)
		return
	}
	if !ok {
		m.logger.Warn("spawn rollback: session missing for preserved workspace", "sessionID", id, "workspacePath", ws.Path)
		return
	}
	rec.Metadata.Branch = ws.Branch
	rec.Metadata.WorkspacePath = ws.Path
	rec.Metadata.WorkspaceRepoPath = ws.RepoPath
	if runtimeDestroyed {
		rec.Metadata.RuntimeHandleID = ""
		rec.Metadata.RuntimeLaunchID = ""
	}
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		m.logger.Warn("spawn rollback: failed to record preserved workspace", "sessionID", id, "workspacePath", ws.Path, "error", err)
	}
}

// effectiveHarness resolves the harness for a spawn: an explicit harness wins;
// otherwise the project's role override for the session kind applies. Empty is
// invalid for new worker/orchestrator launches and is rejected by Spawn.
func effectiveHarness(explicit domain.AgentHarness, kind domain.SessionKind, cfg domain.ProjectConfig) domain.AgentHarness {
	if explicit != "" {
		return explicit
	}
	if role := roleOverride(kind, cfg).Harness; role != "" {
		return role
	}
	return ""
}

func roleConfigName(kind domain.SessionKind) string {
	if kind == domain.KindOrchestrator {
		return "orchestrator"
	}
	return "worker"
}

// effectiveAgentConfig merges the role override's agent config over the
// project's base agent config; set override fields win.
func effectiveAgentConfig(kind domain.SessionKind, cfg domain.ProjectConfig) ports.AgentConfig {
	merged := cfg.AgentConfig
	override := roleOverride(kind, cfg).AgentConfig
	if override.Model != "" {
		merged.Model = override.Model
	}
	if override.Mode != "" {
		merged.Mode = override.Mode
	}
	if override.Permissions != "" {
		merged.Permissions = override.Permissions
	}
	return merged
}

func applySpawnAgentConfig(base, override ports.AgentConfig) ports.AgentConfig {
	if override.Model != "" {
		base.Model = override.Model
	}
	if override.Mode != "" {
		base.Mode = override.Mode
	}
	if override.Permissions != "" {
		base.Permissions = override.Permissions
	}
	return base
}

func roleOverride(kind domain.SessionKind, cfg domain.ProjectConfig) domain.RoleOverride {
	if kind == domain.KindOrchestrator {
		return cfg.Orchestrator
	}
	return cfg.Worker
}

// sessionPrefix returns the display prefix for a project: the explicit
// SessionPrefix when set, otherwise the first 12 characters of the project ID.
func sessionPrefix(project domain.ProjectRecord) string {
	if p := strings.TrimSpace(project.Config.SessionPrefix); p != "" {
		return p
	}
	if len(project.ID) <= 12 {
		return project.ID
	}
	return project.ID[:12]
}

// markSpawnFailedTerminated best-effort parks an orphaned spawn as terminated.
// A phantom half-spawned row is worse than a terminal one; we only delete the
// row when nothing observable has landed yet (seed state) via rollbackSpawn or
// rollbackSpawnSeedRow.
// markSpawnFailedTerminated parks a failed spawn terminated. Post-runtime
// callers must surface its error: an active row that could not be terminated
// holds the project's orchestrator slot (migration 0057) with nothing behind
// it. Pre-runtime seed rollbacks fail the whole spawn anyway and ignore it.
func (m *Manager) markSpawnFailedTerminated(ctx context.Context, id domain.SessionID) error {
	m.cleanupSystemPromptDir(id)
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		m.logger.Error("failed spawn: could not mark session terminated", "sessionID", id, "error", err)
		return fmt.Errorf("%w: session %s could not be terminated after a failed spawn: %w",
			ErrLaunchCleanupUnresolved, id, err)
	}
	return nil
}

// ErrBootUnsafe marks a reconciliation outcome the daemon must NOT serve on.
//
// It exists so the boot gate keys on the property rather than on a growing list
// of specific failures: anything that wraps this is fatal at startup by
// construction, and a new condition becomes fail-closed by wrapping it rather
// than by remembering to edit daemon.go. The bar for wrapping it is narrow —
// state AO cannot describe or cannot correct, where continuing would let a real
// process or a durable row diverge from what the database says.
var ErrBootUnsafe = errors.New("session: unsafe to serve")

// ErrRestoreMarkerUnresolved means a shutdown-saved marker that MUST be removed
// could not be. It is boot-fatal because the row it leaves behind is not inert:
// see neutralizeRestoreMarkers.
var ErrRestoreMarkerUnresolved = fmt.Errorf("%w: restore marker not neutralized", ErrBootUnsafe)

// ErrOrchestratorEvidenceUnresolved means boot could not establish which saved
// orchestrators a project has, so it cannot know whether one should have been
// neutralized. It is boot-fatal for the same reason as the above and by the same
// mechanism: an unexamined marker is still eligible, so killing the current
// owner would let a predecessor return on a later boot. Not looking and failing
// to delete leave the identical durable hazard.
var ErrOrchestratorEvidenceUnresolved = fmt.Errorf("%w: orchestrator restore evidence incomplete", ErrBootUnsafe)

// ErrLaunchCleanupUnresolved means a failed launch could not be rolled back to
// a state AO can describe. It is deliberately distinct from the launch failure
// itself: a launch failing is ordinary, and the compensating writes are what
// keep it ordinary. When THOSE fail, one of two invariants is broken —
//
//   - a runtime survived teardown and no row names it, so nothing (reconcile,
//     Kill, the boot reaper) can ever find it again; or
//   - a session that must not stay active could not be terminated, so it holds
//     the project's orchestrator slot with nothing behind it.
//
// Callers must surface it. Boot in particular must not serve on it: the
// terminated-session reap pass runs BEFORE RestoreAll (see Reconcile), so a
// relaunch that leaves an unconfirmed runtime is not swept until the next
// restart.
var ErrLaunchCleanupUnresolved = fmt.Errorf("%w: launch cleanup unresolved", ErrBootUnsafe)

// ErrPausedLivenessUnresolved is boot proving a paused session's runtime dead
// and then FAILING to record it.
//
// Boot-fatal rather than logged, because the surviving state is actively
// misleading rather than merely incomplete: the row still says the agent is
// working. A human looking at a paused session would be shown "Resume" for a
// process that no longer exists, and told nothing needs restarting. Serving a
// read model that boot has already disproved is worse than not serving.
var ErrPausedLivenessUnresolved = fmt.Errorf("%w: paused session liveness not recorded", ErrBootUnsafe)

// ErrRuntimeReapUnresolved means boot could not prove that a terminated
// session's recorded runtime is absent. A shutdown-saved row is eligible for
// RestoreAll immediately after the reap pass, so treating probe uncertainty as
// an ordinary skip can relaunch the row beside the runtime the failed probe did
// not disprove. The same classification covers a known-live runtime whose
// Destroy failed: in both cases restore must not run during this boot.
var ErrRuntimeReapUnresolved = fmt.Errorf("%w: terminated runtime reap unresolved", ErrBootUnsafe)

// reapFailedLaunchRuntime tears down the runtime of a launch that could not be
// adopted, and reports whether its death is CONFIRMED.
//
// MarkSpawned is the single write that adopts a launch: it clears is_terminated
// and records RuntimeHandleID/RuntimeLaunchID together. Until it commits, a
// runtime exists that no durable row names — so a `Destroy` whose error is
// discarded (what this replaces) could leave a process executing inside the
// session's workspace with nothing pointing at it. Reconcile, Kill and the boot
// reaper all work from the recorded handle, so an unrecorded runtime is not
// merely leaked, it is unreachable.
//
// Hence two rules. Death is established by probe, not by Destroy's return
// value (destroyRuntimeProbed, switch.go). And a runtime that is NOT confirmed
// dead has its identity written to the row anyway, so it stays reapable — a
// write that deliberately leaves is_terminated alone, which matters because the
// motivating failure is migration 0057's index rejecting the activation, and
// that write would fail again.
// UNCONFIRMED DEATH IS ALWAYS AN ERROR, recorded or not. Recording the survivor
// makes it reapable *eventually*; it does not make it gone. The distinction that
// matters to a caller is only how bad the state is, and both are bad enough that
// boot must not serve on either:
//
//   - recorded: a live runtime executing in the session's workspace that the
//     NEXT boot can find. But Reconcile's terminated-session reap pass runs
//     BEFORE RestoreAll, so nothing sweeps it during THIS boot.
//   - not recorded: the same live runtime, which nothing can ever find.
//
// Returning nil for the first would leave the boot gate with nothing to key on,
// which is exactly the hole this closes.
func (m *Manager) reapFailedLaunchRuntime(ctx context.Context, operation string, id domain.SessionID, handle ports.RuntimeHandle, launchID string) (confirmedDead bool, err error) {
	dead, probeErr := m.destroyRuntimeProbed(ctx, handle.ID)
	if dead {
		return true, nil
	}
	if adoptErr := m.adoptOrphanedLaunchRuntime(ctx, id, handle, launchID); adoptErr != nil {
		m.logger.Error("runtime survived a launch that could not be adopted, AND could not be recorded: it is now untracked",
			"operation", operation, "sessionID", id, "handleID", handle.ID, "error", adoptErr)
		return false, fmt.Errorf("%w: runtime %q survived %s of %s and could not be recorded: %w",
			ErrLaunchCleanupUnresolved, handle.ID, operation, id, adoptErr)
	}
	m.logger.Error("runtime survived a launch that could not be adopted; recorded its identity so it stays reapable",
		"operation", operation, "sessionID", id, "handleID", handle.ID, "error", probeErr)
	if probeErr != nil {
		return false, fmt.Errorf("%w: death of runtime %q could not be established after %s of %s; identity recorded: %w",
			ErrLaunchCleanupUnresolved, handle.ID, operation, id, probeErr)
	}
	return false, fmt.Errorf("%w: runtime %q survived %s of %s and is still executing; identity recorded",
		ErrLaunchCleanupUnresolved, handle.ID, operation, id)
}

// adoptOrphanedLaunchRuntime records the execution identity of a runtime that
// outlived a failed adoption, WITHOUT touching is_terminated. It is the
// difference between a process AO can still find and one it cannot, so its
// failure is returned rather than logged.
func (m *Manager) adoptOrphanedLaunchRuntime(ctx context.Context, id domain.SessionID, handle ports.RuntimeHandle, launchID string) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("read session %s: %w", id, err)
	}
	if !ok {
		return fmt.Errorf("session %s no longer exists", id)
	}
	rec.Metadata.RuntimeHandleID = handle.ID
	rec.Metadata.RuntimeLaunchID = launchID
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return fmt.Errorf("record runtime identity for %s: %w", id, err)
	}
	return nil
}

// clearStaleRuntimeIdentity strips RuntimeHandleID/RuntimeLaunchID from a row
// whose runtime is confirmed gone.
//
// Only the execution identity is cleared. Branch, WorkspacePath and
// AgentSessionID deliberately survive: the worktree still exists and those
// fields are what a later Restore rebuilds and resumes from, unlike
// markSpawnFailedTerminatedWithoutWorkspace, which clears them precisely
// because its workspace was destroyed.
func (m *Manager) clearStaleRuntimeIdentity(ctx context.Context, id domain.SessionID) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return
	}
	if rec.Metadata.RuntimeHandleID == "" && rec.Metadata.RuntimeLaunchID == "" {
		return
	}
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		m.logger.Warn("failed to clear stale runtime identity", "sessionID", id, "error", err)
	}
}

// markSpawnFailedTerminatedWithoutWorkspace parks a spawn failure after the
// runtime row had become observable, but clears launch handles for resources
// that were destroyed during rollback. This keeps later restore/cleanup paths
// from treating a removed worktree as reusable state.
func (m *Manager) markSpawnFailedTerminatedWithoutWorkspace(ctx context.Context, id domain.SessionID) error {
	if err := m.markSpawnFailedTerminated(ctx, id); err != nil {
		// Do NOT strip the handles below: on a row that is still active that
		// would hide it from runtime reconciliation while it holds the slot.
		return err
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		// Deliberately nil: the termination above already succeeded, which is
		// the part that matters. Stripping the handles is cleanup, and NOT
		// stripping them is the safe direction — reconcileReap uses the runtime
		// handle to sweep a leaked tmux from a terminated row, so a row that
		// keeps its handle is still reapable while one that loses it is not.
		m.logger.Warn("spawn failure cleanup: could not re-read session to strip handles",
			"sessionID", id, "found", ok, "error", err)
		return nil //nolint:nilerr // see above: losing the handle is worse than keeping it
	}
	rec.Metadata.Branch = ""
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.AgentSessionID = ""
	// Best-effort: the row is already terminated, so a stale path here costs a
	// redundant lookup rather than breaking an invariant.
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		m.logger.Warn("failed spawn: could not clear handles for destroyed resources", "sessionID", id, "error", err)
	}
	return nil
}

// rollbackSpawnSeedRow best-effort removes the row of a spawn that failed
// before anything observable (worktree, runtime) was built, so failed spawns
// don't accumulate terminated rows in session lists. DeleteSession only removes
// rows still in seed state; if the row has progressed or the delete itself
// fails, fall back to parking it terminated so a phantom row never looks live.
func (m *Manager) rollbackSpawnSeedRow(ctx context.Context, id domain.SessionID) {
	if deleted, err := m.store.DeleteSession(ctx, id); err == nil && deleted {
		m.cleanupSystemPromptDir(id)
		return
	}
	if err := m.markSpawnFailedTerminated(ctx, id); err != nil {
		m.logger.Error("spawn rollback: could not mark session terminated", "sessionID", id, "error", err)
	}
}

// rollbackSpawn deletes a session row when it is still in seed state — used
// when an out-of-band step that happens AFTER `Spawn` returns (e.g. PR claim
// over HTTP) has failed and the caller wants the partially-spawned session
// gone without leaving a terminated orphan visible under `--include-terminated`.
//
// If the row has progressed past seed state (workspace exists, runtime created,
// etc.), DeleteSession is a no-op and rollbackSpawn falls back to a Kill so the
// runtime/workspace are torn down. Returns (deleted, killed):
//   - deleted=true: the row was a seed row and has been removed
//   - killed=true:  the row had spawn output and was torn down + terminated
//   - both false:   the row was already terminated or absent — benign no-op
func (m *Manager) rollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	deleted, err = m.store.DeleteSession(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, err)
	}
	if deleted {
		m.cleanupSystemPromptDir(id)
		return true, false, nil
	}
	// killUnderOwnership, not Kill: RollbackSpawn may already hold the project
	// gate, and re-acquiring it here would deadlock.
	killed, err = m.killUnderOwnership(ctx, id)
	if err != nil {
		return false, false, err
	}
	return false, killed, nil
}

// RollbackSpawn is the public surface of rollbackSpawn for service-layer
// callers. An orchestrator rollback takes the project ownership gate: it can
// arrive arbitrarily late (it undoes an out-of-band step after Spawn returned)
// and must not race a replacement or tear down a successor's workspace.
func (m *Manager) RollbackSpawn(ctx context.Context, id domain.SessionID) (deleted, killed bool, err error) {
	rec, ok, getErr := m.store.GetSession(ctx, id)
	if getErr != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, getErr)
	}
	if !ok || rec.Kind != domain.KindOrchestrator {
		return m.rollbackSpawn(ctx, id)
	}
	release, acqErr := m.acquireProjectOwnership(ctx, rec.ProjectID)
	if acqErr != nil {
		return false, false, fmt.Errorf("rollback %s: %w", id, acqErr)
	}
	defer release()
	return m.rollbackSpawn(ctx, id)
}

// Kill tears down the runtime and workspace, then records terminal intent with
// the LCM. A workspace teardown refused by the worktree-remove safety
// (uncommitted work) is never forced: Kill succeeds with freed=false,
// signalling the workspace was preserved for later inspection/cleanup while
// the session itself is still marked terminated.
//
// A session whose runtime handle or workspace path is missing (e.g. spawn
// failed partway, handle lost after a crash) is still terminated after the
// available destroy steps are skipped so it can be cleaned up from the
// dashboard.
// Killing an orchestrator takes the project ownership gate, so a teardown
// cannot interleave with a replacement that is minting its successor.
func (m *Manager) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return false, fmt.Errorf("kill %s: interface transition: %w", id, err)
	} else if active {
		return false, fmt.Errorf("kill %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		return false, nil // already gone: benign race
	}
	if rec.Kind != domain.KindOrchestrator {
		return m.killUnderOwnership(ctx, id)
	}
	release, err := m.acquireProjectOwnership(ctx, rec.ProjectID)
	if err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	defer release()
	return m.killUnderOwnership(ctx, id)
}

// killUnderOwnership is Kill's body. Callers must already hold the project
// ownership gate for orchestrator sessions.
func (m *Manager) killUnderOwnership(ctx context.Context, id domain.SessionID) (bool, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	if !ok {
		return false, nil // already gone: benign race
	}
	m.stopPreviewBestEffort(ctx, id)
	m.destroyBrowserBestEffort(ctx, id)
	handle := runtimeHandle(rec.Metadata)
	ws := workspaceInfo(rec)

	// A superseded orchestrator row must never tear down the canonical
	// workspace its successor now owns. This suppresses *shared workspace*
	// teardown only — it is deliberately NOT applied to the runtime handle,
	// which belongs to this row's own process and would otherwise be left
	// running untracked after the row is terminated.
	sharedWorkspace, aliasErr := m.canonicalWorkspaceHeldByActiveOrchestrator(ctx, rec)
	if aliasErr != nil {
		return false, fmt.Errorf("kill %s: %w", id, aliasErr)
	}
	// Execution surfaces — the agent runtime and this session's scoped shells —
	// belong to THIS row no matter who owns the files, and must still be drained.
	// Only filesystem teardown is suppressed, so keep the original
	// workspace-presence signal for the shell gate below.
	hadWorkspace := ws.Path != ""
	if sharedWorkspace {
		m.logger.Warn("kill: workspace is owned by the active orchestrator; skipping filesystem teardown",
			"sessionID", id, "project", rec.ProjectID, "path", ws.Path)
		ws = ports.WorkspaceInfo{}
	}

	var workspaceProjectRows []ports.WorkspaceRepoInfo
	workspaceProject := false
	// Workspace-project rows name the root AND child worktrees. Under a shared
	// canonical workspace those child paths are the successor's too, so the
	// rows must be suppressed alongside ws — clearing ws alone would still let
	// destroyWorkspaceProjectRows delete the live successor's children.
	if !sharedWorkspace {
		if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
			return false, fmt.Errorf("kill %s: workspace rows: %w", id, rowErr)
		} else if ok {
			workspaceProjectRows = rows
			workspaceProject = true
		}
	}

	// Exactly one controller exists, so exactly one gets torn down. A chat
	// session has no runtime handle; its controller owns an app-server child
	// process, and closing it also settles any turn left in flight so a later
	// read does not show work that is no longer running.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		m.stopChatBestEffort(ctx, id)
	} else if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return false, fmt.Errorf("kill %s: runtime: %w", id, err)
		}
	}
	// Gate shut any shell terminal scoped to this session BEFORE the worktree
	// goes away: an open shell whose cwd is that directory can otherwise
	// survive the removal (and on Windows can even block it — an open handle
	// on a directory refuses deletion), or a concurrent Open could land a new
	// one in the same race window. A runtime that cannot be confirmed dead
	// stops Kill here — same shape as a dirty-workspace refusal — rather than
	// letting the worktree disappear out from under it.
	//
	// Gated on hadWorkspace, not ws.Path: a superseded orchestrator's shells are
	// scoped to the canonical workspace the successor now owns, so skipping this
	// would leave them alive and writing inside it.
	if hadWorkspace {
		release, err := m.beginShellTerminalTeardown(ctx, id)
		if err != nil {
			// Same shape as the dirty-workspace refusal below: the worktree is
			// left alone, but the restore marker still must not survive a user
			// kill, or the next boot's RestoreAll could resurrect a session the
			// user explicitly terminated (#2319).
			if err := m.store.DeleteSessionWorktrees(ctx, id); err != nil {
				m.logger.Warn("kill: delete restore marker failed", "sessionID", id, "error", err)
			}
			if err := m.lcm.MarkTerminated(ctx, id); err != nil {
				return false, fmt.Errorf("kill %s: %w", id, err)
			}
			m.cleanupSystemPromptDir(id)
			return false, nil
		}
		if release != nil {
			defer release()
		}
	}
	freed := false
	if workspaceProject {
		cleaned, err := m.destroyWorkspaceProjectRows(ctx, workspaceProjectRows)
		if err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				if err := m.lcm.MarkTerminated(ctx, id); err != nil {
					return false, fmt.Errorf("kill %s: %w", id, err)
				}
				m.cleanupSystemPromptDir(id)
				return false, nil
			}
			return false, fmt.Errorf("kill %s: workspace: %w", id, err)
		}
		freed = cleaned
		if cleaned {
			m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		}
	} else if ws.Path != "" {
		if err := m.workspace.Destroy(ctx, ws); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				if err := m.store.DeleteSessionWorktrees(ctx, id); err != nil {
					m.logger.Warn("kill: delete restore marker failed", "sessionID", id, "error", err)
				}
				if err := m.lcm.MarkTerminated(ctx, id); err != nil {
					return false, fmt.Errorf("kill %s: %w", id, err)
				}
				m.cleanupSystemPromptDir(id)
				return false, nil
			}
			return false, fmt.Errorf("kill %s: workspace: %w", id, err)
		}
		freed = true
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	}
	// Clear the restore marker so the next boot's RestoreAll cannot resurrect a
	// killed session (#2319). For workspace projects this must happen after
	// teardown reads the rows; dirty-preserved rows return above and are left as
	// non-restorable inventory.
	if err := m.store.DeleteSessionWorktrees(ctx, id); err != nil {
		m.logger.Warn("kill: delete restore marker failed", "sessionID", id, "error", err)
	}
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		return false, fmt.Errorf("kill %s: %w", id, err)
	}
	m.cleanupSystemPromptDir(id)
	return freed, nil
}

// RetireForReplacement terminates a live orchestrator and releases its branch
// for a replacement session. Unlike Kill, this captures uncommitted work before
// force-removing the worktree, so a dirty canonical orchestrator worktree does
// not block the replacement from claiming the canonical branch.
//
// This deliberately does not write a session_worktrees row: those rows are
// boot-restore markers, and a replaced orchestrator must stay terminated.
// It takes the project ownership gate, so a retirement cannot interleave with a
// concurrent orchestrator spawn or restore for the same project.
func (m *Manager) RetireForReplacement(ctx context.Context, id domain.SessionID) error {
	// Pre-gate read resolves the project only; the authoritative read happens
	// under the gate in retireForReplacementUnderOwnership.
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	if !ok {
		return nil
	}
	release, err := m.acquireProjectOwnership(ctx, rec.ProjectID)
	if err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	defer release()
	return m.retireForReplacementUnderOwnership(ctx, id)
}

// retireForReplacementUnderOwnership is RetireForReplacement's body. Callers
// must already hold the project ownership gate. It re-reads the session because
// any view taken before the gate was acquired is stale by construction.
func (m *Manager) retireForReplacementUnderOwnership(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	if !ok || rec.IsTerminated {
		return nil
	}
	m.stopPreviewBestEffort(ctx, id)
	m.destroyBrowserBestEffort(ctx, id)
	if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			return fmt.Errorf("retire replacement %s: clear restore markers: %w", id, err)
		}
		handle := runtimeHandle(rec.Metadata)
		if handle.ID != "" {
			if err := m.runtime.Destroy(ctx, handle); err != nil {
				return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
			}
		}
		return m.finalizeRetirement(ctx, id)
	}
	// Gate shut this session's scoped shell terminals before either branch
	// below force-removes its worktree (or worktrees, for a workspace
	// project). Unlike Kill there is no dirty-refusal path here — retirement
	// always force-destroys — so a shell that cannot be confirmed closed fails
	// the whole retirement instead of silently force-removing ground out from
	// under it.
	release, closeErr := m.beginShellTerminalTeardown(ctx, id)
	if closeErr != nil {
		return fmt.Errorf("retire replacement %s: %w", id, closeErr)
	}
	if release != nil {
		defer release()
	}

	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		return fmt.Errorf("retire replacement %s: workspace rows: %w", id, rowErr)
	} else if ok {
		return m.retireWorkspaceProjectForReplacement(ctx, rec, rows)
	}

	ws := workspaceInfo(rec)
	staleWorkspace := false
	if _, err := m.workspace.StashUncommitted(ctx, ws); err != nil {
		if !errors.Is(err, ports.ErrWorkspaceStale) {
			return fmt.Errorf("retire replacement %s: stash: %w", id, err)
		}
		staleWorkspace = true
		m.logger.Warn("retire replacement: stale workspace; skipping preserve", "sessionID", id, "path", ws.Path, "error", err)
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", id, err)
		}
	}
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		if staleWorkspace {
			m.logger.Warn("retire replacement: stale workspace cleanup failed", "sessionID", id, "path", ws.Path, "error", err)
		}
		return fmt.Errorf("retire replacement %s: force destroy: %w", id, err)
	}
	m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: clear restore markers: %w", id, err)
	}
	return m.finalizeRetirement(ctx, id)
}

// finalizeRetirement is the single success tail shared by every retirement
// branch — branchless/scratch, workspace-project, and single-repo alike.
//
// Release-then-terminate must happen on ALL of them: the orchestrator worktree
// and branch are canonical per project, so a row that is terminated *without*
// releasing its claim keeps naming whatever the successor spawns onto that
// path, and any later path-keyed teardown (Kill, Cleanup) would destroy the
// current orchestrator's worktree. Keeping this in one helper is what stops a
// future branch from terminating without releasing.
// Retirement is two writes and cannot be one: MarkTerminated goes through the
// lifecycle manager (activity state, flight bookkeeping, container reaping),
// while releasing the claim is a plain metadata update. A crash between them is
// therefore possible, and the ORDER decides which residue it leaves.
//
// Terminate first. The residue is then a terminated row that still names the
// canonical workspace — an alias, already defended by
// canonicalWorkspaceHeldByActiveOrchestrator and cleared by
// reconcileOrchestratorRetirement at boot.
//
// The reverse order looks tidier and is worse. Releasing the claim first leaves
// an ACTIVE orchestrator with no workspace: it still occupies the project's
// single active slot under migration 0057, so no successor can be created,
// while EnsureOrchestrator's idempotent path happily returns it and hands the
// caller a coordinator that owns nothing. That state is both more damaging and
// less obviously wrong than a stale path on a dead row.
func (m *Manager) finalizeRetirement(ctx context.Context, id domain.SessionID) error {
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		return fmt.Errorf("retire replacement %s: mark terminated: %w", id, err)
	}
	if err := m.releaseRetiredWorkspaceClaim(ctx, id); err != nil {
		return fmt.Errorf("retire replacement %s: %w", id, err)
	}
	return nil
}

// releaseRetiredWorkspaceClaim clears the workspace/runtime ownership fields of
// a retired session so its row can never alias a successor's canonical
// workspace. Role pin and identity are preserved for audit.
func (m *Manager) releaseRetiredWorkspaceClaim(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return fmt.Errorf("release workspace claim: %w", err)
	}
	if !ok {
		return nil
	}
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.WorkspaceRepoPath = ""
	rec.Metadata.Branch = ""
	rec.Metadata.RuntimeHandleID = ""
	rec.Metadata.RuntimeLaunchID = ""
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return fmt.Errorf("release workspace claim: %w", err)
	}
	return nil
}

// canonicalWorkspaceHeldByActiveOrchestrator reports whether rec's recorded
// workspace path is also recorded by a different, still-active orchestrator in
// the same project. Because the orchestrator worktree is canonical per project,
// that means the path belongs to the current owner and must not be torn down on
// behalf of a superseded row. Defence in depth behind
// releaseRetiredWorkspaceClaim, which prevents the alias from existing at all.
func (m *Manager) canonicalWorkspaceHeldByActiveOrchestrator(ctx context.Context, rec domain.SessionRecord) (bool, error) {
	path := strings.TrimSpace(rec.Metadata.WorkspacePath)
	if path == "" || rec.Kind != domain.KindOrchestrator {
		return false, nil
	}
	recs, err := m.store.ListSessions(ctx, rec.ProjectID)
	if err != nil {
		return false, fmt.Errorf("list sessions for %s: %w", rec.ProjectID, err)
	}
	for _, other := range recs {
		if other.ID == rec.ID || other.IsTerminated || other.Kind != domain.KindOrchestrator {
			continue
		}
		if strings.TrimSpace(other.Metadata.WorkspacePath) == path {
			return true, nil
		}
	}
	return false, nil
}

func (m *Manager) stopPreviewBestEffort(ctx context.Context, id domain.SessionID) {
	if m.preview == nil {
		return
	}
	if err := m.preview.StopSession(ctx, id); err != nil {
		m.logger.Warn("session preview cleanup failed", "sessionID", id, "error", err)
	}
}

func (m *Manager) destroyBrowserBestEffort(ctx context.Context, id domain.SessionID) {
	if m.browser == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.browser.DestroySession(cleanupCtx, id); err != nil {
		m.logger.Warn("session browser cleanup failed", "sessionID", id, "error", err)
	}
}

func (m *Manager) retireWorkspaceProjectForReplacement(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo) error {
	staleRepos := make(map[string]bool)
	for _, row := range rows {
		if _, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row)); err != nil {
			if !errors.Is(err, ports.ErrWorkspaceStale) {
				return fmt.Errorf("retire replacement %s repo %s: stash: %w", rec.ID, row.RepoName, err)
			}
			staleRepos[row.RepoName] = true
			m.logger.Warn("retire replacement: stale workspace repo; skipping preserve", "sessionID", rec.ID, "repo", row.RepoName, "path", row.Path, "error", err)
		}
	}
	handle := runtimeHandle(rec.Metadata)
	if handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return fmt.Errorf("retire replacement %s: runtime: %w", rec.ID, err)
		}
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if err := m.workspace.ForceDestroy(ctx, workspaceInfoFromRepoInfo(rows[i])); err != nil {
			if staleRepos[rows[i].RepoName] {
				m.logger.Warn("retire replacement: stale workspace repo cleanup failed", "sessionID", rec.ID, "repo", rows[i].RepoName, "path", rows[i].Path, "error", err)
			}
			return fmt.Errorf("retire replacement %s repo %s: force destroy: %w", rec.ID, rows[i].RepoName, err)
		}
	}
	m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
		return fmt.Errorf("retire replacement %s: clear restore markers: %w", rec.ID, err)
	}
	return m.finalizeRetirement(ctx, rec.ID)
}

// RestoreWithMode relaunches a torn-down session and reports whether AO used
// native resume, a saved-prompt fallback, or a fresh launch. The fallible I/O
// runs before any durable session write, so a failure never resurrects the row
// or destroys the worktree (it may hold the agent's prior work).
// RestoreWithMode relaunches a terminated session. Restoring an orchestrator
// takes the project ownership gate: restore creates or adopts the canonical
// orchestrator worktree *before* MarkSpawned flips the row to active, so
// without the gate two restores — or a restore racing a replacement — can adopt
// the same worktree before any uniqueness check is reached.
func (m *Manager) RestoreWithMode(ctx context.Context, id domain.SessionID) (RestoreResult, error) {
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: interface transition: %w", id, err)
	} else if active {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	if !ok {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
	}
	if rec.Kind == domain.KindOrchestrator {
		release, acqErr := m.acquireProjectOwnership(ctx, rec.ProjectID)
		if acqErr != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", id, acqErr)
		}
		defer release()
		// Re-read under the gate: the pre-gate view above only resolved kind
		// and project, and a concurrent replacement may have moved on since.
		rec, ok, err = m.store.GetSession(ctx, id)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
		}
		if !ok {
			return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrNotFound)
		}
	}
	if !rec.IsTerminated {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrNotRestorable)
	}
	meta := rec.Metadata
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, err)
	}
	// Mirror Kill's incomplete-handle guard: a session whose spawn failed before
	// the workspace landed has neither WorkspacePath nor Branch, and there is
	// nothing meaningful to restore from. Surface this as a typed 409 instead of
	// letting workspace.Restore fail with an opaque wrapped error.
	if meta.WorkspacePath == "" || (meta.Branch == "" && project.Kind.WithDefault() != domain.ProjectKindScratch) {
		return RestoreResult{}, fmt.Errorf("restore %s: %w", id, ErrIncompleteHandle)
	}
	// Resumability is decided inside restoreArgv, not here. A promptless session
	// can still be fully resumable when the harness pins a deterministic session id
	// (Claude Code). restoreArgv returns ErrNotResumable only for a promptless,
	// unresumable non-orchestrator (a worker with no task and no native id to resume).
	// Orchestrators always relaunch fresh with the system prompt only.

	ws, err := m.restoreSessionWorkspace(ctx, project, rec)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore %s: workspace: %w", id, err)
	}
	return m.relaunchRestoredSession(ctx, rec, project, ws)
}

func (m *Manager) relaunchRestoredSession(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo) (RestoreResult, error) {
	return m.relaunchSession(ctx, "restore", rec, project, ws, nil /* restart */, relaunchOpts{})
}

// ResumeAgentWithMode replaces an exited agent inside its still-live session.
// Unlike RestoreWithMode, it preserves the existing worktree and terminal
// identity and never changes the durable terminated flag as an intermediate
// step.
func (m *Manager) ResumeAgentWithMode(ctx context.Context, id domain.SessionID) (RestoreResult, error) {
	// Lock order is project ownership -> session resume fence, never the
	// reverse: an exited orchestrator must not be relaunched while a
	// replacement is retiring it.
	if rec, ok, err := m.store.GetSession(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	} else if ok && rec.Kind == domain.KindOrchestrator {
		release, acqErr := m.acquireProjectOwnership(ctx, rec.ProjectID)
		if acqErr != nil {
			return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, acqErr)
		}
		defer release()
	}
	// Upstream's gate, kept after the ownership gate: an interface transition
	// in flight owns the controller, and relaunching underneath it would give
	// the session two.
	if active, err := m.hasActiveInterfaceTransition(ctx, id); err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: interface transition: %w", id, err)
	} else if active {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrInterfaceTransitionInProgress)
	}
	if !m.beginAgentResume(id) {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrResumeInProgress)
	}
	defer m.endAgentResume(id)

	// Reload under both protections; the pre-gate read above resolved kind and
	// project only.
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	if !ok {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrNotFound)
	}
	if rec.IsTerminated {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrTerminated)
	}
	if rec.Activity.State != domain.ActivityExited {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrAgentNotExited)
	}
	meta := rec.Metadata
	// A chat session has no runtime handle BY DESIGN — no pane, nothing to
	// reattach — so requiring one refused every chat restart with "missing
	// runtime or workspace handles". That is the Restart control the pause
	// contract insists on keeping separate from Resume, and on the paused-dead
	// chat cell it was the only way back; relaunchSession already dispatches to
	// the chat controller from the persisted mode.
	//
	// Found by restarting a paused-dead CHAT session on a live daemon: resume
	// lifted the pause and left it exited, and restart then answered 409.
	// Loaded BEFORE the completeness check, because what counts as complete
	// depends on the project kind: a scratch session legitimately has no
	// branch. RestoreWithMode has always keyed on that; this path did not, so
	// a scratch chat session was rejected for a branch it is not supposed to
	// have — the runtime-handle exemption alone left it unreachable.
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, err)
	}
	// A chat session has no runtime handle BY DESIGN — no pane, nothing to
	// reattach — so requiring one refused every chat restart with "missing
	// runtime or workspace handles". That is the Restart control the pause
	// contract insists on keeping separate from Resume, and on the paused-dead
	// chat cell it was the only way back; relaunchSession already dispatches to
	// the chat controller from the persisted mode.
	//
	// Found by restarting a paused-dead CHAT session on a live daemon: resume
	// lifted the pause and left it exited, and restart then answered 409.
	chatMode := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	branchRequired := project.Kind.WithDefault() != domain.ProjectKindScratch
	if meta.WorkspacePath == "" ||
		(branchRequired && meta.Branch == "") ||
		(!chatMode && meta.RuntimeHandleID == "") {
		return RestoreResult{}, fmt.Errorf("resume agent %s: %w", id, ErrIncompleteHandle)
	}
	ws := ports.WorkspaceInfo{
		Path:      meta.WorkspacePath,
		Branch:    meta.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}
	handle := ports.RuntimeHandle{ID: meta.RuntimeHandleID}
	return m.relaunchSession(ctx, "resume agent", rec, project, ws, &handle, relaunchOpts{})
}

func (m *Manager) beginAgentResume(id domain.SessionID) bool {
	m.ownershipMu.Lock()
	defer m.ownershipMu.Unlock()
	if _, exists := m.resuming[id]; exists {
		return false
	}
	if _, switching := m.switching[id]; switching {
		return false
	}
	m.resuming[id] = struct{}{}
	return true
}

func (m *Manager) endAgentResume(id domain.SessionID) {
	m.ownershipMu.Lock()
	delete(m.resuming, id)
	m.ownershipMu.Unlock()
}

// relaunchOpts optional overrides for switch launches (pending target harness +
// forced generation id matching the lifecycle ledger).
type relaunchOpts struct {
	LaunchHarness domain.AgentHarness
	ForceLaunchID string
	RoleModel     string // when set, overrides agent config model for this launch
	// KeepSessionOnLaunchFailure suppresses the terminate half of the rollback
	// when MarkSpawned fails. Set by the switch saga, which owns that state:
	// its source is already stopped, and RecoverSwitchFromPostStop explicitly
	// REFUSES a terminated session (switch.go), so terminating here would swap a
	// recoverable ErrSwitchPostStop for a dead end. The runtime is still reaped
	// probe-authoritatively and a survivor's identity still recorded — only the
	// row's terminal state is left to the saga.
	KeepSessionOnLaunchFailure bool
	// ForceFresh bypasses native resume on both sides (upstream's
	// relaunchSessionFresh): used by the interface transition and by switches
	// where an empty conversation would fail target startup.
	ForceFresh bool
}

func (m *Manager) relaunchSession(ctx context.Context, operation string, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo, restartHandle *ports.RuntimeHandle, opts ...relaunchOpts) (RestoreResult, error) {
	var o relaunchOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	// Relaunch dispatches from the currently committed persisted mode, never
	// from a caller hint. The interface-transition coordinator changes that
	// fact only after stopping the old controller, then reuses this path.
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		// A read-only role must not reach Chat. read_only_enforced is a
		// property of the harness's TERMINAL launch — Codex earns it from the
		// --sandbox read-only argv this function builds. The Chat controller
		// does not take that argv, and Codex Chat maps ordinary permissions to
		// danger-full-access, so inheriting the harness capability into Chat
		// would hand writes to a role defined not to have them.
		if err := requireChatModeAllowed(rec.Metadata.Role); err != nil {
			return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
		}
		if o.ForceFresh {
			rec.Metadata.ProviderConversationID = ""
		}
		return m.resumeChatController(ctx, operation, rec, project, ws)
	}
	launchHarness := rec.Harness
	if o.LaunchHarness != "" {
		launchHarness = o.LaunchHarness
	}
	agent, ok := m.agents.Agent(launchHarness)
	if !ok {
		return RestoreResult{}, fmt.Errorf("%s %s: no agent adapter for harness %q", operation, rec.ID, launchHarness)
	}
	// Ephemeral target identity for prompt/config generation on switch/fresh
	// relaunches. Durable session Harness/Role pin stay on the source until
	// target_ack; only this in-memory copy is retargeted so the launched process
	// receives AUTHORITATIVE ROLE FOOTER "Harness: <target>" (and model).
	promptRec := rec
	// Only retarget a pin that EXISTS. This stamp is here so the authoritative
	// role footer names the target harness, and a session with no role pin has
	// no footer to retarget — writing ResolvedHarness onto an empty binding
	// instead manufactures a partial pin, which restoreRoleApplyResult then
	// correctly refuses as corrupt metadata (ErrIncompleteRolePin).
	//
	// That is not hypothetical: it is what a live orchestrator fresh
	// conversation hit. Every un-pinned session was unreachable behind the
	// service's ROLE_PIN_REQUIRED check, so the saga corrupted its own in-memory
	// copy and failed at the system prompt — after the source had already
	// stopped, leaving a post-stop recovery loop that failed identically on
	// every boot.
	if strings.TrimSpace(rec.Metadata.Role.RoleID) != "" && o.LaunchHarness != "" {
		promptRec.Metadata.Role.ResolvedHarness = o.LaunchHarness
		if o.LaunchHarness != rec.Harness {
			// Cross-harness: explicit target model (empty = provider default).
			promptRec.Metadata.Role.ResolvedModel = o.RoleModel
		} else if o.RoleModel != "" {
			promptRec.Metadata.Role.ResolvedModel = o.RoleModel
		}
	}
	// Refresh live standing instructions, but restore the role body exclusively
	// from the immutable template artifact pinned on the session.
	systemPrompt, _, err := m.buildRestoreSystemPrompt(ctx, promptRec, project)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: system prompt: %w", operation, rec.ID, err)
	}
	systemPromptFile, err := m.prepareSystemPromptFile(rec.ID, launchHarness, systemPrompt)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: system prompt file: %w", operation, rec.ID, err)
	}

	// Restore re-applies the host-resolved role model over the current project
	// config. For switch, use the ephemeral target-resolved model above.
	agentConfig := restoreAgentConfig(promptRec, project)
	// Rotate spawn capability on every relaunch so a terminated/killed session's
	// prior token cannot be reused after hash is rewritten.
	spawnToken, spawnHash, err := spawncred.Issue()
	if err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: spawn capability: %w", operation, rec.ID, err)
	}
	rec.Metadata.SpawnCapabilityHash = spawnHash
	rec.UpdatedAt = m.clock()
	if err := m.store.UpdateSession(ctx, rec); err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: persist spawn capability: %w", operation, rec.ID, err)
	}
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, spawnToken)
	m.augmentAgentRuntimeEnv(agent, env)
	if err := m.prepareWorkspace(ctx, agent, rec.ID, ws.Path, systemPrompt, systemPromptFile, agentConfig, env); err != nil {
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	var argv []string
	var delivery ports.PromptDeliveryStrategy
	var mode RestoreMode
	if o.ForceFresh {
		argv, delivery, mode, err = freshLaunchArgv(ctx, agent, rec.ID, ws.Path, rec.Metadata,
			systemPrompt, systemPromptFile, agentConfig, rec.Kind, m.dataDir, true,
			rec.Metadata.Role.ResolvedPermissions)
	} else {
		// For switch pending target, RO policy still follows pinned role permissions.
		argv, delivery, mode, err = restoreArgv(ctx, agent, rec.ID, ws.Path, rec.Metadata,
			systemPrompt, systemPromptFile, agentConfig, rec.Kind, launchHarness, m.dataDir,
			rec.Metadata.Role.ResolvedPermissions)
	}
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	// A fresh fallback with a saved prompt is still only an attempt. Its public
	// mode is promoted after the selected delivery strategy succeeds below.
	replaysSavedPrompt := mode == RestoreModeFresh && rec.Metadata.Prompt != ""
	if err := m.validateAgentBinary(argv); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: %w", operation, rec.ID, err)
	}
	m.augmentRuntimePATHForLaunchBinary(ctx, env, argv)
	argv, launchID, err := m.superviseAgentProcess(agent, rec.ID, env, argv, o.ForceLaunchID)
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: supervisor: %w", operation, rec.ID, err)
	}
	if err := m.lcm.PrepareLaunch(rec.ID, launchID); err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: prepare launch: %w", operation, rec.ID, err)
	}
	defer m.lcm.CancelLaunch(rec.ID, launchID)
	runtimeCfg := ports.RuntimeConfig{
		SessionID:     rec.ID,
		WorkspacePath: ws.Path,
		Argv:          argv,
		Env:           env,
	}
	var handle ports.RuntimeHandle
	if restartHandle == nil {
		handle, err = m.runtime.Create(ctx, runtimeCfg)
	} else {
		handle, err = m.restartRuntime(ctx, *restartHandle, runtimeCfg)
	}
	if err != nil {
		m.cleanupSystemPromptDir(rec.ID)
		return RestoreResult{}, fmt.Errorf("%s %s: runtime: %w", operation, rec.ID, err)
	}
	metadata := domain.SessionMetadata{
		Branch:            ws.Branch,
		WorkspacePath:     ws.Path,
		WorkspaceRepoPath: ws.RepoPath,
		RuntimeHandleID:   handle.ID,
		RuntimeLaunchID:   launchID,
		AgentSessionID:    rec.Metadata.AgentSessionID,
		Prompt:            rec.Metadata.Prompt,
	}
	if err := m.lcm.MarkSpawned(ctx, rec.ID, metadata); err != nil {
		cleanupErr := m.parkFailedRelaunch(ctx, operation, rec.ID, handle, launchID, o.KeepSessionOnLaunchFailure)
		// Joined, not replaced: the caller needs the launch failure (which may be
		// ErrActiveOrchestratorExists) AND the fact that rollback left something
		// AO cannot describe.
		return RestoreResult{}, fmt.Errorf("%s %s: completed: %w", operation, rec.ID, errors.Join(err, cleanupErr))
	}
	if replaysSavedPrompt && delivery != ports.PromptDeliveryAfterStart {
		// In-command/custom delivery is owned by the launched process. Fence the
		// adoption with a generation-scoped supervisor probe so a process that died
		// during launch cannot be reported idle with a successfully replayed task.
		// A failed/unsupported probe is not proof of death and preserves the prior
		// behavior; only a confirmed dead workload fails the relaunch.
		if err := m.confirmCommandDeliveredAssignment(ctx, handle, rec.ID, launchID); err != nil {
			cleanupErr := m.parkFailedRelaunch(ctx, operation, rec.ID, handle, launchID, false)
			return RestoreResult{}, relaunchAssignmentFailure(
				operation,
				rec.ID,
				"the original assignment was included in the launch command, but the agent exited during startup before AO could confirm it accepted the task",
				"Resolve why the agent exits during startup before restarting; an unchanged retry will end before the task can be confirmed",
				err,
				cleanupErr,
			)
		}
		mode = RestoreModeSavedPrompt
	}
	if replaysSavedPrompt && delivery == ports.PromptDeliveryAfterStart {
		launchCfg := ports.LaunchConfig{
			DataDir:          m.dataDir,
			SessionID:        string(rec.ID),
			WorkspacePath:    ws.Path,
			Kind:             rec.Kind,
			Prompt:           rec.Metadata.Prompt,
			SystemPrompt:     systemPrompt,
			SystemPromptFile: systemPromptFile,
			Config:           agentConfig,
			Permissions:      agentConfig.Permissions,
		}
		if err := m.deliverAfterStartPrompt(ctx, agent, launchCfg, handle, rec.ID, launchID, rec.Metadata.Prompt); err != nil {
			// Always terminates, switch included: the launch WAS adopted here, so
			// the row already names the runtime and there is no post-stop state to
			// preserve. Matches what this branch has always done.
			cleanupErr := m.parkFailedRelaunch(ctx, operation, rec.ID, handle, launchID, false)
			return RestoreResult{}, relaunchAssignmentFailure(
				operation,
				rec.ID,
				"AO could not confirm that the original assignment was delivered, so the task must be treated as unsent",
				"Resolve the reported prompt-readiness or pane-delivery failure before restarting; an unchanged retry will fail before the task is sent",
				err,
				cleanupErr,
			)
		}
		mode = RestoreModeSavedPrompt
	}
	updated, err := m.getRecord(ctx, rec.ID)
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Session: updated, Mode: mode}, nil
}

func relaunchAssignmentFailure(operation string, id domain.SessionID, taskOutcome, retryAction string, cause, cleanupErr error) error {
	sessionOutcome := "the relaunched process was stopped and the session was parked as terminated for a later restore"
	if cleanupErr != nil {
		sessionOutcome = "AO attempted to stop the relaunched process and park the session, but cleanup was not fully confirmed; the joined cleanup error describes the remaining runtime or session state"
	}
	return fmt.Errorf("%s %s: %s; %s. %s: %w",
		operation, id, taskOutcome, sessionOutcome, retryAction, errors.Join(cause, cleanupErr))
}

// relaunchSessionFresh bypasses native resume on both sides, so an empty
// conversation cannot fail startup (or rollback) with "No conversation found".
func (m *Manager) relaunchSessionFresh(ctx context.Context, operation string, rec domain.SessionRecord, project domain.ProjectRecord, ws ports.WorkspaceInfo, restartHandle *ports.RuntimeHandle) (RestoreResult, error) {
	return m.relaunchSession(ctx, operation, rec, project, ws, restartHandle, relaunchOpts{ForceFresh: true})
}

// parkFailedRelaunch unwinds a relaunch (restore, resume agent, switch) that
// created a runtime it could not complete, leaving the row honest about what is
// executing. Unlike the spawn rollback it NEVER touches the workspace: the
// worktree predates this launch, and for an orchestrator it is the canonical
// per-project one the survivor owns.
//
// The failure this exists for is ResumeAgentWithMode losing at MarkSpawned. The
// row there is already ACTIVE and still carries the previous
// RuntimeHandleID — which the teardown below may have just killed — so doing
// nothing leaves a session that reads as live with no process behind it. For an
// orchestrator that row also holds the project's single active slot (migration
// 0046), so no replacement can be spawned: the project wedges.
//
// Terminating is uniform across both probe outcomes, and deliberately so. When
// death is confirmed there is nothing to be active about. When it is not, the
// runtime is an orphan of a launch whose lifecycle bookkeeping never completed —
// its identity has just been recorded (reapFailedLaunchRuntime), so reconcile
// and the boot reaper can still find it, and leaving the session "active" would
// only invite interaction with a half-launched agent. Either way the
// orchestrator slot is freed. Recovery is Restore, which needs Branch and
// WorkspacePath — both preserved.
//
// keepSession is the one exception, and it belongs to the switch saga alone:
// see relaunchOpts.KeepSessionOnLaunchFailure. Reaping the runtime is not
// optional even then — only the row's terminal state is.
//
// Returns ErrLaunchCleanupUnresolved when a compensating write failed, which
// the caller must join into the error it surfaces. The ORDER below is the
// contract: identity is cleared only after termination has actually persisted.
// Clearing it on a row that is still active would leave an orchestrator holding
// the project's only slot with an empty handle — occupying it while runtime
// reconciliation, which works from recorded handles, skips it entirely.
func (m *Manager) parkFailedRelaunch(ctx context.Context, operation string, id domain.SessionID, handle ports.RuntimeHandle, launchID string, keepSession bool) error {
	confirmedDead, cleanupErr := m.reapFailedLaunchRuntime(ctx, operation, id, handle, launchID)
	m.cleanupSystemPromptDir(id)
	if keepSession {
		return cleanupErr
	}
	if err := m.lcm.MarkTerminated(ctx, id); err != nil {
		m.logger.Error("failed relaunch: could not mark session terminated; leaving runtime identity in place",
			"operation", operation, "sessionID", id, "error", err)
		return errors.Join(cleanupErr, fmt.Errorf("%w: session %s could not be terminated after a failed %s: %w",
			ErrLaunchCleanupUnresolved, id, operation, err))
	}
	if confirmedDead {
		// Best-effort by contrast with the two writes above, and safely so: a
		// terminated row naming a dead handle just costs one redundant probe,
		// whereas both failures above break an invariant.
		m.clearStaleRuntimeIdentity(ctx, id)
	}
	return cleanupErr
}

func (m *Manager) restartRuntime(ctx context.Context, handle ports.RuntimeHandle, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		// ErrRuntimeUnavailable is deliberately broader than authoritative
		// absence: the tmux adapter also uses it for permission failures and
		// stale/unreachable sockets. Only (false, nil) proves the old runtime is
		// gone and permits a replacement launch.
		return ports.RuntimeHandle{}, fmt.Errorf("probe existing runtime: %w", err)
	}
	if alive {
		if restarter, ok := m.runtime.(ports.RuntimeRestarter); ok {
			return restarter.Restart(ctx, handle, cfg)
		}
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			return ports.RuntimeHandle{}, fmt.Errorf("destroy existing runtime: %w", err)
		}
	}
	return m.runtime.Create(ctx, cfg)
}

func (m *Manager) getRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, ErrNotFound)
	}
	return rec, nil
}

// SaveAndTeardownAll captures uncommitted work and tears down every live
// session that has a workspace path. It is the shutdown path for the daemon:
// each session's uncommitted work is stashed into a preserve ref, the ref is
// written to session_worktrees (the "shutdown-saved" marker) BEFORE the
// worktree is force-removed. The DB write is committed before the worktree is
// destroyed so a crash between the two leaves the ref in place and the row
// present; RestoreAll will replay both.
//
// Failures on individual sessions are logged and do not abort the loop.
// ForceDestroy is never called if capture or the DB write did not succeed.
func (m *Manager) SaveAndTeardownAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("save-teardown-all: list sessions: %w", err)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if rec.Metadata.WorkspacePath == "" || rec.Metadata.Branch == "" {
			continue
		}
		if err := m.saveAndTeardownOne(ctx, rec, true); err != nil {
			m.logger.Error("save-teardown-all: session failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

// saveAndTeardownOne runs the capture-then-destroy sequence for a single
// session. The DB write (UpsertSessionWorktree) is committed before
// ForceDestroy; if either capture or the DB write fails, ForceDestroy is
// not called.
func (m *Manager) saveAndTeardownOne(ctx context.Context, rec domain.SessionRecord, destroyRuntime bool) error {
	// Gate shut this session's scoped shell terminals before either branch
	// below force-removes its worktree. Both SaveAndTeardownAll and
	// reconcileLive only reach here for a session with a real workspace, so
	// there is always a worktree to protect.
	release, closeErr := m.beginShellTerminalTeardown(ctx, rec.ID)
	if closeErr != nil {
		return fmt.Errorf("save %s: %w", rec.ID, closeErr)
	}
	if release != nil {
		defer release()
	}

	if rows, ok, err := m.workspaceProjectRows(ctx, rec); err != nil {
		return fmt.Errorf("save %s: workspace rows: %w", rec.ID, err)
	} else if ok {
		return m.saveAndTeardownWorkspaceProject(ctx, rec, rows, destroyRuntime)
	}

	// 1. Capture uncommitted work (ref may be "" for clean worktrees).
	ws := workspaceInfo(rec)
	ref, err := m.workspace.StashUncommitted(ctx, ws)
	if err != nil {
		return fmt.Errorf("save %s: stash: %w", rec.ID, err)
	}

	// 2. Write the shutdown-saved marker to the DB. The row's presence (even
	// with an empty preserved_ref) is what RestoreAll uses to identify sessions
	// saved by this run. This MUST be committed before ForceDestroy.
	row := domain.SessionWorktreeRecord{
		SessionID:    rec.ID,
		RepoName:     domain.RootWorkspaceRepoName,
		Branch:       rec.Metadata.Branch,
		WorktreePath: rec.Metadata.WorkspacePath,
		PreservedRef: ref,
		State:        "removed",
	}
	if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
		return fmt.Errorf("save %s: upsert worktree row: %w", rec.ID, err)
	}

	// 3. Mark terminal via the LCM (same path Kill uses).
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}

	// 4. Runtime teardown (best-effort; same pattern as Kill).
	handle := runtimeHandle(rec.Metadata)
	if destroyRuntime && handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}

	// 5. Force-remove the worktree (safe: work is captured in step 1 and the
	// DB write in step 2 is already committed).
	if err := m.workspace.ForceDestroy(ctx, ws); err != nil {
		m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "error", err)
	} else {
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	}
	return nil
}

// reconcileLive handles a single non-terminated session on boot. If its runtime
// session is still alive (tmux is the persistence layer, so it survives a daemon
// crash) we adopt it: a no-op, the agent keeps running. If the runtime is gone,
// the agent died with the daemon, so we save-and-tear-down to the SAME end state
// a graceful shutdown produces: capture uncommitted work into a preserve ref,
// record the session_worktrees restore marker, mark terminated, and remove the
// worktree. RestoreAll (which Reconcile runs immediately after) then relaunches
// it on this same boot, resuming history. Crash recovery thus matches graceful
// restart instead of silently abandoning the session.
//
// If the work capture fails we mark terminated WITHOUT a marker and leave the
// worktree intact: better to skip the relaunch than to tear down un-preserved
// work or relaunch onto an inconsistent worktree.
func (m *Manager) reconcileLive(ctx context.Context, rec domain.SessionRecord) error {
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return err
	}
	projectKind := project.Kind.WithDefault()
	if rec.Metadata.WorkspacePath == "" || (rec.Metadata.Branch == "" && projectKind != domain.ProjectKindScratch) {
		return nil
	}
	// Defense in depth: never tear down a session waiting on post_stop recovery
	// (recover pass should have handled it; if it failed, leave for next boot).
	if incomplete, err := m.hasIncompletePostStop(ctx, rec.ID); err != nil {
		return fmt.Errorf("reconcile %s: ledger: %w", rec.ID, err)
	} else if incomplete {
		m.logger.Warn("reconcile: leaving incomplete post_stop switch for recovery", "sessionID", rec.ID)
		return nil
	}
	handle := runtimeHandle(rec.Metadata)
	probedDead := false
	// A chat controller is an in-process child of the daemon, so unlike tmux it
	// can never have survived the crash: there is nothing to adopt and nothing
	// to probe. It falls through to the same save-and-teardown a dead runtime
	// gets, which is what records the restore marker RestoreAll needs.
	//
	// probedDead therefore stays FALSE for chat. It means "a probe confirmed
	// death", and pause liveness keys on it — a session that was never probed
	// must not be reported as confirmed dead.
	isChat := domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
	if !isChat {
		if handle.ID != "" {
			alive, err := m.runtime.IsAlive(ctx, handle)
			if err != nil {
				// A failed probe is not proof of death. The runtime adapter returns
				// (false, nil) for authoritative absence; every error, including
				// ErrRuntimeUnavailable, leaves the session untouched.
				return fmt.Errorf("reconcile %s: probe: %w", rec.ID, err)
			}
			if alive {
				return nil // adopt: the session survived the crash.
			}
			probedDead = true
		}
	}
	// Past this point the runtime is confirmed dead and the session is torn
	// down WITH a shutdown-saved marker — which RestoreAll then relaunches from,
	// in this same boot. For a paused session that is the automatic restart
	// rule 2 forbids, so the teardown is skipped.
	if m.pausedSkip(rec, "save-and-teardown of a dead runtime") {
		// But skipping the teardown must not also skip the OBSERVATION. We just
		// proved the process is gone; leaving the pre-crash activity in place
		// would let the row serialize as paused AND working, and the pause
		// contract requires the UI to tell paused-and-running from
		// paused-and-dead so it knows to offer "Restart agent" instead of
		// "Resume" (PHASE3A_PAUSE_CONTRACT §2). Recording it is not a teardown:
		// no marker is written, the row stays active, the pin is untouched, and
		// nothing relaunches.
		if probedDead {
			return m.recordConfirmedExit(ctx, rec)
		}
		return nil
	}
	if projectKind == domain.ProjectKindScratch {
		return m.lcm.MarkTerminated(ctx, rec.ID)
	}
	if err := m.saveAndTeardownOne(ctx, rec, false); err != nil {
		m.logger.Warn("reconcile: save-and-teardown failed; terminating without restore marker", "sessionID", rec.ID, "error", err)
		if mErr := m.lcm.MarkTerminated(ctx, rec.ID); mErr != nil {
			return fmt.Errorf("reconcile %s: mark terminated: %w", rec.ID, mErr)
		}
	}
	return nil
}

// reconcileReap kills the leaked tmux session of a session the DB already marks
// terminated. This covers the teardown that marked the row terminated but failed
// to kill the runtime (e.g. ForceDestroy/Destroy errored after MarkTerminated).
// Destroy is idempotent, so an already-gone session is a no-op. Probe failures
// are boot-unsafe rather than ordinary per-row failures: this pass exists to
// establish that RestoreAll cannot launch beside an old runtime, and an error
// establishes nothing. ErrRuntimeUnavailable is intentionally included because
// it covers both absence-like and reachability failures; authoritative adapter
// absence is expressed only as (false, nil).
func (m *Manager) reconcileReap(ctx context.Context, rec domain.SessionRecord) error {
	handle := runtimeHandle(rec.Metadata)
	if handle.ID == "" {
		return nil
	}
	alive, err := m.runtime.IsAlive(ctx, handle)
	if err != nil {
		return fmt.Errorf("%w: session %s probe: %w", ErrRuntimeReapUnresolved, rec.ID, err)
	}
	if !alive {
		return nil
	}
	if err := m.runtime.Destroy(ctx, handle); err != nil {
		return fmt.Errorf("%w: session %s destroy: %w", ErrRuntimeReapUnresolved, rec.ID, err)
	}
	return nil
}

// Reconcile is the boot-time consistency pass. It replaces the bare RestoreAll
// call so that however the previous daemon died (clean shutdown, SIGKILL, or
// crash), live reality matches the DB:
//
//  1. Post-stop recovery: re-drive incomplete worker switch/fresh sagas that
//     reached post_stop (handoff retained) before target_ack. Must run before
//     the live pass so a dead runtime mid-switch is not torn down as a normal
//     crash (which would drop the recoverable handoff path).
//  2. Live pass: for each non-terminated session, adopt it if its runtime
//     survived, else capture work and mark terminated (reconcileLive).
//  3. Reap pass: for each terminated session whose runtime leaked, kill it
//     (reconcileReap). Runs before restore so a restored session does not
//     collide with a leaked tmux of the same name.
//  4. Restore pass: relaunch shutdown-saved sessions (existing RestoreAll).
//
// Best-effort passes continue collecting safe work, but every ErrBootUnsafe
// child is returned before RestoreAll. That ordering is load-bearing: a
// terminated row whose old runtime could not be disproved must not be relaunched
// beside it. RestoreAll's own boot-unsafe outcomes are still returned after the
// restore pass because they arise during that pass. The daemon treats either
// return as FATAL, ahead of every client-facing surface (daemon.go, pinned by
// boot_order_test.go).
func (m *Manager) Reconcile(ctx context.Context) error {
	m.startTransitionMessageDispatcher(ctx)
	_, err := m.recoverInterruptedInterfaceTransitions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: interface transitions: %w", err)
	}
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile: list sessions: %w", err)
	}
	var unresolved []error
	for _, rec := range recs {
		// Orchestrators included since 2B-1: an orchestrator whose fresh
		// conversation crashed post-stop has a stopped source and no target,
		// and skipping it here left the project with no coordinator until
		// someone noticed. RecoverSwitchFromPostStop takes the project gate
		// itself for those.
		if rec.IsTerminated || (rec.Kind != domain.KindWorker && rec.Kind != domain.KindOrchestrator) {
			continue
		}
		// Recovery LAUNCHES the switch target. On a paused session that is an
		// automatic restart, so the incomplete saga is left exactly as it is
		// and re-driven after an explicit resume. The handoff is durable, so
		// nothing is lost by waiting.
		if m.pausedSkip(rec, "post_stop switch recovery") {
			continue
		}
		if _, err := m.RecoverSwitchFromPostStop(ctx, rec.ID); err != nil {
			if errors.Is(err, ErrSwitchNothingToRecover) || errors.Is(err, ErrSwitchInProgress) {
				continue
			}
			if errors.Is(err, ErrLaunchCleanupUnresolved) {
				// This is the LAST place the condition is visible. Recovery
				// keeps the row ACTIVE on purpose (KeepSessionOnLaunchFailure —
				// a terminated session is unrecoverable), so the live pass below
				// skips it as switch-pending and RestoreAll only walks
				// terminated rows. Neither downstream collector can ever see it.
				m.logger.Error("reconcile: post_stop recovery left an unresolved runtime",
					"sessionID", rec.ID, "error", err)
				unresolved = append(unresolved, err)
				continue
			}
			m.logger.Error("reconcile: post_stop recovery failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	// Re-list: recovery may have updated harness/runtime handles.
	recs, err = m.store.ListAllSessions(ctx)
	if err != nil {
		// Joined, not returned bare: anything already collected above describes
		// a runtime that is executing right now, and a caller gating on
		// ErrLaunchCleanupUnresolved would miss it behind a plain store error.
		return errors.Join(append(unresolved, fmt.Errorf("reconcile: re-list sessions: %w", err))...)
	}
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if err := m.reconcileLive(ctx, rec); err != nil {
			// Boot-unsafe outcomes are COLLECTED, not merely logged: this loop
			// used to log everything, so an ErrBootUnsafe raised here could never
			// reach Reconcile's return and the daemon served anyway. The test is
			// on the error, not on the pass, so any future boot-unsafe condition
			// in the live pass is carried automatically.
			if errors.Is(err, ErrBootUnsafe) {
				m.logger.Error("reconcile: live pass left boot unsafe", "sessionID", rec.ID, "error", err)
				unresolved = append(unresolved, err)
				continue
			}
			m.logger.Error("reconcile: live pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		if err := m.reconcileReap(ctx, rec); err != nil {
			// Same rule as the live pass above, for the same reason.
			if errors.Is(err, ErrBootUnsafe) {
				m.logger.Error("reconcile: reap pass left boot unsafe", "sessionID", rec.ID, "error", err)
				unresolved = append(unresolved, err)
				continue
			}
			m.logger.Error("reconcile: reap pass failed, skipping", "sessionID", rec.ID, "error", err)
		}
	}
	// Restore is the first pass that launches terminated rows. Any boot-unsafe
	// finding collected before this point means the precondition for launching
	// them was not established. Continue the earlier per-row passes so unrelated
	// cleanup can make progress, then stop here before workspace adoption or
	// runtime creation.
	if len(unresolved) > 0 {
		return errors.Join(unresolved...)
	}
	// RestoreAll can itself discover a boot-unsafe condition while the restore
	// pass is running. Surface it unchanged; every pre-restore finding already
	// returned above.
	if err := m.RestoreAll(ctx); err != nil {
		return err
	}
	// Upstream's transition outbox runs only on a boot that reached here
	// cleanly. Delivery failure is deliberately non-fatal — the durable outbox
	// retries — but it must not run at all on an unsafe boot, which returns
	// above.
	if err := m.deliverAllTransitionMessages(ctx); err != nil {
		m.logger.Error("reconcile: transition-message delivery deferred for retry", "error", err)
	}
	m.wakeTransitionMessageDispatcher()
	return nil
}

// RestoreAll relaunches every terminated session that was saved by the last
// SaveAndTeardownAll. The "shutdown-saved" marker is the presence of a
// session_worktrees row for the session; sessions the user killed before
// shutdown have no such row and are left terminated.
//
// For each saved session:
//  1. Ensure the worktree exists via workspace.Restore.
//  2. If a preserve ref is recorded, replay it via ApplyPreserved; on conflict
//     log and continue (still relaunch the agent, never delete the ref).
//  3. Relaunch via the existing Restore method.
//
// Failures on individual sessions are logged and do not abort the loop, with
// ONE exception: a relaunch that left an unresolved runtime
// (ErrLaunchCleanupUnresolved) is collected and returned. Skipping it would be
// wrong in a way the others are not — Reconcile's terminated-session reap pass
// runs BEFORE this loop, so a runtime that outlived a failed restore is
// executing in the session's workspace with nothing scheduled to sweep it until
// the next boot. Returning it is what lets boot refuse to serve, which the
// daemon now does before exposing any client-facing surface.
//
// Phase 2B gap (tracked as 2B-0b in docs/roles/PHASE2B_PLAN.md): unlike
// RestoreWithMode, this loop restores workspaces directly and does NOT take the
// project ownership gate, so it can resurrect more than one orchestrator per
// project. Gating alone would not fix that — the loop also needs deterministic
// survivor selection and restore-marker neutralization, which land together
// with migration 0057. It runs at boot before the daemon serves, so it does not
// currently race API-driven restores.
func (m *Manager) RestoreAll(ctx context.Context) error {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("restore-all: list sessions: %w", err)
	}
	// Orchestrators first, and under the project ownership gate: they carry a
	// per-project cardinality rule that workers do not, and they all share one
	// canonical worktree. Workers follow, unordered and ungated as before.
	unresolved := m.restoreProjectOrchestrators(ctx, recs)
	for _, rec := range recs {
		if !rec.IsTerminated || rec.Kind == domain.KindOrchestrator {
			continue
		}
		if err := m.restoreSavedSession(ctx, rec); err != nil {
			unresolved = append(unresolved, err)
		}
	}
	// Every other session was still restored: an unresolved runtime on one is a
	// reason not to SERVE, not a reason to abandon the rest of the pass.
	return errors.Join(unresolved...)
}

// restoreProjectOrchestrators restores at most ONE orchestrator per project,
// under that project's ownership gate.
//
// This is the half of boot restore that migration 0057 could not cover. The
// migration reconciles rows that were already duplicated; this prevents the
// loop from *creating* duplicates in the first place, which it previously could
// in two distinct ways:
//
//   - restoring several terminated orchestrators that all carry shutdown-saved
//     markers, each flipping to active on the same canonical worktree; and
//   - restoring a terminated orchestrator when a live one already owns the
//     project, e.g. one Reconcile just adopted across a daemon restart.
//
// The unique index would reject the second row eventually, but not before
// workspace.Restore has already adopted the shared worktree — the damage lands
// before any constraint is reached, which is exactly why the gate exists and
// the index alone is not enough.
//
// Losing candidates have their restore markers NEUTRALIZED rather than left in
// place, mirroring what 0046 does to the losers it reconciles: a marker that
// survives is retried on every subsequent boot, so leaving it would make this a
// permanent source of duplicate-restore attempts. Only the marker row is
// removed; the preserved ref itself is never deleted, so the stranded work
// stays recoverable by hand.
func (m *Manager) restoreProjectOrchestrators(ctx context.Context, recs []domain.SessionRecord) []error {
	// This pre-gate snapshot decides WHICH PROJECTS to look at, and nothing
	// else. Every record the election and the relaunch use is re-read under the
	// project's own gate; a stale row here can only cost a redundant gated
	// lookup, never a decision.
	seen := map[domain.ProjectID]struct{}{}
	projects := make([]domain.ProjectID, 0, 4)
	for _, rec := range recs {
		if rec.Kind != domain.KindOrchestrator || !rec.IsTerminated {
			continue
		}
		if _, dup := seen[rec.ProjectID]; dup {
			continue
		}
		seen[rec.ProjectID] = struct{}{}
		projects = append(projects, rec.ProjectID)
	}
	// Sort so a multi-project boot is reproducible regardless of list order.
	sort.Slice(projects, func(i, j int) bool { return projects[i] < projects[j] })

	var unresolved []error
	for _, projectID := range projects {
		if err := m.restoreOneOrchestrator(ctx, projectID); err != nil {
			unresolved = append(unresolved, err)
		}
	}
	return unresolved
}

// restoreOneOrchestrator holds projectID's ownership gate across BOTH the
// survivor decision and the restore itself. Splitting them would reintroduce
// the race the gate exists to close: the "is anyone active?" answer must still
// be true when the row flips.
func (m *Manager) restoreOneOrchestrator(ctx context.Context, projectID domain.ProjectID) error {
	release, err := m.acquireProjectOwnership(ctx, projectID)
	if err != nil {
		m.logger.Error("restore-all: orchestrator ownership gate unavailable",
			"projectID", projectID, "error", err)
		return nil
	}
	defer release()

	// Re-read under the gate, and derive EVERYTHING from this read. The caller's
	// snapshot only identified which projects to look at; using its records to
	// elect or to relaunch would decide from rows a competing gated operation
	// may already have terminated, retired, or reparented.
	live, err := m.store.ListSessions(ctx, projectID)
	if err != nil {
		// Same policy as an unreadable marker below: without this read there may
		// be an un-neutralized predecessor we never even saw.
		m.logger.Error("restore-all: list project sessions failed", "projectID", projectID, "error", err)
		return fmt.Errorf("%w: project %s sessions unreadable: %w",
			ErrOrchestratorEvidenceUnresolved, projectID, err)
	}
	activeOwner := domain.SessionID("")
	candidates := make([]domain.SessionRecord, 0, len(live))
	for _, rec := range live {
		if rec.Kind != domain.KindOrchestrator {
			continue
		}
		if rec.IsTerminated {
			candidates = append(candidates, rec)
			continue
		}
		activeOwner = rec.ID
	}

	// Only candidates that still carry a restorable marker can win, or the
	// election could pick a row with nothing to restore from.
	//
	// A marker LOOKUP FAILURE is not "no marker". Treating them alike lets a
	// failed read on the newest candidate silently promote an older one, which
	// then adopts the shared canonical worktree and replays older preserved
	// state — a wrong winner chosen from incomplete evidence, and unlike a
	// duplicate it is not something a later boot corrects.
	//
	// Abandoning the election is necessary but NOT sufficient, so this is
	// boot-fatal rather than a skip. An unread marker may belong to a
	// predecessor that should have been neutralized: it survives, and it is
	// eligible. Kill the current owner and the next boot — once the read
	// recovers — restores that predecessor. That is the same delayed
	// resurrection an undeletable loser marker causes, reached by not having
	// looked rather than by having failed to delete, so it gets the same answer.
	restorable := make([]domain.SessionRecord, 0, len(candidates))
	for _, rec := range candidates {
		// A paused candidate does not take part in the election AT ALL — it is
		// neither the survivor nor a loser. That distinction is the whole point:
		// losers get their markers neutralized, and neutralizing a paused
		// orchestrator's marker would permanently destroy the restorability a
		// later resume depends on. Excluding it here also means it can never be
		// elected and relaunched, which is the rule-2 violation.
		if m.pausedSkip(rec, "orchestrator survivor election") {
			continue
		}
		rows, err := m.restorableMarkers(ctx, rec.ID)
		if err != nil {
			m.logger.Error("restore-all: abandoning orchestrator election on an unreadable marker",
				"projectID", projectID, "sessionID", rec.ID, "error", err)
			return fmt.Errorf("%w: project %s candidate %s: %w",
				ErrOrchestratorEvidenceUnresolved, projectID, rec.ID, err)
		}
		if len(rows) > 0 {
			restorable = append(restorable, rec)
		}
	}
	if len(restorable) == 0 {
		return nil
	}

	if activeOwner != "" {
		// The slot is taken. Every candidate is a loser.
		m.logger.Warn("restore-all: project already has a live orchestrator; dropping saved restores",
			"projectID", projectID, "owner", activeOwner, "dropped", len(restorable))
		return m.neutralizeRestoreMarkers(ctx, restorable, "a live orchestrator already owns the project")
	}

	// Same rule as newestOrchestratorRecord and migration 0057: newest
	// CreatedAt, then UpdatedAt, then lexically greatest id. The database and
	// this loop must never disagree about who owns a project.
	survivor := newestOrchestratorRecord(restorable)
	losers := make([]domain.SessionRecord, 0, len(restorable))
	for _, rec := range restorable {
		if rec.ID != survivor.ID {
			losers = append(losers, rec)
		}
	}
	// Neutralize BEFORE restoring, and only restore if it stuck.
	if err := m.neutralizeRestoreMarkers(ctx, losers, "lost the orchestrator survivor election"); err != nil {
		return err
	}
	return m.restoreSavedSession(ctx, survivor)
}

// neutralizeRestoreMarkers drops losing candidates' shutdown-saved markers, and
// is a DURABLE PRECONDITION of restoring the winner rather than best-effort.
//
// A surviving loser marker is not inert. It stays eligible, and it does not
// stay a loser: the moment the current owner is killed — or otherwise loses its
// own marker — that stale row becomes the only restorable orchestrator for the
// project and a later boot resurrects the very session this election
// superseded. So a failure here is boot-fatal (ErrBootUnsafe) rather than
// logged: the daemon's reconcile gate is otherwise non-fatal, and continuing
// would leave a durable row that AO will later act on as if it were current.
func (m *Manager) neutralizeRestoreMarkers(ctx context.Context, losers []domain.SessionRecord, why string) error {
	var failed []error
	for _, rec := range losers {
		m.logger.Warn("restore-all: neutralizing orchestrator restore marker",
			"sessionID", rec.ID, "reason", why)
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			m.logger.Error("restore-all: neutralize restore marker failed", "sessionID", rec.ID, "error", err)
			failed = append(failed, fmt.Errorf("%w: session %s (%s): %w",
				ErrRestoreMarkerUnresolved, rec.ID, why, err))
		}
	}
	return errors.Join(failed...)
}

// restorableMarkers reports the session's shutdown-saved marker rows. An empty
// slice with a nil error means the session was killed before shutdown and is
// deliberately left terminated; a non-nil error means the lookup itself failed
// and the caller has NO evidence either way. The two are kept distinct because
// callers that elect between sessions must not read a failure as an absence.
func (m *Manager) restorableMarkers(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list worktrees for %s: %w", id, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	rows = restorableWorktreeRows(rows)
	if len(rows) == 0 {
		return nil, nil
	}
	return rows, nil
}

// restoreSavedSession restores one shutdown-saved session: ensure the worktree,
// replay any preserve ref, relaunch, then drop the consumed marker.
//
// Per-session failures are logged and returned as nil — boot restores what it
// can. The ONE exception is ErrLaunchCleanupUnresolved, which is returned so
// the caller can refuse to serve; see RestoreAll.
func (m *Manager) restoreSavedSession(ctx context.Context, rec domain.SessionRecord) error {
	// The last gate before an agent is relaunched, and the one every restore
	// path funnels through — the worker loop and the elected orchestrator both
	// end here. A session paused before a clean shutdown carries a marker like
	// any other, so without this the next boot simply starts it again.
	//
	// The marker is deliberately NOT neutralized: it is what makes the session
	// restorable once someone resumes it. An un-restored marker is inert.
	if m.pausedSkip(rec, "restore of a shutdown-saved session") {
		return nil
	}
	rows, err := m.restorableMarkers(ctx, rec.ID)
	if err != nil {
		// Single-session path: no election is riding on this, so an unreadable
		// marker only costs this one restore and the next boot retries it.
		m.logger.Error("restore-all: list worktrees failed", "sessionID", rec.ID, "error", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	// Step 1: ensure the worktree exists. workspace.Restore re-creates it
	// if it was removed by SaveAndTeardownAll.
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		m.logger.Error("restore-all: load project failed", "sessionID", rec.ID, "error", err)
		return nil
	}
	var ws ports.WorkspaceInfo
	restoredWorkspaceProject := project.Kind.WithDefault() == domain.ProjectKindWorkspace
	var projectRows []ports.WorkspaceRepoInfo
	if restoredWorkspaceProject {
		var rowErr error
		projectRows, rowErr = m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
		if rowErr != nil {
			m.logger.Error("restore-all: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
			return nil
		}
		root, restoreErr := m.restoreWorkspaceProjectRows(ctx, projectRows)
		if restoreErr != nil {
			m.logger.Error("restore-all: workspace project restore failed", "sessionID", rec.ID, "error", restoreErr)
			return nil
		}
		ws = workspaceInfoFromRepoInfo(root)
	} else {
		var restoreErr error
		ws, restoreErr = m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID:     rec.ProjectID,
			SessionID:     rec.ID,
			Kind:          rec.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        rec.Metadata.Branch,
			Path:          rec.Metadata.WorkspacePath,
		})
		if restoreErr != nil {
			m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", restoreErr)
			return nil
		}
	}
	if ws.Path == "" {
		m.logger.Error("restore-all: workspace restore failed", "sessionID", rec.ID, "error", "empty restored root path")
		return nil
	}

	// Step 2: replay preserve ref when one was recorded.
	if restoredWorkspaceProject {
		m.applyWorkspaceProjectPreserved(ctx, projectRows)
	} else {
		var preserveRef string
		for _, r := range rows {
			if r.PreservedRef != "" {
				preserveRef = r.PreservedRef
				break
			}
		}
		if preserveRef != "" {
			if applyErr := m.workspace.ApplyPreserved(ctx, ws, preserveRef); applyErr != nil {
				if errors.Is(applyErr, ports.ErrPreservedConflict) {
					m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
						"sessionID", rec.ID, "ref", preserveRef, "error", applyErr)
				} else {
					m.logger.Error("restore-all: apply preserved failed", "sessionID", rec.ID, "error", applyErr)
				}
				// Continue: always relaunch even on conflict (never delete the ref here).
			}
		}
	}

	// Step 3: relaunch the agent in the restored workspace.
	if _, err := m.relaunchRestoredSession(ctx, rec, project, ws); err != nil {
		switch {
		case errors.Is(err, ErrLaunchCleanupUnresolved):
			// NOT skippable. A failed restore whose runtime outlived
			// teardown is executing in the session's workspace right now,
			// and Reconcile's terminated-session reap pass has already run
			// (see step 3 of its own doc comment) — so nothing sweeps it
			// until the next boot. Returned rather than logged, so the
			// caller can refuse to serve.
			m.logger.Error("restore-all: relaunch left an unresolved runtime", "sessionID", rec.ID, "error", err)
			return err
		case errors.Is(err, ErrNotResumable):
			// A promptless, unresumable worker is intentionally left terminated:
			// expected, not an operational failure, so log it quietly.
			m.logger.Warn("restore-all: session left terminated (nothing to resume)", "sessionID", rec.ID)
		case errors.Is(err, ErrNotFound):
			// The row was reaped between listing and relaunch (a stale id during
			// reconciliation): skip it and keep restoring the rest.
			m.logger.Warn("restore-all: session vanished before relaunch, skipping", "sessionID", rec.ID)
		default:
			m.logger.Error("restore-all: relaunch failed", "sessionID", rec.ID, "error", err)
		}
		return nil
	}

	// One-shot: drop the consumed marker so it never outlives one restart
	// (#2319). A still-live session re-acquires it at the next quit.
	if restoredWorkspaceProject {
		for _, row := range projectRows {
			if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
				m.logger.Warn("restore-all: marking workspace repo active failed", "sessionID", rec.ID, "repo", row.RepoName, "error", err)
			}
		}
	} else {
		if err := m.markSessionWorktreesActive(ctx, rows); err != nil {
			m.logger.Warn("restore-all: marking worktrees active failed", "sessionID", rec.ID, "error", err)
		}
		if err := m.store.DeleteSessionWorktrees(ctx, rec.ID); err != nil {
			m.logger.Warn("restore-all: delete restore marker failed", "sessionID", rec.ID, "error", err)
		}
	}
	return nil
}

func restorableWorktreeRows(rows []domain.SessionWorktreeRecord) []domain.SessionWorktreeRecord {
	out := make([]domain.SessionWorktreeRecord, 0, len(rows))
	for _, row := range rows {
		if row.State == "removed" || legacyRestorableWorktreeRow(row) {
			out = append(out, row)
		}
	}
	return out
}

func legacyRestorableWorktreeRow(row domain.SessionWorktreeRecord) bool {
	return row.State == "" && (row.PreservedRef != "" || row.RepoName == domain.RootWorkspaceRepoName)
}

func (m *Manager) markSessionWorktreesActive(ctx context.Context, rows []domain.SessionWorktreeRecord) error {
	for _, row := range rows {
		row.State = "active"
		row.PreservedRef = ""
		if err := m.store.UpsertSessionWorktree(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) restoreSessionWorkspace(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) (ports.WorkspaceInfo, error) {
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID:     rec.ProjectID,
			SessionID:     rec.ID,
			Kind:          rec.Kind,
			SessionPrefix: sessionPrefix(project),
			Branch:        rec.Metadata.Branch,
			Path:          rec.Metadata.WorkspacePath,
		})
	}
	rows, err := m.workspaceProjectRestoreRows(ctx, project, rec)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	root, err := m.restoreWorkspaceProjectRows(ctx, rows)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	for _, row := range rows {
		if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("mark repo %s active: %w", row.RepoName, err)
		}
	}
	return workspaceInfoFromRepoInfo(root), nil
}

func (m *Manager) workspaceProjectRestoreRows(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	return m.workspaceProjectRestoreRowsFromMarkers(ctx, project, rec, rows)
}

func (m *Manager) workspaceProjectRestoreRowsFromMarkers(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	if len(rows) > 1 {
		return m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	}
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	rootPath := rec.Metadata.WorkspacePath
	rootBranch := rec.Metadata.Branch
	var rootBaseSHA string
	if len(rows) == 1 && (rows[0].RepoName == "" || rows[0].RepoName == domain.RootWorkspaceRepoName) {
		rootPath = firstNonEmptyString(rows[0].WorktreePath, rootPath)
		rootBranch = firstNonEmptyString(rows[0].Branch, rootBranch)
		rootBaseSHA = rows[0].BaseSHA
	}
	out := []ports.WorkspaceRepoInfo{{
		RepoName:  domain.RootWorkspaceRepoName,
		RepoPath:  project.Path,
		Path:      rootPath,
		Branch:    rootBranch,
		BaseSHA:   rootBaseSHA,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
	}}
	for _, repo := range childRepos {
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     repo.Name,
			RepoPath:     filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath)),
			Path:         filepath.Join(rootPath, filepath.FromSlash(repo.RelativePath)),
			Branch:       rootBranch,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: repo.RelativePath,
		})
	}
	return out, nil
}

func (m *Manager) workspaceProjectRows(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, false, err
	}
	if len(rows) <= 1 {
		return nil, false, nil
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return nil, false, nil
	}
	infos, err := m.sessionWorktreeRowsToRepoInfos(ctx, project, rec, rows)
	if err != nil {
		return nil, false, err
	}
	return infos, true, nil
}

func (m *Manager) sessionWorktreeRowsToRepoInfos(ctx context.Context, project domain.ProjectRecord, rec domain.SessionRecord, rows []domain.SessionWorktreeRecord) ([]ports.WorkspaceRepoInfo, error) {
	childRepos, err := m.store.ListWorkspaceRepos(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	repoPaths := map[string]string{domain.RootWorkspaceRepoName: project.Path}
	relPaths := map[string]string{}
	for _, repo := range childRepos {
		repoPaths[repo.Name] = filepath.Join(project.Path, filepath.FromSlash(repo.RelativePath))
		relPaths[repo.Name] = repo.RelativePath
	}
	out := make([]ports.WorkspaceRepoInfo, 0, len(rows))
	for _, row := range rows {
		repoPath := repoPaths[row.RepoName]
		if repoPath == "" {
			return nil, fmt.Errorf("session worktree row %q no longer matches workspace registry", row.RepoName)
		}
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:     row.RepoName,
			RepoPath:     repoPath,
			Path:         row.WorktreePath,
			Branch:       firstNonEmptyString(row.Branch, rec.Metadata.Branch),
			BaseSHA:      row.BaseSHA,
			SessionID:    rec.ID,
			ProjectID:    rec.ProjectID,
			RelativePath: relPaths[row.RepoName],
		})
	}
	return out, nil
}

func (m *Manager) saveAndTeardownWorkspaceProject(ctx context.Context, rec domain.SessionRecord, rows []ports.WorkspaceRepoInfo, destroyRuntime bool) error {
	for _, row := range rows {
		ref, err := m.workspace.StashUncommitted(ctx, workspaceInfoFromRepoInfo(row))
		if err != nil {
			return fmt.Errorf("save %s repo %s: stash: %w", rec.ID, row.RepoName, err)
		}
		if err := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    rec.ID,
			RepoName:     row.RepoName,
			Branch:       row.Branch,
			BaseSHA:      row.BaseSHA,
			WorktreePath: row.Path,
			PreservedRef: ref,
			State:        "removed",
		}); err != nil {
			return fmt.Errorf("save %s repo %s: upsert worktree row: %w", rec.ID, row.RepoName, err)
		}
	}
	if err := m.lcm.MarkTerminated(ctx, rec.ID); err != nil {
		return fmt.Errorf("save %s: mark terminated: %w", rec.ID, err)
	}
	handle := runtimeHandle(rec.Metadata)
	if destroyRuntime && handle.ID != "" {
		if err := m.runtime.Destroy(ctx, handle); err != nil {
			m.logger.Warn("save-teardown-all: runtime destroy failed", "sessionID", rec.ID, "error", err)
		}
	}
	rootDestroyed := false
	for i := len(rows) - 1; i >= 0; i-- {
		info := workspaceInfoFromRepoInfo(rows[i])
		if err := m.workspace.ForceDestroy(ctx, info); err != nil {
			m.logger.Warn("save-teardown-all: force destroy failed", "sessionID", rec.ID, "repo", rows[i].RepoName, "error", err)
		} else if info.Path == rec.Metadata.WorkspacePath {
			rootDestroyed = true
		}
	}
	if rootDestroyed {
		m.cleanupAgentWorkspace(ctx, rec, rec.Metadata.WorkspacePath)
	}
	return nil
}

func (m *Manager) destroyWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (bool, error) {
	cleaned := false
	var firstErr error
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Path == "" {
			continue
		}
		info := workspaceInfoFromRepoInfo(rows[i])
		if err := m.workspace.Destroy(ctx, info); err != nil {
			if errors.Is(err, ports.ErrWorkspaceDirty) {
				return cleaned, err
			}
			if stateErr := m.upsertWorkspaceProjectRowState(ctx, rows[i], "retry_remove"); stateErr != nil && firstErr == nil {
				firstErr = stateErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := m.upsertWorkspaceProjectRowState(ctx, rows[i], "unavailable"); err != nil && firstErr == nil {
			firstErr = err
		}
		cleaned = true
	}
	return cleaned, firstErr
}

func (m *Manager) upsertWorkspaceProjectRowState(ctx context.Context, row ports.WorkspaceRepoInfo, state string) error {
	return m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
		SessionID:    row.SessionID,
		RepoName:     row.RepoName,
		Branch:       row.Branch,
		BaseSHA:      row.BaseSHA,
		WorktreePath: row.Path,
		State:        state,
	})
}

func (m *Manager) restoreWorkspaceProjectRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) (ports.WorkspaceRepoInfo, error) {
	var root ports.WorkspaceRepoInfo
	for _, row := range rows {
		restored, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID: row.ProjectID,
			SessionID: row.SessionID,
			Branch:    row.Branch,
			RepoPath:  row.RepoPath,
			Path:      row.Path,
		})
		if err != nil {
			return ports.WorkspaceRepoInfo{}, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		row.Path = restored.Path
		row.Branch = restored.Branch
		if row.RepoName == domain.RootWorkspaceRepoName {
			root = row
		}
	}
	if root.Path == "" {
		return ports.WorkspaceRepoInfo{}, errors.New("workspace project root worktree row missing")
	}
	return root, nil
}

func (m *Manager) applyWorkspaceProjectPreserved(ctx context.Context, rows []ports.WorkspaceRepoInfo) {
	for _, row := range rows {
		var preserveRef string
		sessionRows, err := m.store.ListSessionWorktrees(ctx, row.SessionID)
		if err != nil {
			m.logger.Error("restore-all: list worktrees failed", "sessionID", row.SessionID, "error", err)
			continue
		}
		for _, sessionRow := range sessionRows {
			if sessionRow.RepoName == row.RepoName {
				preserveRef = sessionRow.PreservedRef
				break
			}
		}
		if preserveRef == "" {
			continue
		}
		if applyErr := m.workspace.ApplyPreserved(ctx, workspaceInfoFromRepoInfo(row), preserveRef); applyErr != nil {
			if errors.Is(applyErr, ports.ErrPreservedConflict) {
				m.logger.Warn("restore-all: apply preserved produced conflicts; agent relaunched with conflict markers in place",
					"sessionID", row.SessionID, "repo", row.RepoName, "ref", preserveRef, "error", applyErr)
			} else {
				m.logger.Error("restore-all: apply preserved failed", "sessionID", row.SessionID, "repo", row.RepoName, "error", applyErr)
			}
		}
	}
}

// Send delivers a message to a running session's agent through the guarded
// pane-write primitive, then best-effort confirms the agent actually accepted
// it. The guard refuses delivery into a session that is gone, terminated, has
// an exited agent, or is paused on a permission decision;
// those refusals surface as typed sentinels so the API reports why instead of
// silently dropping the message. AO has no delivery ack: the messenger returns
// nil the moment the runtime paste + Enter commands exit 0, and for a large
// multiline prompt a single Enter may not submit (claude-code leaves it as an
// unsubmitted draft). confirmActive observes the durable Activity.State
// (flipped to active by the user-prompt-submit hook) and re-sends Enter until
// the session is active or the budget is exhausted. Confirmation never fails
// the send: it only decides whether to nudge again.
func (m *Manager) Send(ctx context.Context, id domain.SessionID, message string) error {
	return m.send(ctx, id, message, "", sendOriginUser)
}

// send carries an optional idempotency key used by durable transition-message
// retries. Ordinary callers leave it empty; the outbox preserves the key across
// restart, rollback, and even a second overlapping handoff.
// sendOrigin says who initiated a chat write, mirroring sessionguard's
// writeOrigin for the path that does not pass through the guard.
type sendOrigin int

const (
	// sendOriginUser is a human's message. It is allowed while paused: pause
	// stops AO acting on its own, not a person talking to their agent.
	sendOriginUser sendOrigin = iota
	// sendOriginAuto is AO's own initiative, and is subject to the pause fence.
	sendOriginAuto
)

func (m *Manager) send(ctx context.Context, id domain.SessionID, message, clientMessageID string, origin sendOrigin) error {
	// A controller transition deliberately has a short interval with no writer.
	// Queue internal/lifecycle sends durably instead of racing either controller
	// or dropping coordination work; the transition worker drains this outbox
	// only after the target controller is active.
	if queued, err := m.queueDuringInterfaceTransition(ctx, id, message, clientMessageID); err != nil {
		return fmt.Errorf("send %s: interface transition: %w", id, err)
	} else if queued {
		return nil
	}
	// Chat mode has no pane to type into, so it does not go through the messenger
	// at all. Without this branch the send reached the runtime guard and was
	// refused as "missing runtime handles" — true of the handles, wrong about the
	// session, and it left `ao send` and orchestrator-to-worker relay unable to
	// reach a chat worker.
	if handled, err := m.sendChat(ctx, id, message, clientMessageID, origin); handled {
		return err
	}

	message, err := m.prepareOutboundMessage(ctx, id, message)
	if err != nil {
		return err
	}
	// Deliver is sessionguard's USER-origin operation, so calling it for an
	// automatic write told the guard a human had typed this — and the pause
	// fence only refuses originAuto. Threading the origin into sendChat alone
	// fixed the chat half and left the terminal half exactly as it was: the
	// transition outbox could still write to a paused TUI session.
	deliver := m.messenger.Deliver
	if origin == sendOriginAuto {
		deliver = m.messenger.DeliverAuto
	}
	outcome, err := deliver(ctx, id, message)
	if err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	switch outcome {
	case sessionguard.SuppressedPaused:
		// Suppressed, not failed: the guard already logged it, and the write
		// was AO's own. Reporting an error would make the outbox retry a
		// message the pause exists to withhold.
		return nil
	case sessionguard.SuppressedNotFound:
		return fmt.Errorf("send %s: %w", id, ErrNotFound)
	case sessionguard.SuppressedTerminated:
		return fmt.Errorf("send %s: %w", id, ErrTerminated)
	case sessionguard.SuppressedExited:
		return fmt.Errorf("send %s: %w", id, ErrAgentExited)
	case sessionguard.SuppressedAwaitingUser:
		return fmt.Errorf("send %s: %w", id, ErrAwaitingDecision)
	case sessionguard.SuppressedSwitchPending:
		return fmt.Errorf("send %s: %w", id, ErrSwitchInProgress)
	case sessionguard.SuppressedUnknown:
		return fmt.Errorf("send %s: pre-write session read failed", id)
	}
	// confirmActive only helps — and is only SAFE — when the harness reports
	// both a prompt-submit signal (so the loop can observe active) and a
	// blocked signal it can clear mid-turn (so it can tell an unsubmitted
	// draft from a pending permission dialog and never Enter into the latter).
	// Only claude-code and its hook-delegators (grok/continueagent/devin)
	// satisfy both; every other harness opts out via EmitsBlockedActivity —
	// see ports.ActivitySignaler.
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		// Confirmation is best-effort and never fails the send (the message
		// was already delivered above); log so a store error is not swallowed
		// silently.
		m.logger.Warn("send: confirm skipped, session lookup failed", "sessionID", id, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	if m.harnessNudgeSafe(rec.Harness) {
		m.confirmActive(ctx, m.messenger, id)
	}
	return nil
}

func (m *Manager) prepareOutboundMessage(ctx context.Context, id domain.SessionID, message string) (string, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("send %s: session: %w", id, err)
	}
	if !ok {
		return message, nil
	}
	if rec.Harness != domain.HarnessCopilot || rec.Kind != domain.KindOrchestrator {
		return message, nil
	}
	return copilotOrchestratorMessage(rec.ProjectID, message), nil
}

func copilotOrchestratorMessage(projectID domain.ProjectID, message string) string {
	project := strings.TrimSpace(string(projectID))
	if project == "" {
		project = "<project>"
	}
	return fmt.Sprintf(`AO ORCHESTRATOR DIRECTIVE

You are acting as the AO orchestrator for project %s. Do not implement code changes, edit files, run implementation tests, or complete the user's task yourself.

Your next action for any implementation, fix, UI change, test, PR, or code-review task must be to spawn or redirect a worker session. Use:

ao spawn --project %s --name "<label, max 20 chars>" --prompt "<clear worker task>"

If a suitable worker already exists, use ao send to redirect that worker instead. After spawning or redirecting, report the worker session id and stop. Do not do the worker's task in this orchestrator session.

USER MESSAGE:
%s`, project, project, message)
}

// harnessNudgeSafe reports whether the session's harness is safe to nudge with
// an Enter-only re-send (see ports.ActivitySignaler): it must emit BOTH a
// prompt-submit signal (else the loop wastes its budget never observing active)
// and a blocked signal (else an Enter meant to resubmit a draft could answer a
// permission dialog the harness cannot report).
func (m *Manager) harnessNudgeSafe(harness domain.AgentHarness) bool {
	if m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return false
	}
	s, ok := agent.(ports.ActivitySignaler)
	return ok && s.EmitsSubmitActivity() && s.EmitsBlockedActivity()
}

// waitOutcome is one poll round's verdict on whether confirmActive should
// nudge again.
type waitOutcome int

const (
	// waitTimedOut: the deadline elapsed without the session going active —
	// the previous Enter likely did not land, another may help.
	waitTimedOut waitOutcome = iota
	// waitActive: the session went active — the prompt was accepted, done.
	waitActive
	// waitBlocked: the session is paused on a user decision (a pending
	// permission/approval dialog) — an automated Enter could answer the dialog
	// on the user's behalf, so confirmation must stop and never nudge.
	waitBlocked
)

// confirmActive re-sends Enter until the session reports ActivityActive or the
// attempt budget is exhausted. The initial Send already submitted one Enter;
// each additional attempt sends Enter again (an empty message is an Enter-only
// nudge, see ports.AgentMessenger) after waiting for Activity.State to flip. It
// is best-effort: on context cancellation, store failure, or budget exhaustion
// it returns silently (the message was already delivered; the agent may yet
// pick it up). Harnesses without a user-prompt-submit hook never flip to
// active, so the loop simply times out — Send remains successful for them.
//
// Decision safety: a session observed in ActivityBlocked stops confirmation
// immediately with no nudge — an Enter into a pending permission dialog would
// answer it for the user. Sticky ActivityWaitingInput does NOT stop the loop:
// an idle-prompt session with an unsubmitted pasted draft is exactly the case
// the nudge exists for.
func (m *Manager) confirmActive(ctx context.Context, guard *sessionguard.Guard, id domain.SessionID) {
	for attempt := 1; ; attempt++ {
		outcome, err := m.waitForActive(ctx, id)
		if err != nil || outcome == waitActive {
			return
		}
		if outcome == waitBlocked {
			m.logger.Info("send: session awaiting a decision; skipping Enter nudge", "sessionID", id, "attempt", attempt)
			return
		}
		if attempt >= m.sendConfirm.maxAttempts {
			m.logger.Warn("send: activity confirmation budget exhausted", "sessionID", id, "attempts", attempt)
			return
		}
		// Timed out with budget remaining: the previous Enter did not land.
		// Nudge again with an Enter-only send. Deliver re-reads state
		// immediately before pasting — a permission dialog can appear in the
		// gap between waitForActive's final poll and this send, and an Enter
		// into it would answer the decision. This closes the TOCTOU the
		// per-poll check inside waitForActive cannot cover; a store failure
		// inside the guard fails closed (no Enter on an unknown state).
		// DeliverAuto, not Deliver: the user asked for the original message,
		// but AO alone decides to press Enter again, so a durable pause must
		// stop it.
		nudge, nudgeErr := guard.DeliverAuto(ctx, id, "")
		if nudgeErr != nil {
			m.logger.Warn("send: confirm re-send failed", "sessionID", id, "attempt", attempt, "error", nudgeErr)
			return
		}
		if nudge != sessionguard.Sent {
			// Not necessarily blocked: the session may also have terminated or
			// vanished since the poll — the outcome says which.
			m.logger.Info("send: session unavailable before nudge; skipping Enter nudge", "sessionID", id, "attempt", attempt, "outcome", nudge.String())
			return
		}
	}
}

// waitForActive polls Activity.State for up to attemptDeadline and reports
// whether another nudge could help (see waitOutcome). Blocked is checked every
// poll so a permission dialog appearing mid-wait aborts immediately instead of
// burning the deadline. A non-nil error means polling cannot continue (ctx
// cancelled, store failure, session gone).
func (m *Manager) waitForActive(ctx context.Context, id domain.SessionID) (waitOutcome, error) {
	deadlineAt := m.clock().Add(m.sendConfirm.attemptDeadline)
	ticker := time.NewTicker(m.sendConfirm.pollInterval)
	defer ticker.Stop()
	for {
		rec, ok, err := m.store.GetSession(ctx, id)
		if err != nil {
			return waitTimedOut, err
		}
		if !ok {
			return waitTimedOut, fmt.Errorf("session %s not found", id)
		}
		switch rec.Activity.State {
		case domain.ActivityActive:
			return waitActive, nil
		case domain.ActivityBlocked:
			return waitBlocked, nil
		}
		if !m.clock().Before(deadlineAt) {
			return waitTimedOut, nil
		}
		// The tick select respects ctx cancellation so a request timeout
		// unblocks promptly.
		select {
		case <-ctx.Done():
			return waitTimedOut, ctx.Err()
		case <-ticker.C:
		}
	}
}

// CleanupSkip reports one terminal session whose workspace was preserved
// rather than reclaimed, and why.
type CleanupSkip struct {
	SessionID domain.SessionID
	Reason    string
}

// CleanupResult reports what Cleanup reclaimed and what it preserved.
type CleanupResult struct {
	Cleaned []domain.SessionID
	Skipped []CleanupSkip
}

// Cleanup reclaims the workspaces of terminal sessions in a project. A workspace
// whose teardown is refused (uncommitted work) is never forced; it is reported
// in Skipped with the reason so the refusal is visible instead of silent.
// Cleanup takes the project ownership gate for its whole run: it reclaims
// terminated sessions by their recorded workspace path, and a superseded
// orchestrator row must not be reclaimed while (or after) a replacement hands
// that canonical path to a successor.
func (m *Manager) Cleanup(ctx context.Context, project domain.ProjectID) (CleanupResult, error) {
	if project == "" {
		// Unfiltered cleanup is a supported request (HTTP/CLI clean every
		// project). Acquiring the synthetic "" gate would serialize against
		// nothing: real mutations are gated by actual ProjectID. Partition and
		// take each project's own gate instead.
		return m.cleanupAllProjects(ctx)
	}
	release, err := m.acquireProjectOwnership(ctx, project)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	defer release()
	return m.cleanupProjectUnderOwnership(ctx, project)
}

// cleanupAllProjects runs cleanup one project at a time, each under that
// project's own ownership gate. Project ids come from a first pass whose only
// job is partitioning; every record acted on is re-read under the gate, so a
// project that gains or retires an orchestrator in between is still handled
// against current state.
func (m *Manager) cleanupAllProjects(ctx context.Context) (CleanupResult, error) {
	recs, err := m.store.ListAllSessions(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup: %w", err)
	}
	seen := make(map[domain.ProjectID]struct{}, len(recs))
	projects := make([]domain.ProjectID, 0, len(recs))
	for _, rec := range recs {
		if _, ok := seen[rec.ProjectID]; ok {
			continue
		}
		seen[rec.ProjectID] = struct{}{}
		projects = append(projects, rec.ProjectID)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i] < projects[j] })

	combined := CleanupResult{Cleaned: []domain.SessionID{}, Skipped: []CleanupSkip{}}
	for _, p := range projects {
		release, acqErr := m.acquireProjectOwnership(ctx, p)
		if acqErr != nil {
			return CleanupResult{}, fmt.Errorf("cleanup %s: %w", p, acqErr)
		}
		res, cleanErr := m.cleanupProjectUnderOwnership(ctx, p)
		release()
		if cleanErr != nil {
			return CleanupResult{}, cleanErr
		}
		combined.Cleaned = append(combined.Cleaned, res.Cleaned...)
		combined.Skipped = append(combined.Skipped, res.Skipped...)
	}
	return combined, nil
}

// cleanupProjectUnderOwnership reclaims one project's terminated workspaces.
// Callers must already hold that project's ownership gate.
func (m *Manager) cleanupProjectUnderOwnership(ctx context.Context, project domain.ProjectID) (CleanupResult, error) {
	// Re-read under the gate: any listing taken before it is stale.
	recs, err := m.cleanupRecords(ctx, project)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, err)
	}
	result := CleanupResult{Cleaned: make([]domain.SessionID, 0, len(recs)), Skipped: []CleanupSkip{}}
	for _, rec := range recs {
		if !rec.IsTerminated {
			continue
		}
		ws := workspaceInfo(rec)
		if ws.Path == "" {
			m.cleanupSystemPromptDir(rec.ID)
			continue
		}
		// Never reclaim a canonical orchestrator workspace that the current
		// owner is using: a superseded row can still name it.
		aliased, aliasErr := m.canonicalWorkspaceHeldByActiveOrchestrator(ctx, rec)
		if aliasErr != nil {
			return CleanupResult{}, fmt.Errorf("cleanup %s: %w", project, aliasErr)
		}
		if h := runtimeHandle(rec.Metadata); h.ID != "" {
			_ = m.runtime.Destroy(ctx, h) // best effort; usually already gone
		}
		if aliased {
			// Only filesystem teardown is suppressed. This row's scoped shells
			// still live inside the successor's canonical workspace, so drain
			// them before reporting the workspace as preserved.
			m.drainScopedShells(ctx, rec.ID)
			result.Skipped = append(result.Skipped, CleanupSkip{
				SessionID: rec.ID,
				Reason:    "workspace is owned by the project's active orchestrator",
			})
			continue
		}
		if reason := m.cleanupOne(ctx, rec, ws); reason != "" {
			result.Skipped = append(result.Skipped, CleanupSkip{SessionID: rec.ID, Reason: reason})
			continue
		}
		m.cleanupSystemPromptDir(rec.ID)
		result.Cleaned = append(result.Cleaned, rec.ID)
	}
	return result, nil
}

// cleanupOne reclaims one terminated session's workspace, gating shut any
// shell terminal scoped to it first (same ordering as Kill). Split out of
// Cleanup's loop so the release function's defer is scoped to one session's
// call, not deferred across every iteration until Cleanup itself returns.
// Returns "" when the workspace was reclaimed; a non-empty reason means it was
// left alone this run (Cleanup records it in Skipped and can retry on a later
// call) — most commonly because a scoped shell terminal could not be
// confirmed closed, so reclaiming would pull the ground out from under it.
// drainScopedShells closes any shell terminal scoped to a session without
// removing its workspace. Used when filesystem teardown is deliberately
// suppressed (the path belongs to another, still-active owner) but the
// session's own execution surfaces must not be left running inside it.
func (m *Manager) drainScopedShells(ctx context.Context, id domain.SessionID) {
	release, err := m.beginShellTerminalTeardown(ctx, id)
	if err != nil {
		m.logger.Warn("shell terminal still open on a preserved workspace", "sessionID", id, "error", err)
		return
	}
	if release != nil {
		// Nothing is being removed, so the teardown gate is released
		// immediately; the shells themselves are already closed.
		release()
	}
}

func (m *Manager) cleanupOne(ctx context.Context, rec domain.SessionRecord, ws ports.WorkspaceInfo) (skipReason string) {
	release, closeErr := m.beginShellTerminalTeardown(ctx, rec.ID)
	if closeErr != nil {
		m.logger.Warn("cleanup: shell terminal still open", "sessionID", rec.ID, "error", closeErr)
		return "shell terminal still open"
	}
	if release != nil {
		defer release()
	}

	if rows, ok, rowErr := m.workspaceProjectRows(ctx, rec); rowErr != nil {
		m.logger.Warn("cleanup: workspace rows failed", "sessionID", rec.ID, "error", rowErr)
		return "workspace teardown failed"
	} else if ok {
		if _, err := m.destroyWorkspaceProjectRows(ctx, rows); err != nil {
			if !errors.Is(err, ports.ErrWorkspaceDirty) {
				m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
			}
			return cleanupSkipReason(err)
		}
		m.cleanupAgentWorkspace(ctx, rec, ws.Path)
		return ""
	}
	if err := m.workspace.Destroy(ctx, ws); err != nil {
		if !errors.Is(err, ports.ErrWorkspaceDirty) {
			// The public reason stays a fixed string (the raw error carries
			// internal filesystem paths); the full cause lands here.
			m.logger.Warn("cleanup: workspace teardown failed", "sessionID", rec.ID, "path", ws.Path, "error", err)
		}
		return cleanupSkipReason(err)
	}
	m.cleanupAgentWorkspace(ctx, rec, ws.Path)
	return ""
}

// cleanupSkipReason renders a workspace teardown refusal as a short
// user-facing reason for the cleanup report. Deliberately not the raw error:
// it flows to the API response and CLI output, and teardown errors embed
// internal filesystem paths.
func cleanupSkipReason(err error) string {
	if errors.Is(err, ports.ErrWorkspaceDirty) {
		return "workspace has uncommitted changes"
	}
	if errors.Is(err, ErrProjectNotResolvable) {
		return "project is archived or unregistered — remove worktree manually"
	}
	return "workspace teardown failed"
}

func (m *Manager) cleanupRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		return m.store.ListAllSessions(ctx)
	}
	return m.store.ListSessions(ctx, project)
}

// ---- helpers ----

func seedRecord(cfg ports.SpawnConfig, now time.Time) domain.SessionRecord {
	rec := domain.SessionRecord{
		ProjectID:   cfg.ProjectID,
		IssueID:     cfg.IssueID,
		Kind:        cfg.Kind,
		CreatedAt:   now,
		UpdatedAt:   now,
		Harness:     cfg.Harness,
		DisplayName: cfg.DisplayName,
		Activity:    domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
		// Resolved before this point and persisted here. There is no UPDATE
		// statement that can change it afterwards.
		Mode: domain.NormalizeSessionMode(cfg.RequestedMode),
	}
	if cfg.RoleBinding.RoleID != "" {
		rec.Metadata.Role = cfg.RoleBinding
		// Prefer resolved harness from role binding when set.
		if cfg.RoleBinding.ResolvedHarness != "" {
			rec.Harness = cfg.RoleBinding.ResolvedHarness
		}
	}
	return rec
}

func defaultSessionBranch(id domain.SessionID, kind domain.SessionKind, prefix, branchNamespace string) string {
	if kind == domain.KindOrchestrator {
		return aoBranch(branchNamespace, prefix+"-orchestrator")
	}
	// A fresh, unique branch per worker session: gitworktree can't add a worktree
	// on a branch already checked out elsewhere (e.g. main). Put the root work
	// branch under a session namespace so sibling PR branches such as
	// ao/<session>/<topic> remain valid Git refs.
	return aoBranch(branchNamespace, string(id), "root")
}

// DefaultSpawnBranch returns AO's generated work branch for a spawn. Explicit
// user-provided branches bypass this helper.
func DefaultSpawnBranch(id domain.SessionID, kind domain.SessionKind, prefix string, projectKind domain.ProjectKind, dataDir string) string {
	if projectKind == domain.ProjectKindScratch {
		return ""
	}
	branchNamespace := generatedBranchNamespace(dataDir)
	// Workspace projects give each worker a per-session branch shared across the
	// root and child repos. Orchestrators are the exception in every project
	// kind: their worktree is canonical per project
	// (<managedRoot>/<projectID>/orchestrator/<prefix>-orchestrator), so the
	// branch must be canonical too. A per-session branch on a project-canonical
	// path cannot survive retire-and-replace — Create adopts the existing
	// registration and returns the previous session's branch — and it makes
	// verifyOrchestratorReplacement reject every workspace-project orchestrator.
	if projectKind == domain.ProjectKindWorkspace && kind != domain.KindOrchestrator {
		return aoBranch(branchNamespace, string(id))
	}
	return defaultSessionBranch(id, kind, prefix, branchNamespace)
}

// DefaultOrchestratorBranch returns the generated canonical orchestrator branch
// for a project in the current data-dir namespace.
func DefaultOrchestratorBranch(prefix, dataDir string) string {
	return defaultSessionBranch("", domain.KindOrchestrator, prefix, generatedBranchNamespace(dataDir))
}

func aoBranch(namespace string, parts ...string) string {
	all := []string{"ao"}
	if namespace != "" {
		all = append(all, namespace)
	}
	all = append(all, parts...)
	return strings.Join(all, "/")
}

func generatedBranchNamespace(dataDir string) string {
	if isDefaultDevDataDir(dataDir) {
		return "dev"
	}
	return ""
}

func isDefaultDevDataDir(dataDir string) bool {
	if strings.TrimSpace(dataDir) == "" {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	want, err := filepath.Abs(filepath.Join(home, ".ao", "dev", "data"))
	if err != nil {
		return false
	}
	got, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	return filepath.Clean(got) == filepath.Clean(want)
}

func buildPrompt(cfg ports.SpawnConfig) string {
	return buildTaskPrompt(taskPromptConfig{
		Role:         promptRoleForKind(cfg.Kind),
		Prompt:       cfg.Prompt,
		IssueID:      string(cfg.IssueID),
		IssueContext: cfg.IssueContext,
	})
}

func promptRoleForKind(kind domain.SessionKind) sessionPromptRole {
	switch kind {
	case domain.KindOrchestrator:
		return sessionPromptRoleOrchestrator
	case domain.KindWorker:
		return sessionPromptRoleWorker
	default:
		return ""
	}
}

func promptProjectContext(projectID domain.ProjectID, project domain.ProjectRecord) promptProject {
	cfg := project.Config.WithDefaults()
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		cfg.DefaultBranch = ""
	}
	id := project.ID
	if strings.TrimSpace(id) == "" {
		id = string(projectID)
	}
	return promptProject{
		ID:            id,
		Name:          project.DisplayName,
		Repo:          project.RepoOriginURL,
		DefaultBranch: cfg.DefaultBranch,
		Path:          project.Path,
	}
}

// attachmentsDir is the worktree-relative directory where spawn image
// attachments are written.
const attachmentsDir = ".ao/attachments"

// writeSpawnAttachments writes each attachment into the worktree under
// attachmentsDir as image-1<ext>, image-2<ext>, ... and returns the
// worktree-relative paths in order. The files are excluded from git via the
// worktree's info/exclude so they do not dirty the working tree.
func writeSpawnAttachments(workspacePath string, attachments []ports.SpawnAttachment) ([]string, error) {
	dir := filepath.Join(workspacePath, filepath.FromSlash(attachmentsDir))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create attachments dir: %w", err)
	}
	refs := make([]string, 0, len(attachments))
	for i, a := range attachments {
		ext := a.Ext
		if ext == "" {
			ext = ".bin"
		}
		name := fmt.Sprintf("image-%d%s", i+1, ext)
		if err := os.WriteFile(filepath.Join(dir, name), a.Data, 0o600); err != nil {
			return nil, fmt.Errorf("write attachment %d: %w", i+1, err)
		}
		// Worktree-relative reference, always forward-slashed for the prompt.
		refs = append(refs, attachmentsDir+"/"+name)
	}
	return refs, nil
}

// appendAttachmentReferences appends a block listing the attached image paths so
// the agent knows to read them. Placed after the human's brief.
func appendAttachmentReferences(prompt string, refs []string) string {
	if len(refs) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)
	if strings.TrimSpace(prompt) != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Attached images (read these files in the workspace for visual context):")
	for _, ref := range refs {
		b.WriteString("\n- ")
		b.WriteString(ref)
	}
	return b.String()
}

// buildSpawnTexts returns the user-facing prompt and the system prompt to
// deliver separately to the agent. Orchestrator role instructions and worker
// coordination hints are placed in the system prompt so they are treated as
// standing instructions rather than part of the human's task request. A
// promptless spawn delivers no user prompt at all: the agent simply lands at an
// empty input box rather than receiving an auto-generated kickoff turn.
func (m *Manager) buildSpawnTexts(ctx context.Context, cfg ports.SpawnConfig, role roleApplyResult) (prompt, systemPrompt string, err error) {
	prompt = buildPrompt(cfg)
	systemPrompt, err = m.buildSystemPrompt(ctx, cfg.Kind, cfg.ProjectID)
	if err != nil {
		return "", "", err
	}
	// Use the single pinned role resolution from Spawn — fail closed if missing.
	systemPrompt, err = composeSystemPromptWithRole(systemPrompt, role)
	if err != nil {
		return "", "", err
	}
	return prompt, systemPrompt, nil
}

// buildSystemPrompt derives the standing instructions for a session of the
// given kind from current store state. Restore recomputes them through here
// rather than persisting them, so a restored worker points at the orchestrator
// that is active now, not the one from its original spawn.
func (m *Manager) buildSystemPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	cfg := systemPromptConfig{
		Role:    promptRoleForKind(kind),
		Project: promptProjectContext(projectID, project),
	}

	switch kind {
	case domain.KindOrchestrator:
		cfg.OrchestratorRules = project.Config.OrchestratorRules
	case domain.KindWorker:
		orchestratorID, ok, err := m.activeOrchestratorSessionID(ctx, projectID)
		if err != nil {
			return "", err
		}
		if ok {
			cfg.OrchestratorSessionID = string(orchestratorID)
		}
		rules, err := buildProjectRules(projectRulesConfig{
			ProjectPath:    project.Path,
			AgentRules:     project.Config.AgentRules,
			AgentRulesFile: project.Config.AgentRulesFile,
		})
		if err != nil {
			return "", err
		}
		cfg.ProjectRules = rules
	default:
		return "", nil
	}

	workspacePrompt, err := m.workspaceProjectPrompt(ctx, kind, projectID)
	if err != nil {
		return "", err
	}
	if workspacePrompt != "" {
		cfg.AdditionalSections = append(cfg.AdditionalSections, workspacePrompt)
	}
	if pointer := strings.TrimSpace(m.aoSkillPointer()); pointer != "" {
		cfg.AdditionalSections = append(cfg.AdditionalSections, pointer)
	}
	return buildSystemPromptText(cfg), nil
}

// aoSkillPointer is appended to every agent system prompt. It points the agent
// at the using-ao skill the daemon installs under the data dir, rather than
// inlining the whole CLI catalog. The path is absolute so it resolves from any
// project's worktree, not just the AO repo (the only place a repo-relative
// skills/ path would exist). The skill file carries exact flags and examples,
// so the standing prompt stays a short pointer rather than a command dump.
func (m *Manager) aoSkillPointer() string {
	dir := skillassets.Dir(m.dataDir)
	skillFile := filepath.ToSlash(filepath.Join(dir, "SKILL.md"))
	commandsGlob := filepath.ToSlash(filepath.Join(dir, "commands", "*.md"))
	browserFile := filepath.ToSlash(filepath.Join(dir, "commands", "browser.md"))
	previewFile := filepath.ToSlash(filepath.Join(dir, "commands", "preview.md"))
	return "\n\n" + "## Using the ao CLI\n\n" +
		"When using `ao`, read `" + skillFile + "` and only the relevant file under `" + commandsGlob + "`; do not load unrelated command guides.\n\n" +
		"## AO desktop Browser panel\n\n" +
		"For frontend work, read `" + previewFile + "` before previewing or starting an app: open static HTML or Markdown directly; Never create or modify `package.json` or install dependencies solely to display static files. Do not create `.ao/launch.json` unless the user asks. Automatically open the primary requested browser-displayable artifact immediately after creating or materially updating it, but do not replace an active application preview with a supporting asset. " +
		"For page inspection or interaction, read `" + browserFile + "` and use `ao browser` from this AO session. Browser network capture is optional and off by default; follow that guide and never enable it for routine browser actions. " +
		"Do not use Codex/host in-app browser connectors, `agent.browsers.get(\"iab\")`, or a browser MCP for the AO Browser panel: those are separate browser runtimes and cannot see or control AO's session-owned page. " +
		"`ao browser` operates the same live page the user sees in that panel."
}

func (m *Manager) workspaceProjectPrompt(ctx context.Context, kind domain.SessionKind, projectID domain.ProjectID) (string, error) {
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	if project.Kind.WithDefault() != domain.ProjectKindWorkspace {
		return "", nil
	}
	repos, err := m.store.ListWorkspaceRepos(ctx, string(projectID))
	if err != nil {
		return "", fmt.Errorf("list workspace repos for prompt: %w", err)
	}
	switch kind {
	case domain.KindOrchestrator:
		return workspaceOrchestratorPrompt(repos), nil
	case domain.KindWorker:
		return workspaceWorkerPrompt(repos), nil
	default:
		return "", nil
	}
}

// activeOrchestratorSessionID resolves the project's owning orchestrator, which
// a worker's system prompt names as its coordinator.
//
// It applies newestOrchestratorRecord — the SAME rule as EnsureOrchestrator and
// migration 0057 — rather than taking the first match in ListSessions order.
// That order is insertion order, so the old first-match returned the OLDEST
// active orchestrator while ownership resolved to the newest: with two rows
// briefly active, every worker spawned in that window was told to report to the
// one being superseded. Migration 0057's index now makes two active rows
// unreachable, but a second resolver that disagrees by construction is a trap
// waiting for the next path that predates the index, so there is exactly one.
func (m *Manager) activeOrchestratorSessionID(ctx context.Context, project domain.ProjectID) (domain.SessionID, bool, error) {
	recs, err := m.store.ListSessions(ctx, project)
	if err != nil {
		return "", false, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	active := make([]domain.SessionRecord, 0, 1)
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated {
			active = append(active, rec)
		}
	}
	if len(active) == 0 {
		return "", false, nil
	}
	return newestOrchestratorRecord(active).ID, true, nil
}

func (m *Manager) writeSystemPromptFile(id domain.SessionID, systemPrompt string) (string, error) {
	if systemPrompt == "" || strings.TrimSpace(m.dataDir) == "" {
		return "", nil
	}
	path := filepath.Join(m.systemPromptDir(id), "system.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(strings.TrimRight(systemPrompt, "\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (m *Manager) prepareSystemPromptFile(id domain.SessionID, harness domain.AgentHarness, systemPrompt string) (string, error) {
	path, err := m.writeSystemPromptFile(id, systemPrompt)
	if err == nil || path != "" {
		return path, err
	}
	if systemPromptFileRequired(harness) {
		return "", err
	}
	m.logger.Warn("system prompt file unavailable; falling back to inline system prompt", "session", id, "harness", harness, "err", err)
	return "", nil
}

func systemPromptFileRequired(harness domain.AgentHarness) bool {
	switch harness {
	case domain.HarnessAider,
		domain.HarnessAgy,
		domain.HarnessAuggie,
		domain.HarnessKiro,
		domain.HarnessOpenCode,
		domain.HarnessCopilot,
		domain.HarnessVibe:
		return true
	default:
		return false
	}
}

func (m *Manager) systemPromptDir(id domain.SessionID) string {
	if strings.TrimSpace(m.dataDir) == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "prompts", string(id))
}

func (m *Manager) cleanupSystemPromptDir(id domain.SessionID) {
	dir := m.systemPromptDir(id)
	if dir == "" {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		m.logger.Warn("system prompt cleanup failed", "session", id, "path", dir, "err", err)
	}
}

func workspaceOrchestratorPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This project is a multi-repository workspace. Sessions start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

When spawning workers, name the repository path or paths they should work in. Work can span multiple repositories, so track deliverables, pull requests, and checks by repository.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceWorkerPrompt(repos []domain.WorkspaceRepoRecord) string {
	return fmt.Sprintf(`## Workspace project

This session is a multi-repository workspace. You start at the workspace root. The root repository is %s at path `+"`.`"+`; child repositories are nested below it.

Repositories:
%s

Before editing, identify which repository owns the task and keep changes scoped to the requested repository or repositories. If you touch root files, call that out explicitly because root changes are separate from child-repository changes.`, domain.RootWorkspaceRepoName, workspaceRepoList(repos))
}

func workspaceRepoList(repos []domain.WorkspaceRepoRecord) string {
	lines := make([]string, 0, 1+len(repos))
	lines = append(lines, fmt.Sprintf("- %s: .", domain.RootWorkspaceRepoName))
	for _, repo := range repos {
		lines = append(lines, fmt.Sprintf("- %s: %s", repo.Name, repo.RelativePath))
	}
	return strings.Join(lines, "\n")
}

// spawnEnv builds the runtime environment: the per-project env vars first, then
// the AO-internal vars last so they always win (a project cannot override
// AO_SESSION_ID and friends).
func spawnEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, dataDir string, projectEnv map[string]string) map[string]string {
	env := make(map[string]string, len(projectEnv)+4)
	for k, v := range projectEnv {
		env[k] = v
	}
	env[EnvSessionID] = string(id)
	env[EnvProjectID] = string(project)
	env[EnvIssueID] = string(issue)
	env[EnvDataDir] = dataDir
	// Distinct from AO_DATA_DIR: marks a process as an AO-managed agent session.
	env[EnvManagedSession] = "1"
	return env
}

// runtimeEnv is spawnEnv plus the hook PATH pin: the session's PATH puts the
// running daemon's own directory first, so the bare `ao` in workspace hook
// commands resolves to the daemon that installed them rather than whatever
// `ao` is first on the inherited PATH (e.g. a legacy CLI without the hooks
// command, which fails every callback and silently kills activity tracking).
// When the pin cannot be applied the inherited PATH is kept and a warning is
// logged so the degradation isn't silent.
//
// spawnToken is the plaintext random capability for this generation (hash is
// already durable on the session). Empty skips AO_SPAWN_CAPABILITY (cleanup paths).
func (m *Manager) runtimeEnv(id domain.SessionID, project domain.ProjectID, issue domain.IssueID, projectEnv map[string]string, spawnToken string) map[string]string {
	env := spawnEnv(id, project, issue, m.dataDir, projectEnv)
	if m.browserCapabilities != nil {
		env[EnvBrowserCapability] = m.browserCapabilities.Token(id)
	}
	if spawnToken != "" {
		env[EnvSpawnCapability] = spawnToken
	}
	// Never inherit operator or browser runtime secrets into session processes
	// (tmux exec and Windows ConPTY merge os.Environ() into children).
	env[EnvBrowserRuntimeToken] = ""
	env[EnvOperatorSpawnToken] = ""
	path, err := HookPATH(m.executable, os.Getenv, projectEnv)
	if err != nil {
		m.logger.Warn("session PATH not pinned to the daemon binary; `ao hooks` callbacks may resolve to a different ao and activity tracking will stall",
			"session", id, "error", err)
		return env
	}
	env["PATH"] = path
	return env
}

// HookPATH builds the PATH value pinned into a spawned session: the daemon
// executable's directory prepended to the base PATH (the project's PATH
// override when set, else the daemon's inherited PATH — matching what the
// runtime would have exported anyway). An error means the pin cannot be
// applied: the executable is unresolvable, or is not named "ao", in which case
// prepending its directory would not change what `ao` resolves to. Exported so
// the reviewer launcher can pin its pane's PATH the same way.
func HookPATH(executable func() (string, error), getenv func(string) string, projectEnv map[string]string) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", fmt.Errorf("resolve daemon executable: %w", err)
	}
	name := filepath.Base(exe)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	if name != hookBinaryName {
		return "", fmt.Errorf("daemon executable %s is not named %q", exe, hookBinaryName)
	}
	base := projectEnv["PATH"]
	if base == "" {
		base = getenv("PATH")
	}
	dir := filepath.Dir(exe)
	if base == "" {
		return dir, nil
	}
	return dir + string(os.PathListSeparator) + base, nil
}

// provisionWorkspace applies the project's per-workspace setup after the
// worktree exists: symlink shared files from the project repo, then run any
// post-create commands. Either failing aborts the spawn so a half-provisioned
// workspace never launches an agent.
func (m *Manager) provisionWorkspace(ctx context.Context, project domain.ProjectRecord, workspacePath string) error {
	if err := applySymlinks(project.Path, workspacePath, project.Config.Symlinks); err != nil {
		return err
	}
	return runPostCreate(ctx, workspacePath, project.Config.PostCreate)
}

// applySymlinks links each repo-relative path into the workspace. A source that
// does not exist is skipped (symlinks are a convenience for optional files like
// .env); a real link failure aborts. Paths must be repo-relative with no
// parent traversal (no leading "/", no ".." segment) — a bad path is refused
// up front so a project config cannot escape the project or workspace tree.
func applySymlinks(projectPath, workspacePath string, symlinks []string) error {
	for _, rel := range symlinks {
		rel = strings.TrimSpace(rel)
		if rel == "" {
			continue
		}
		clean, err := safeRelPath(rel)
		if err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		source := filepath.Join(projectPath, clean)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := filepath.Join(workspacePath, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("symlink %q: %w", rel, err)
		}
	}
	return nil
}

// safeRelPath confines rel to a repo-relative path: no absolute paths and no
// ".." segments (before or after Clean). The cleaned form is returned so
// callers join it against project/workspace roots safely.
func safeRelPath(rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("path must be repo-relative")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == "." || clean == "" {
		return "", fmt.Errorf("path must be repo-relative")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", fmt.Errorf("path must be repo-relative")
		}
	}
	return clean, nil
}

// runPostCreate runs each post-create command in the workspace via the platform
// shell, so OS-agnostic commands like "pnpm install" work. A non-zero exit
// aborts the spawn with the command output.
func runPostCreate(ctx context.Context, workspacePath string, commands []string) error {
	for _, command := range commands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = aoprocess.CommandContext(ctx, "cmd", "/c", command)
		} else {
			cmd = aoprocess.CommandContext(ctx, "sh", "-c", command)
		}
		cmd.Dir = workspacePath
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("postCreate %q: %w: %s", command, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// preLauncher is an optional Agent capability: a step the manager runs before
// launch. Claude Code implements it to record workspace trust in ~/.claude.json
// so its interactive "do you trust this folder?" dialog can't block the headless
// pane. Adapters that don't need it simply omit the method.
type preLauncher interface {
	PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error
}

// workspaceCleaner is an optional Agent capability for durable agent-side state
// that should be released only after AO has actually removed the workspace.
type workspaceCleaner interface {
	CleanupWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error
}

type runtimeEnvAugmenter interface {
	AugmentRuntimeEnv(env map[string]string, dataDir string)
}

func (m *Manager) augmentAgentRuntimeEnv(agent ports.Agent, env map[string]string) {
	if augmenter, ok := agent.(runtimeEnvAugmenter); ok {
		augmenter.AugmentRuntimeEnv(env, m.dataDir)
	}
}

// prepareWorkspace runs the per-session pre-launch steps before the runtime
// starts the agent: installing the workspace-local activity hooks (so early
// startup hooks can update the already-created session row), then any optional
// PreLaunch step. Shared by Spawn and Restore.
func (m *Manager) prepareWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, env map[string]string) error {
	if err := agent.GetAgentHooks(ctx, ports.WorkspaceHookConfig{
		SessionID:        string(id),
		WorkspacePath:    workspacePath,
		DataDir:          m.dataDir,
		Env:              env,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
	}); err != nil {
		m.cleanupPreparedAgentWorkspace(ctx, agent, id, workspacePath, env)
		return fmt.Errorf("install hooks: %w", err)
	}
	if pl, ok := agent.(preLauncher); ok {
		if err := pl.PreLaunch(ctx, ports.LaunchConfig{DataDir: m.dataDir, SessionID: string(id), WorkspacePath: workspacePath}); err != nil {
			m.cleanupPreparedAgentWorkspace(ctx, agent, id, workspacePath, env)
			return fmt.Errorf("pre-launch: %w", err)
		}
	}
	return nil
}

func (m *Manager) cleanupPreparedAgentWorkspace(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, env map[string]string) {
	cleaner, ok := agent.(workspaceCleaner)
	if !ok {
		return
	}
	if err := cleaner.CleanupWorkspace(ctx, ports.WorkspaceHookConfig{
		SessionID:     string(id),
		WorkspacePath: workspacePath,
		DataDir:       m.dataDir,
		Env:           env,
	}); err != nil {
		m.logger.Warn("session prepare rollback: failed to clean agent workspace state",
			"session", id, "workspacePath", workspacePath, "error", err)
	}
}

func (m *Manager) cleanupAgentWorkspace(ctx context.Context, rec domain.SessionRecord, workspacePath string) {
	if strings.TrimSpace(workspacePath) == "" {
		return
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return
	}
	cleaner, ok := agent.(workspaceCleaner)
	if !ok {
		return
	}
	env := spawnEnv(rec.ID, rec.ProjectID, rec.IssueID, m.dataDir, nil)
	if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
		env = m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env, "")
	} else {
		m.logger.Warn("workspace cleanup: project env unavailable; agent cleanup using AO env only",
			"sessionID", rec.ID, "projectID", rec.ProjectID, "error", err)
	}
	if err := cleaner.CleanupWorkspace(ctx, ports.WorkspaceHookConfig{
		DataDir:       m.dataDir,
		Env:           env,
		SessionID:     string(rec.ID),
		WorkspacePath: workspacePath,
	}); err != nil {
		m.logger.Warn("workspace cleanup: agent cleanup failed", "sessionID", rec.ID, "workspacePath", workspacePath, "error", err)
	}
}

func (m *Manager) deliverAfterStartPrompt(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig, handle ports.RuntimeHandle, id domain.SessionID, launchID, prompt string) error {
	if err := m.waitForPromptReadiness(ctx, agent, cfg, handle); err != nil {
		return err
	}
	// Readiness described what the pane was SHOWING, not what is still behind
	// it. tmux keeps the pane alive after the agent exits (buildLaunchCommand
	// execs an interactive shell in its place), so an agent that quit during
	// the wait — the incident's "No, quit" — leaves a shell that would run the
	// task brief as commands. The guard below cannot see that: the store still
	// says active because no exit hook has landed yet.
	if err := m.confirmWorkloadBeforePaste(ctx, id, handle, launchID); err != nil {
		return err
	}
	// Host-owned delivery: may inject into a SwitchPending target so the first
	// handoff prompt lands before durable target_ack. User Deliver still gated.
	outcome, err := m.messenger.DeliverHost(ctx, id, prompt)
	if err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	switch outcome {
	case sessionguard.SuppressedNotFound:
		return fmt.Errorf("send %s: %w", id, ErrNotFound)
	case sessionguard.SuppressedTerminated:
		return fmt.Errorf("send %s: %w", id, ErrTerminated)
	case sessionguard.SuppressedExited:
		return fmt.Errorf("send %s: %w", id, ErrAgentExited)
	case sessionguard.SuppressedAwaitingUser:
		return fmt.Errorf("send %s: %w", id, ErrAwaitingDecision)
	case sessionguard.SuppressedSwitchPending:
		// Only reached if DeliverHost is unavailable and pending is set.
		return fmt.Errorf("send %s: %w", id, ErrSwitchInProgress)
	case sessionguard.SuppressedUnknown:
		return fmt.Errorf("send %s: pre-write session read failed", id)
	case sessionguard.Sent:
		return nil
	default:
		return fmt.Errorf("send %s: unexpected guard outcome %v", id, outcome)
	}
}

// promptDeliveryWorkloadRecheck bounds the single re-probe in
// confirmWorkloadBeforePaste. It separates "the launch shell has not forked the
// agent yet" from "the agent is gone" — one process-table snapshot cannot tell
// them apart, and an adapter with no readiness hints delivers with no wait at
// all.
const promptDeliveryWorkloadRecheck = 250 * time.Millisecond

// confirmWorkloadBeforePaste refuses an after-start delivery whose agent
// process is no longer running under the pane. Runtimes that cannot inspect
// their workload keep the previous behavior rather than blocking every spawn.
//
// A probe that fails is refused too: pasting a task brief into a pane AO cannot
// account for is the unrecoverable direction, and a refused delivery is the
// recoverable one (same posture as sessionguard's fail-closed read).
func (m *Manager) confirmWorkloadBeforePaste(ctx context.Context, id domain.SessionID, handle ports.RuntimeHandle, launchID string) error {
	result, supported, err := m.probeSupervisedWorkload(ctx, id, handle, launchID)
	switch {
	case !supported:
		return nil
	case err != nil:
		return fmt.Errorf("deliver %s: agent liveness unresolved before prompt delivery: %w", id, err)
	case result == ports.ProbeDead:
		return fmt.Errorf("deliver %s: %w during startup, before the task was delivered", id, ErrAgentExited)
	default:
		return nil
	}
}

// confirmCommandDeliveredAssignment closes the launch-adoption window for a
// saved prompt that the adapter embedded in argv. MarkSpawned necessarily seeds
// ActivityIdle, so this runs immediately afterward and converts a confirmed
// generation-scoped startup death into a failed relaunch instead of returning a
// misleading saved_prompt success.
//
// Unlike after-start paste, a failed probe is not a reason to stop: the task is
// already part of the command, and an unknown probe is never proof of death.
func (m *Manager) confirmCommandDeliveredAssignment(ctx context.Context, handle ports.RuntimeHandle, id domain.SessionID, launchID string) error {
	result, supported, err := m.probeSupervisedWorkload(ctx, id, handle, launchID)
	if !supported {
		return nil
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		m.logger.Warn("relaunch assignment liveness probe failed; not treating it as death",
			"sessionID", id, "launchID", launchID, "error", err)
		return nil
	}
	if result == ports.ProbeDead {
		return fmt.Errorf("confirm command-delivered assignment for %s: %w during startup", id, ErrAgentExited)
	}
	return nil
}

func (m *Manager) probeSupervisedWorkload(ctx context.Context, id domain.SessionID, handle ports.RuntimeHandle, launchID string) (ports.ProbeResult, bool, error) {
	inspector, ok := m.runtime.(ports.SupervisedProcessInspector)
	if !ok || strings.TrimSpace(launchID) == "" || strings.TrimSpace(handle.ID) == "" {
		return ports.ProbeFailed, false, nil
	}
	ref := ports.SupervisedProcessRef{SessionID: id, LaunchID: launchID}
	alive, err := inspector.IsSupervisedProcessAlive(ctx, handle, ref)
	if err == nil && alive {
		return ports.ProbeAlive, true, nil
	}
	if err := sleepContext(ctx, promptDeliveryWorkloadRecheck); err != nil {
		return ports.ProbeFailed, true, err
	}
	alive, err = inspector.IsSupervisedProcessAlive(ctx, handle, ref)
	switch {
	case err != nil:
		return ports.ProbeFailed, true, err
	case !alive:
		return ports.ProbeDead, true, nil
	default:
		return ports.ProbeAlive, true, nil
	}
}

// AllowTerminalInput implements terminal.InputGate: refuse PTY client writes for
// sessions with durable SwitchPending (one-generation ownership).
//
// terminalID is the runtime handle id (tmux session name), which is not always
// equal to SessionID (dots / long ids are sanitized). Resolve by:
//  1. SessionID match (common short-id case)
//  2. Metadata.RuntimeHandleID match
//  3. SwitchPending.SourceRuntimeHandleID match (pending before handle clear)
func (m *Manager) AllowTerminalInput(ctx context.Context, terminalID string) error {
	terminalID = strings.TrimSpace(terminalID)
	if terminalID == "" {
		return nil
	}
	// Fast path: handle often equals session id (short un-sanitized ids).
	if rec, ok, err := m.store.GetSession(ctx, domain.SessionID(terminalID)); err != nil {
		return fmt.Errorf("terminal input: %w", err)
	} else if ok {
		return gateTerminalPending(rec)
	}
	// Indexed lookup by live runtime handle (tmux-normalized name).
	if rec, ok, err := m.store.GetSessionByRuntimeHandleID(ctx, terminalID); err != nil {
		return fmt.Errorf("terminal input: %w", err)
	} else if ok {
		return gateTerminalPending(rec)
	}
	// After source destroy, handle is only on pending JSON (indexed via json_extract).
	if rec, ok, err := m.store.GetSessionByPendingSourceHandle(ctx, terminalID); err != nil {
		return fmt.Errorf("terminal input: %w", err)
	} else if ok {
		return gateTerminalPending(rec)
	}
	// Not an agent session handle (e.g. shell terminal) — allow.
	return nil
}

func gateTerminalPending(rec domain.SessionRecord) error {
	if rec.Metadata.SwitchPending != nil && strings.TrimSpace(rec.Metadata.SwitchPending.GenerationID) != "" {
		return fmt.Errorf("%w: switch pending gen %s", ErrSwitchInProgress, rec.Metadata.SwitchPending.GenerationID)
	}
	return nil
}

func (m *Manager) waitForPromptReadiness(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig, handle ports.RuntimeHandle) error {
	// No provider is not permission to paste.
	//
	// This returned nil, so an adapter that offered no readiness evidence got
	// the ORIGINAL blind-paste behaviour — the liveness probe proves a process
	// exists, never that its pane is an input prompt. Aider and Goose are
	// after-start with no hints today, so they were exactly the sessions this
	// whole change was supposed to protect.
	provider, ok := agent.(ports.AgentPromptReadinessProvider)
	if !ok {
		return fmt.Errorf("%s: %w: this harness offers no readiness evidence",
			cfg.SessionID, ErrPromptNotReady)
	}
	hints, err := provider.PromptReadinessHints(ctx, cfg)
	if err != nil {
		return fmt.Errorf("prompt readiness: %w", err)
	}
	if hints.InitialDelay > 0 {
		if err := sleepContext(ctx, hints.InitialDelay); err != nil {
			return err
		}
	}
	// Same rule for a provider that answers with nothing usable: a delay is not
	// evidence, and a pattern set AO never waits on is not either.
	if len(hints.Patterns) == 0 || hints.Timeout <= 0 {
		return fmt.Errorf("%s: %w: this harness supplied no usable readiness patterns",
			cfg.SessionID, ErrPromptNotReady)
	}
	poll := hints.PollInterval
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	lines := hints.Lines
	if lines <= 0 {
		lines = 80
	}

	deadline := time.NewTimer(hints.Timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		output, err := m.runtime.GetOutput(ctx, handle, lines)
		if err == nil {
			// Negative evidence outranks positive evidence, always: the trust
			// screen that ended three sessions carried BOTH the banner the
			// matcher wanted and the y/n choice the brief went on to answer.
			if marker, blocked := promptOutputBlocked(output); blocked {
				m.logger.Warn("prompt readiness: approval screen on the pane; refusing after-start prompt delivery",
					"sessionID", cfg.SessionID,
					"kind", string(cfg.Kind),
					"marker", marker,
				)
				return fmt.Errorf("prompt readiness %s: %w: the pane is showing an approval screen (%q)",
					cfg.SessionID, ErrPromptNotReady, marker)
			}
			if promptOutputContains(output, hints.Patterns) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// A missing marker is not permission to paste. AO knows what the
			// pane is NOT (an input prompt it recognizes) and nothing about what
			// it is, and the runtime appends Enter to every paste — which is how
			// a task brief answered a dialog nobody had read. Refuse, and let
			// the caller unwind the launch it cannot hand a task to.
			m.logger.Warn("prompt readiness timed out; refusing after-start prompt delivery",
				"sessionID", cfg.SessionID,
				"kind", string(cfg.Kind),
				"timeout", hints.Timeout.String(),
				"pollInterval", poll.String(),
				"lines", lines,
			)
			return fmt.Errorf("prompt readiness %s: %w: no prompt marker appeared within %s",
				cfg.SessionID, ErrPromptNotReady, hints.Timeout)
		case <-ticker.C:
		}
	}
}

func promptOutputContains(output string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(output, pattern) {
			return true
		}
	}
	return false
}

// promptApprovalMarkers name screens that are waiting for a keystroke from the
// USER rather than a task from AO. They are checked against the same capture
// the positive patterns read, and a match refuses delivery even when a positive
// pattern matched too.
//
// The first three are Grok Build 1.0.0's repository-trust screen, captured
// live: "Do you trust the contents of this directory?" over the choices "Yes,
// proceed  y" and "No, quit  n". A pasted brief containing "n" chose the
// second one for three sessions, and the same screen prints the "Grok Build"
// banner the adapter used to accept as readiness. Claude-Code-shaped harnesses
// (Grok's compat layer, Cline, Kiro) phrase the same dialog with "files in this
// folder" and "No, exit"; the y/n forms cover the plain-text confirmations
// other CLIs print before their UI starts.
var promptApprovalMarkers = []string{
	"do you trust",
	"yes, proceed",
	"no, quit",
	"no, exit",
	"press enter to continue",
	"(y/n)",
	"[y/n]",
}

// promptOutputBlocked reports the approval marker visible on the pane, if any.
// Matching is case-insensitive because these screens are prose, not protocol.
func promptOutputBlocked(output string) (string, bool) {
	lowered := strings.ToLower(output)
	for _, marker := range promptApprovalMarkers {
		if strings.Contains(lowered, marker) {
			return marker, true
		}
	}
	return "", false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// restoreArgv builds the argv to relaunch a torn-down session: the agent's
// native resume command when it can continue the session, else a fresh launch
// for harnesses where replaying the saved prompt is acceptable. The agent
// signals via ok=false (e.g. no native session id captured yet). Returns
// ErrNotResumable when transcript-preserving restore is required but unavailable,
// or when a promptless, unresumable worker has nothing to restore from.
func restoreArgv(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, kind domain.SessionKind, harness domain.AgentHarness, dataDir string, policy domain.RoleExecutionPolicy) ([]string, ports.PromptDeliveryStrategy, RestoreMode, error) {
	ref := ports.SessionRef{
		ID:            string(id),
		WorkspacePath: workspacePath,
		Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: meta.AgentSessionID},
	}
	restoreCfg := ports.RestoreConfig{
		Session:          ref,
		Kind:             kind,
		DataDir:          dataDir,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
		Permissions:      agentConfig.Permissions,
	}
	// Re-apply RO enforcement on restore (defense in depth with launch).
	if strings.TrimSpace(meta.Role.RoleID) != "" && !policy.WorkspaceWrites {
		readonly.ApplyRestore(harness, &restoreCfg)
		agentConfig = restoreCfg.Config
	}
	cmd, ok, err := agent.GetRestoreCommand(ctx, restoreCfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("restore command: %w", err)
	}
	if ok {
		return cmd, ports.PromptDeliveryInCommand, RestoreModeNative, nil
	}
	return freshLaunchArgv(ctx, agent, id, workspacePath, meta, systemPrompt,
		systemPromptFile, agentConfig, kind, dataDir, false, policy)
}

// freshLaunchArgv builds the non-resume half of restoreArgv. Interface
// transitions also use it when an adapter proves its reserved id has no
// persisted history, both for preflight and for the actual target launch.
// policy is the fork's addition: a FRESH launch of a role-pinned session still
// has to reach the adapter with read-only applied. Without it, "start a fresh
// conversation" would quietly become the way to get a writable process out of a
// workspaceWrites:false role.
func freshLaunchArgv(ctx context.Context, agent ports.Agent, id domain.SessionID, workspacePath string, meta domain.SessionMetadata, systemPrompt, systemPromptFile string, agentConfig ports.AgentConfig, kind domain.SessionKind, dataDir string, allowPromptless bool, policy domain.RoleExecutionPolicy) ([]string, ports.PromptDeliveryStrategy, RestoreMode, error) {
	// A saved prompt is replayed fresh. An orchestrator is promptless by design
	// and relaunches with the system prompt only. A promptless WORKER has no task
	// and no session id to restore from: do not blank-relaunch it.
	if meta.Prompt == "" && kind != domain.KindOrchestrator && !allowPromptless {
		return nil, "", "", ErrNotResumable
	}
	// Fall through to a fresh launch. Command-delivered agents receive
	// meta.Prompt in argv; after-start agents receive it via the messenger once
	// the runtime is live.
	launchCfg := ports.LaunchConfig{
		DataDir:          dataDir,
		SessionID:        string(id),
		WorkspacePath:    workspacePath,
		Kind:             kind,
		Prompt:           meta.Prompt,
		SystemPrompt:     systemPrompt,
		SystemPromptFile: systemPromptFile,
		Config:           agentConfig,
		Permissions:      agentConfig.Permissions,
	}
	if strings.TrimSpace(meta.Role.RoleID) != "" && !policy.WorkspaceWrites {
		readonly.ApplyLaunch(meta.Role.ResolvedHarness, &launchCfg)
	}
	delivery, err := agent.GetPromptDeliveryStrategy(ctx, launchCfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("prompt delivery: %w", err)
	}
	if delivery == ports.PromptDeliveryAfterStart {
		launchCfg.Prompt = ""
	}
	argv, err := agent.GetLaunchCommand(ctx, launchCfg)
	if err != nil {
		return nil, "", "", fmt.Errorf("launch command: %w", err)
	}
	// Fresh is provisional here. A saved prompt merely being present describes
	// the attempted fallback, not its outcome. relaunchSession promotes the
	// result to saved_prompt only after the selected delivery path succeeds.
	return argv, delivery, RestoreModeFresh, nil
}

// validateAgentBinary checks that argv[0] resolves via the manager's
// lookPath (exec.LookPath in prod) before any runtime work happens. Adapters
// that can't resolve their binary now return ports.ErrAgentBinaryNotFound from
// GetLaunchCommand directly; this guard is a defense-in-depth for adapters
// that return an argv[0] like "claude" without verifying. Some adapters prefix
// their command with `env KEY=value`; in that case validate the first real
// executable after the environment assignments.
func (m *Manager) validateAgentBinary(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("agent: empty launch argv: %w", ports.ErrAgentBinaryNotFound)
	}
	bin, ok := launchBinary(argv)
	if !ok {
		return fmt.Errorf("agent: launch argv missing binary: %w", ports.ErrAgentBinaryNotFound)
	}
	if _, err := m.lookPath(bin); err != nil {
		return fmt.Errorf("agent binary %q: %w", bin, ports.ErrAgentBinaryNotFound)
	}
	return nil
}

func launchBinary(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	if filepath.Base(argv[0]) != "env" {
		return argv[0], true
	}
	for _, arg := range argv[1:] {
		if strings.Contains(arg, "=") {
			continue
		}
		return arg, true
	}
	return "", false
}

func (m *Manager) augmentRuntimePATHForLaunchBinary(ctx context.Context, env map[string]string, argv []string) {
	bin, ok := launchBinary(argv)
	if !ok || !filepath.IsAbs(bin) {
		return
	}
	launchDir := filepath.Dir(bin)
	if launchDir == "." || launchDir == string(filepath.Separator) {
		return
	}
	dirs := []string{launchDir}
	if isNodeLaunchBinary(bin) {
		if nodeDir := m.nodeRuntimeDir(ctx); nodeDir != "" && nodeDir != launchDir {
			dirs = append(dirs, nodeDir)
		}
	}
	var parts []string
	if path := env["PATH"]; path != "" {
		parts = strings.Split(path, string(os.PathListSeparator))
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if !containsPathDir(parts, dirs[i]) {
			parts = append([]string{dirs[i]}, parts...)
		}
	}
	env["PATH"] = strings.Join(parts, string(os.PathListSeparator))
}

func isNodeLaunchBinary(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	const maxShebangBytes = 4096
	buf := make([]byte, maxShebangBytes)
	n, _ := f.Read(buf)
	line := string(buf[:n])
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = line[:newline]
	}
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	for _, field := range strings.Fields(strings.TrimPrefix(line, "#!")) {
		if filepath.Base(field) == "node" {
			return true
		}
	}
	return false
}

func containsPathDir(parts []string, dir string) bool {
	for _, part := range parts {
		if part == dir {
			return true
		}
	}
	return false
}

func (m *Manager) nodeRuntimeDir(ctx context.Context) string {
	if err := ctx.Err(); err != nil || runtime.GOOS == "windows" {
		return ""
	}
	if node, err := m.lookPath("node"); err == nil && node != "" {
		return filepath.Dir(node)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	fnmDir := os.Getenv("FNM_DIR")
	if fnmDir == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			fnmDir = filepath.Join(xdg, "fnm")
		} else if runtime.GOOS == "darwin" {
			fnmDir = filepath.Join(home, "Library", "Application Support", "fnm")
		} else {
			fnmDir = filepath.Join(home, ".local", "share", "fnm")
		}
	}
	voltaHome := os.Getenv("VOLTA_HOME")
	if voltaHome == "" {
		voltaHome = filepath.Join(home, ".volta")
	}
	nvm := versionedNodeMatches(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "node"))
	if data, err := os.ReadFile(filepath.Join(home, ".nvm", "alias", "default")); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			nvm = preferNodeVersion(nvm, fields[0])
		}
	}
	fnmMatches := versionedNodeMatches(filepath.Join(fnmDir, "node-versions", "*", "installation", "bin", "node"))
	candidates := make([]string, 0, len(nvm)+len(fnmMatches)+3)
	candidates = append(candidates, nvm...)
	candidates = append(candidates, fnmMatches...)
	// Prefer explicitly selected/versioned runtimes over manager and package-
	// manager shims. A dormant ~/.volta installation must not override the NVM
	// default or newest fnm runtime merely because the GUI omitted shell setup.
	candidates = append(candidates, filepath.Join(voltaHome, "bin", "node"), "/opt/homebrew/bin/node", "/usr/local/bin/node")
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return ""
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return filepath.Dir(candidate)
		}
	}
	return ""
}

func versionedNodeMatches(pattern string) []string {
	matches, _ := filepath.Glob(pattern)
	sort.SliceStable(matches, func(i, j int) bool {
		return compareNodeVersion(nodeVersionFromPath(matches[i]), nodeVersionFromPath(matches[j])) > 0
	})
	return matches
}

func nodeVersionFromPath(path string) string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "bin" {
		dir = filepath.Dir(dir)
	}
	if filepath.Base(dir) == "installation" {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir)
}

func preferNodeVersion(paths []string, version string) []string {
	version = normalizeNodeVersion(version)
	for i, path := range paths {
		if normalizeNodeVersion(nodeVersionFromPath(path)) != version {
			continue
		}
		out := make([]string, 0, len(paths))
		out = append(out, path)
		out = append(out, paths[:i]...)
		out = append(out, paths[i+1:]...)
		return out
	}
	return paths
}

func compareNodeVersion(a, b string) int {
	av, aok := parseNodeVersion(a)
	bv, bok := parseNodeVersion(b)
	for i := range av {
		if av[i] != bv[i] {
			if av[i] > bv[i] {
				return 1
			}
			return -1
		}
	}
	if aok != bok {
		if aok {
			return 1
		}
		return -1
	}
	return strings.Compare(a, b)
}

func parseNodeVersion(version string) ([3]int, bool) {
	var parsed [3]int
	fields := strings.Split(normalizeNodeVersion(version), ".")
	if len(fields) == 0 || fields[0] == "" {
		return parsed, false
	}
	for i := 0; i < len(fields) && i < len(parsed); i++ {
		n, err := strconv.Atoi(fields[i])
		if err != nil {
			return [3]int{}, false
		}
		parsed[i] = n
	}
	return parsed, true
}

func normalizeNodeVersion(version string) string {
	return strings.TrimPrefix(strings.TrimSpace(version), "v")
}

func (m *Manager) validateRuntimePrerequisites() error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if path, err := m.lookPath("tmux"); err != nil || path == "" {
		return fmt.Errorf("%w: tmux required on macOS/Linux but not in PATH", ports.ErrRuntimePrerequisite)
	}
	return nil
}

func (m *Manager) superviseAgentProcess(agent ports.Agent, id domain.SessionID, env map[string]string, argv []string, forceLaunchID ...string) ([]string, string, error) {
	detector, ok := agent.(ports.AgentExitDetector)
	if !ok || detector.ExitDetectionMode() != ports.AgentExitDetectionSupervisor {
		// Non-supervised agents still need a stable generation id for switch fencing.
		launchID := ""
		if len(forceLaunchID) > 0 {
			launchID = strings.TrimSpace(forceLaunchID[0])
		}
		if launchID == "" {
			launchID = m.newLaunchID()
		}
		if strings.TrimSpace(launchID) == "" {
			return nil, "", errors.New("generated empty launch id")
		}
		delete(env, EnvRuntimeLaunchID)
		return argv, launchID, nil
	}
	executable, err := m.executable()
	if err != nil {
		return nil, "", fmt.Errorf("resolve AO executable: %w", err)
	}
	launchID := ""
	if len(forceLaunchID) > 0 {
		launchID = strings.TrimSpace(forceLaunchID[0])
	}
	if launchID == "" {
		launchID = m.newLaunchID()
	}
	if strings.TrimSpace(launchID) == "" {
		return nil, "", errors.New("generated empty launch id")
	}
	env[EnvRuntimeLaunchID] = launchID
	wrapped := make([]string, 0, 8+len(argv))
	wrapped = append(wrapped, executable, "agent-process", "supervise", "--session", string(id), "--launch", launchID, "--")
	wrapped = append(wrapped, argv...)
	return wrapped, launchID, nil
}

func runtimeHandle(meta domain.SessionMetadata) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: meta.RuntimeHandleID}
}

func workspaceInfo(rec domain.SessionRecord) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      rec.Metadata.WorkspacePath,
		Branch:    rec.Metadata.Branch,
		SessionID: rec.ID,
		ProjectID: rec.ProjectID,
		RepoPath:  rec.Metadata.WorkspaceRepoPath,
	}
}

func workspaceInfoFromRepoInfo(info ports.WorkspaceRepoInfo) ports.WorkspaceInfo {
	return ports.WorkspaceInfo{
		Path:      info.Path,
		Branch:    info.Branch,
		SessionID: info.SessionID,
		ProjectID: info.ProjectID,
		RepoPath:  info.RepoPath,
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
