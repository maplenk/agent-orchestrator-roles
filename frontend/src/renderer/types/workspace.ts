import type { components } from "../../api/schema";
import { attentionZone as presentationAttentionZone } from "../lib/session-presentation";

export type SessionStatus =
	| "working"
	| "pr_open"
	| "draft"
	| "ci_failed"
	| "review_pending"
	| "changes_requested"
	| "approved"
	| "mergeable"
	| "merged"
	| "needs_input"
	| "exited"
	| "no_signal"
	| "idle"
	| "terminated"
	| "unknown";

const sessionStatuses = new Set<SessionStatus>([
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"exited",
	"no_signal",
	"idle",
	"terminated",
]);

export function toSessionStatus(status?: string, isTerminated = false): SessionStatus {
	if (status && sessionStatuses.has(status as SessionStatus)) return status as SessionStatus;
	return isTerminated ? "terminated" : "unknown";
}

export type SessionActivityState = "active" | "idle" | "waiting_input" | "blocked" | "exited" | "unknown";

const sessionActivityStates = new Set<SessionActivityState>(["active", "idle", "waiting_input", "blocked", "exited"]);

export type SessionActivity = {
	state: SessionActivityState;
	lastActivityAt: string;
};

export function toSessionActivity(
	activity?: { state?: string; lastActivityAt?: string } | null,
): SessionActivity | undefined {
	if (!activity) return undefined;
	const state = sessionActivityStates.has(activity.state as SessionActivityState)
		? (activity.state as SessionActivityState)
		: "unknown";
	return {
		state,
		lastActivityAt: activity.lastActivityAt ?? "",
	};
}

export type AgentProvider =
	| "codex"
	| "claude-code"
	| "opencode"
	| "aider"
	| "grok"
	| "droid"
	| "amp"
	| "agy"
	| "crush"
	| "cursor"
	| "qwen"
	| "copilot"
	| "goose"
	| "auggie"
	| "continue"
	| "devin"
	| "cline"
	| "kimi"
	| "muse"
	| "kiro"
	| "kilocode"
	| "vibe"
	| "pi"
	| "autohand"
	| "fake";

/** A file changed in a worker workspace (drives the review rail). */
export type ChangedFile = {
	path: string;
	additions: number;
	deletions: number;
	staged?: boolean;
};

export type SessionKind = "worker" | "orchestrator";

/** Lifecycle state of a single pull request, mirrors the daemon's enum. */
export type PRState = "open" | "draft" | "merged" | "closed";

/**
 * One attributed pull request, mirroring the daemon's SessionPRFacts wire shape.
 * A session can own many (e.g. a stack), so {@link WorkspaceSession.prs} is a
 * list. The wire carries no source/target branch or parent pointer, so the UI
 * renders a flat list of PRs, not a stack tree.
 */
export type PullRequestFacts = {
	url: string;
	number: number;
	state: PRState;
	ci: string;
	review: string;
	mergeability: string;
	reviewComments: boolean;
	updatedAt: string;
};

/** Durable pause pin. Mirrors ControllersSessionPauseView. */
export type SessionPause = {
	incidentId: string;
	reason: "usage_limit" | "operator";
	detectedBy: "structured_envelope" | "operator";
	harness?: string;
	pausedAt: string;
	/**
	 * What the provider said, when it said anything. INFORMATION ONLY — nothing
	 * schedules against it, so it must never be rendered as a countdown to an
	 * automatic resume.
	 */
	retryAfter?: string;
};

/**
 * Read-time failover preview for a session, from the daemon's read model
 * (docs/roles/PHASE3B_MVP_CONTRACT.md §9).
 *
 * Taken from the generated schema rather than re-declared, so a change to the
 * block breaks this file instead of silently diverging from the daemon.
 */
export type SessionFailoverView = NonNullable<components["schemas"]["SessionFailoverView"]>;

/**
 * The next failover rung, already resolved by the host. The renderer treats both
 * fields as opaque display strings: it must never map a harness id to a name or
 * resolve a ladder itself (contract §7 — `domain.NextFailoverRung` is the only
 * thing allowed to choose a rung). An empty model means "provider default",
 * exactly as configured; never a wildcard.
 */
export type SessionFailoverTarget = NonNullable<SessionFailoverView["nextTarget"]>;

/**
 * Why the daemon says Continue is unavailable. Machine-readable precisely so the
 * desktop can say *why* without inventing prose. `""` is the value the daemon
 * uses when Continue IS available; it also covers an `available: false` block
 * that names no reason, which is the one case the UI describes generically.
 *
 * Derived from the schema so that a daemon which grows an eighth reason fails
 * the typecheck at the one place that has to care: the map from reason to
 * localized copy.
 */
export type SessionFailoverReason = SessionFailoverView["reason"];

// A Record, not a Set: this has to be exhaustive, and only a Record makes the
// compiler say so. A reason the daemon knows and this list does not would
// silently degrade to the generic copy even though specific copy exists.
const sessionFailoverReasons: Record<SessionFailoverReason, true> = {
	"": true,
	no_role_pin: true,
	no_ladder: true,
	ladder_exhausted: true,
	limit_reached: true,
	not_paused: true,
	switch_unsupported: true,
	// Not a ladder verdict: the daemon could not compute the preview for this
	// session, so the row degrades instead of failing the whole read. It gets
	// its own copy because "we could not find out" is worth re-reading later and
	// "there is nothing" is not.
	unavailable: true,
};

function toSessionFailoverTarget(raw: unknown): SessionFailoverTarget | null {
	if (typeof raw !== "object" || raw === null) return null;
	const target = raw as { harness?: unknown; model?: unknown };
	// A target without a harness cannot label a button, and the renderer is not
	// allowed to invent one — treat it as no target at all.
	if (typeof target.harness !== "string" || target.harness === "") return null;
	return { harness: target.harness, model: typeof target.model === "string" ? target.model : "" };
}

/**
 * Narrow the daemon's failover block. Unknown `reason` values degrade to `""`
 * (the generic "unavailable" copy) rather than throwing: a daemon that grows an
 * eighth reason must not blank the pause surface in an older desktop.
 */
export function toSessionFailover(raw: unknown): SessionFailoverView | undefined {
	if (typeof raw !== "object" || raw === null) return undefined;
	const view = raw as Record<string, unknown>;
	const reason =
		typeof view.reason === "string" && Object.hasOwn(sessionFailoverReasons, view.reason)
			? (view.reason as SessionFailoverReason)
			: "";
	return {
		available: view.available === true,
		roleId: typeof view.roleId === "string" ? view.roleId : "",
		nextTarget: toSessionFailoverTarget(view.nextTarget),
		nextRungIndex: typeof view.nextRungIndex === "number" ? view.nextRungIndex : 0,
		attemptsUsed: typeof view.attemptsUsed === "number" ? view.attemptsUsed : 0,
		maxAttempts: typeof view.maxAttempts === "number" ? view.maxAttempts : 0,
		incidentId: typeof view.incidentId === "string" ? view.incidentId : "",
		reason,
	};
}

/** Host-resolved orchestrator switch state from the generated API schema. */
export type SessionSwitchView = NonNullable<components["schemas"]["SessionSwitchView"]>;
export type SessionSwitchTarget = components["schemas"]["SessionSwitchTarget"];
export type SessionSwitchReason = SessionSwitchView["reason"];

const sessionSwitchReasons: Record<SessionSwitchReason, true> = {
	"": true,
	no_role_pin: true,
	no_role_map: true,
	role_not_in_map: true,
	no_target: true,
	in_progress: true,
	terminated: true,
	unavailable: true,
};

function toSessionSwitchTarget(raw: unknown): SessionSwitchTarget | null {
	if (typeof raw !== "object" || raw === null) return null;
	const target = raw as { harness?: unknown; model?: unknown };
	if (typeof target.harness !== "string" || target.harness === "") return null;
	return { harness: target.harness, model: typeof target.model === "string" ? target.model : "" };
}

/**
 * Narrow the switch block before it reaches controls. A malformed or degraded
 * block always fails closed: it may explain why switching is unavailable, but
 * it never creates a selectable target.
 */
export function toSessionSwitch(raw: unknown): SessionSwitchView | undefined {
	if (typeof raw !== "object" || raw === null) return undefined;
	const view = raw as Record<string, unknown>;
	const reason =
		typeof view.reason === "string" && Object.hasOwn(sessionSwitchReasons, view.reason)
			? (view.reason as SessionSwitchReason)
			: "unavailable";
	const current = toSessionSwitchTarget(view.current) ?? { harness: "", model: "" };
	const targets =
		reason === "unavailable" || !Array.isArray(view.targets)
			? []
			: view.targets.map(toSessionSwitchTarget).filter((target): target is SessionSwitchTarget => target !== null);
	let pending: SessionSwitchView["pending"] = null;
	if (typeof view.pending === "object" && view.pending !== null) {
		const rawPending = view.pending as Record<string, unknown>;
		const from = toSessionSwitchTarget(rawPending.from);
		const to = toSessionSwitchTarget(rawPending.to);
		const kind = rawPending.kind;
		if (
			from &&
			to &&
			typeof rawPending.generationId === "string" &&
			(kind === "switch" || kind === "fresh_conversation" || kind === "orchestrator_fresh_conversation")
		) {
			pending = { generationId: rawPending.generationId, kind, from, to };
		}
	}
	return {
		available: view.available === true && reason === "" && pending === null && targets.length > 0,
		roleId: typeof view.roleId === "string" ? view.roleId : "",
		current,
		targets,
		pending,
		reason,
	};
}

/** The daemon-committed controller currently responsible for the session. */
export type SessionMode = "chat" | "tui";

export type WorkspaceSession = {
	id: string;
	terminalHandleId?: string;
	workspaceId: string;
	workspaceName: string;
	title: string;
	/** Raw issue/task identifier from the daemon. Intake ids are provider-prefixed. */
	issueId?: string;
	provider: AgentProvider;
	/** Reviewer selected for this session; absent means use the project default. */
	reviewerHarness?: "claude-code" | "codex" | "opencode";
	kind?: SessionKind;
	/**
	 * Which controller is currently committed for this session. The session
	 * surface renders from THIS value, never from the current creation default.
	 * Only the daemon's durable interface-transition coordinator may change it.
	 */
	mode?: SessionMode;
	branch?: string;
	status: SessionStatus;
	/** Stack-aware PR context derived by the daemon independently of runtime activity. */
	scmStatus?: SessionStatus;
	/** Durable runtime fact from the daemon; independent of the derived SCM-aware status. */
	isTerminated?: boolean;
	/** User preference to tear down this session when its PR set completes through a merge. */
	terminateOnPrMerge?: boolean;
	/** ISO timestamp from the daemon — used for relative time in the inspector. */
	createdAt?: string;
	/** ISO timestamp from the daemon. */
	updatedAt: string;
	isPinned?: boolean;
	pinnedAt?: string;
	/** Raw agent lifecycle activity from the daemon. */
	activity?: SessionActivity;
	/**
	 * Durable pause pin from the daemon; absent means not paused.
	 *
	 * Paused and agent-liveness are INDEPENDENT facts: a paused session may be
	 * running or dead, and the two need different controls (see
	 * docs/roles/PHASE3A_PAUSE_CONTRACT.md). Liveness comes from `activity`, not
	 * from here.
	 */
	pause?: SessionPause;
	/**
	 * Host-resolved failover preview; absent when the daemon sends no block.
	 *
	 * Computed at read time, never stored (contract §9). Everything the Continue
	 * control needs — whether it is offered, the target that labels it, and the
	 * machine-readable reason when it is not — comes from here. The renderer
	 * neither resolves the ladder nor names a harness.
	 */
	failover?: SessionFailoverView;
	/** Exact role-map targets and durable pending state for an orchestrator. */
	switch?: SessionSwitchView;
	/**
	 * Live preview target set by the daemon (via `ao preview`) and streamed over
	 * CDC. When non-empty, the browser panel opens and navigates here.
	 */
	previewUrl?: string;
	/**
	 * Monotonic counter the daemon bumps on every `ao preview` call (even when
	 * previewUrl is unchanged), so the browser panel can re-navigate / refresh on
	 * a repeated preview of the same target.
	 */
	previewRevision?: number;
	/** The session's git diff against its base, when known. */
	changedFiles?: ChangedFile[];
	/** Pre-filled commit subject for the Git rail, when known. */
	commitMessage?: string;
	/**
	 * The session's attributed pull requests. One session can own many (a stack
	 * or independent PRs); empty when none are open yet. Status aggregation is
	 * done server-side, so {@link status} already reflects all of these.
	 */
	prs: PullRequestFacts[];
};

// Tracker providers whose ids the intake daemon stamps sessions with, in
// "<provider>:<native>" form. Adding a provider (Linear, Jira, ...) later is
// just another prefix in this list — no caller of canonicalTrackerIssueId
// needs to change.
const TRACKER_PROVIDER_PREFIXES = ["github:"] as const;

/**
 * The provider-prefixed issue id if `issueId` came from tracker intake, or
 * undefined for manually created sessions (whose issueId, if any, is a plain
 * task title with no provider prefix).
 */
export function canonicalTrackerIssueId(issueId?: string): string | undefined {
	if (!issueId) return undefined;
	return TRACKER_PROVIDER_PREFIXES.some((prefix) => issueId.startsWith(prefix)) ? issueId : undefined;
}

export type ProjectKind = "single_repo" | "workspace" | "scratch";

const projectKinds = new Set<ProjectKind>(["single_repo", "workspace", "scratch"]);

export function toProjectKind(kind?: string): ProjectKind | undefined {
	return projectKinds.has(kind as ProjectKind) ? (kind as ProjectKind) : undefined;
}

export type WorkspaceRepoSummary = {
	name: string;
	relativePath: string;
	repo: string;
};

// Open PRs (actionable) sort above merged/closed; ties break by number.
const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

/** A session's PRs ordered actionable-first (open, draft, merged, closed). */
export function sortedPRs(session: WorkspaceSession): PullRequestFacts[] {
	return [...session.prs].sort((a, b) => prStateRank[a.state] - prStateRank[b.state] || a.number - b.number);
}

/** PRs still in flight (open or draft). */
export function openPRs(session: WorkspaceSession): PullRequestFacts[] {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
}

export function mergedPRCount(session: WorkspaceSession): number {
	return session.prs.filter((pr) => pr.state === "merged").length;
}

/** The highest-priority PR for compact one-line surfaces (board card, sidebar). */
export function primaryPR(session: WorkspaceSession): PullRequestFacts | undefined {
	return sortedPRs(session)[0];
}

export function isOrchestratorSession(session: WorkspaceSession): boolean {
	return session.kind === "orchestrator" || session.id.endsWith("-orchestrator");
}

/**
 * The project's LIVE orchestrator, if any. Terminated orchestrator rows stay in
 * the session list (the daemon returns all sessions, ordered by spawn number),
 * so an earlier dead orchestrator must not shadow a live one — its zellij
 * session is deleted and attaching to it dead-ends in an instant
 * "[process exited]". No live orchestrator → undefined, so the topbar offers
 * Spawn instead of navigating to a dead session.
 */
export function findProjectOrchestrator(
	workspaces: WorkspaceSummary[],
	projectId: string,
): WorkspaceSession | undefined {
	const workspace = workspaces.find((w) => w.id === projectId);
	return newestActiveOrchestrator(workspace?.sessions ?? []);
}

export function newestActiveOrchestrator(sessions: WorkspaceSession[]): WorkspaceSession | undefined {
	const active = sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	return active.reduce<WorkspaceSession | undefined>(
		(newest, session) => (!newest || sessionNewer(session, newest) ? session : newest),
		undefined,
	);
}

function sessionNewer(a: WorkspaceSession, b: WorkspaceSession): boolean {
	const aCreated = timestamp(a.createdAt);
	const bCreated = timestamp(b.createdAt);
	if (aCreated !== bCreated) return aCreated > bCreated;
	const aUpdated = timestamp(a.updatedAt);
	const bUpdated = timestamp(b.updatedAt);
	if (aUpdated !== bUpdated) return aUpdated > bUpdated;
	return a.id > b.id;
}

function timestamp(value?: string): number {
	if (!value) return 0;
	const parsed = Date.parse(value);
	return Number.isNaN(parsed) ? 0 : parsed;
}

export function workerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return sessions.filter((s) => !isOrchestratorSession(s));
}

export function sessionIsActive(session: WorkspaceSession): boolean {
	return session.isTerminated !== true && session.status !== "terminated";
}

export function sessionNeedsAttention(session: WorkspaceSession): boolean {
	return presentationAttentionZone(session) === "action";
}

export { attentionZone, attentionZoneLabel, attentionZoneOrder } from "../lib/session-presentation";
export type { AttentionZone } from "../lib/session-presentation";

export type WorkspaceSummary = {
	id: string;
	name: string;
	kind?: ProjectKind;
	path: string;
	workspaceRepos?: WorkspaceRepoSummary[];
	type?: "main" | "worktree";
	orchestratorAgent?: AgentProvider;
	accentColor?: string;
	diff?: {
		additions: number;
		deletions: number;
	};
	sessions: WorkspaceSession[];
};

export function hasConfiguredOrchestratorAgent(
	workspace: Pick<WorkspaceSummary, "orchestratorAgent"> | undefined,
): boolean {
	return Boolean(workspace?.orchestratorAgent);
}

export function orchestratorNeedsRestart(workspace: WorkspaceSummary, orchestrator?: WorkspaceSession): boolean {
	if (!orchestrator || !workspace.orchestratorAgent) return false;
	return orchestrator.provider !== workspace.orchestratorAgent;
}

export type OrchestratorHealth =
	| { state: "ok" }
	| { state: "restarting"; message: string }
	| { state: "restart_needed"; message: string }
	| { state: "missing"; message: string }
	| { state: "duplicates"; message: string };

export function orchestratorHealth(workspace: WorkspaceSummary, restarting = false): OrchestratorHealth {
	if (restarting) {
		return {
			state: "restarting",
			message: "Restarting orchestrator. New tasks wait until the replacement is ready.",
		};
	}
	const active = workspace.sessions.filter((session) => isOrchestratorSession(session) && sessionIsActive(session));
	if (active.length > 1) {
		return {
			state: "duplicates",
			message:
				"Multiple orchestrators are active. The newest one is used; stale ones will be cleaned up on daemon reconcile.",
		};
	}
	const orchestrator = newestActiveOrchestrator(workspace.sessions);
	if (!orchestrator) {
		return { state: "missing", message: "No orchestrator is running for this project." };
	}
	if (orchestratorNeedsRestart(workspace, orchestrator)) {
		return {
			state: "restart_needed",
			message: `Configured orchestrator agent is ${workspace.orchestratorAgent}; running agent is ${orchestrator.provider}.`,
		};
	}
	return { state: "ok" };
}

export function toAgentProvider(provider?: string): AgentProvider {
	switch (provider) {
		case "claude-code":
		case "opencode":
		case "aider":
		case "grok":
		case "droid":
		case "amp":
		case "agy":
		case "crush":
		case "cursor":
		case "qwen":
		case "copilot":
		case "goose":
		case "auggie":
		case "continue":
		case "devin":
		case "cline":
		case "kimi":
		case "muse":
		case "kiro":
		case "kilocode":
		case "vibe":
		case "pi":
		case "autohand":
		case "fake":
			return provider;
		default:
			return "codex";
	}
}
