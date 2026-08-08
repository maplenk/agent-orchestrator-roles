import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft, Play } from "lucide-react";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ApiActionError, apiClient } from "@/lib/api-client";
import { workspaceQueryKey } from "@/hooks/useWorkspaceQuery";
import { useRestartAgent } from "@/hooks/useRestartAgent";
import type { MessageKey } from "@/i18n";
import type { SessionFailoverReason, SessionFailoverTarget, WorkspaceSession } from "@/types/workspace";

/**
 * The paused surface, built to docs/roles/PHASE3A_PAUSE_CONTRACT.md and
 * docs/roles/PHASE3B_MVP_CONTRACT.md §2.
 *
 * The 3A point is that a paused session is TWO independent facts — pinned, and
 * agent alive-or-dead — because boot deliberately stops relaunching a paused
 * session, so one whose agent dies stays active with a dead runtime. Resume
 * moves it on the pause axis ONLY; it never starts a process.
 *
 * 3B adds a third operation on the same pin, so the surface now carries three
 * controls that must never read as variants of one another:
 *
 *   Resume         lift the pin. Starts nothing.
 *   Restart agent  relaunch a dead process on the SAME harness. Stays paused.
 *   Continue with  move to the next failover rung, then lift the pin once the
 *                  target acks. The only one that changes harness/model.
 *
 * Every action submits the incident id THIS PANEL IS DISPLAYING, never a
 * re-read. An action raised for one incident landing after another replaced it
 * would otherwise answer the wrong one, on evidence nobody looked at — and on
 * Continue that would move a session onto a rung for an incident nobody saw.
 * The daemon refuses it with PAUSE_INCIDENT_MISMATCH and the panel surfaces
 * that as "re-read", never as "retry".
 *
 * Nothing here schedules anything. `retryAfter` is information, the failure
 * copy offers no retry, and there is no countdown anywhere.
 */

/** Localized copy for every `reason` the read model can carry (contract §9). */
const failoverReasonKeys: Record<SessionFailoverReason, MessageKey> = {
	"": "inspector.pause.failoverUnavailable",
	no_role_pin: "inspector.pause.failoverNoRolePin",
	no_ladder: "inspector.pause.failoverNoLadder",
	ladder_exhausted: "inspector.pause.failoverLadderExhausted",
	limit_reached: "inspector.pause.failoverLimitReached",
	not_paused: "inspector.pause.failoverNotPaused",
	switch_unsupported: "inspector.pause.failoverSwitchUnsupported",
	// Not a verdict about the ladder: the daemon could not COMPUTE the preview
	// for this session. Worth its own copy rather than reusing "no targets",
	// because "we could not find out" and "there is nothing" are different
	// facts, and only one of them is worth re-reading later.
	unavailable: "inspector.pause.failoverPreviewUnavailable",
};

/**
 * What the human should understand about a failure, keyed by the daemon's code
 * (contract §4). This is guidance the daemon's message cannot carry — whether
 * the pause survived, and what to do next. No entry says "retry": nothing in AO
 * retries on its own, and offering one here would reintroduce the automatic
 * restart the pause exists to prevent.
 */
const failureGuidanceKeys: Record<string, MessageKey> = {
	PAUSE_INCIDENT_REQUIRED: "inspector.pause.errorIncidentRequired",
	PAUSE_INCIDENT_INVALID: "inspector.pause.errorIncidentInvalid",
	PAUSE_INCIDENT_MISMATCH: "inspector.pause.errorIncidentMismatch",
	SESSION_NOT_PAUSED: "inspector.pause.errorNotPaused",
	SESSION_TERMINATED: "inspector.pause.errorTerminated",
	PAUSE_AUTH_REQUIRED: "inspector.pause.errorAuthRequired",
	PAUSE_AGENT_FORBIDDEN: "inspector.pause.errorAgentForbidden",
	OPERATOR_CREDENTIAL_INVALID: "inspector.pause.errorCredentialInvalid",
	SWITCH_NOT_SUPPORTED: "inspector.pause.errorSwitchUnsupported",
	SWITCH_CHAT_UNSUPPORTED: "inspector.pause.errorChatUnsupported",
	SWITCH_IN_PROGRESS: "inspector.pause.errorSwitchInProgress",
	NOT_A_WORKER: "inspector.pause.errorNotAWorker",
	ROLE_MAP_REQUIRED: "inspector.pause.errorRoleMapRequired",
	ROLE_NOT_IN_MAP: "inspector.pause.errorRoleNotInMap",
	FAILOVER_NO_TARGET: "inspector.pause.errorNoTarget",
	FAILOVER_ROLE_REQUIRED: "inspector.pause.errorRoleRequired",
	FAILOVER_LIMIT_REACHED: "inspector.pause.errorLimitReached",
	FAILOVER_RECOVERY_REQUIRED: "inspector.pause.errorRecoveryRequired",
};

/**
 * Label for the Continue button's target.
 *
 * Both halves come from the read model verbatim. This joins two backend strings
 * for display; it does NOT map a harness id to a product name (SessionInspector
 * has a `formatHarnessName` for its own chrome — applying it here would let the
 * button claim a target the host never authorized) and it does NOT resolve a
 * ladder. An empty model means "provider default", so the harness stands alone.
 */
function failoverTargetLabel(target: SessionFailoverTarget): string {
	return target.model ? `${target.harness} · ${target.model}` : target.harness;
}

type PanelFailure = { message: string; code?: string };

export function SessionPausePanel({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<PanelFailure | null>(null);
	const reasonId = useId();
	// A second request must be impossible, not merely unlikely: `disabled` alone
	// depends on a re-render landing between two clicks.
	const inFlight = useRef(false);
	const pause = session.pause;
	const failover = session.failover;

	// Liveness is an independent fact, read from activity — never inferred from
	// the pause pin.
	const agentDead = session.activity?.state === "exited" || session.isTerminated === true;
	// Restart is offered only where a process actually died. On a live agent it
	// would be an offer to relaunch something that never stopped, so it is
	// absent rather than reworded (PHASE3A_PAUSE_CONTRACT §2).
	const canRestart = session.activity?.state === "exited" && session.isTerminated !== true;

	const restart = useRestartAgent(session.id);

	const resume = useMutation({
		mutationFn: async (incidentId: string) => {
			const { error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/resume", {
				params: { path: { sessionId: session.id } },
				body: { incidentId },
			});
			// The daemon's own code and message, not a generic failure:
			// PAUSE_INCIDENT_MISMATCH and RUNTIME_SESSION_CONFLICT are both
			// things the human can act on.
			if (error) throw new ApiActionError(error, `Resume failed (${response.status})`);
		},
		onSuccess: async () => {
			setFailure(null);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});

	const continueOn = useMutation({
		mutationFn: async (incidentId: string) => {
			// The body carries the incident and NOTHING else. No harness, no model,
			// no role: the host picks the rung (contract §3), so a free-form target
			// is structurally impossible from here rather than merely rejected —
			// the generated request type has nowhere to put one.
			const { error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/continue", {
				params: { path: { sessionId: session.id } },
				body: { incidentId },
			});
			if (error) throw new ApiActionError(error, `Continue failed (${response.status})`);
		},
		onSuccess: async () => {
			setFailure(null);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});

	if (!pause) return null;

	// THE displayed incident id. One value feeds the description list and every
	// submission, so what a human read is provably what the daemon is answering.
	const displayedIncidentId = pause.incidentId;

	const busy = resume.isPending || restart.isPending || continueOn.isPending;
	const settle = () => {
		inFlight.current = false;
	};
	const claim = (): boolean => {
		if (busy || inFlight.current) return false;
		inFlight.current = true;
		// A failure describes the action that produced it. Leaving it on screen
		// under a different control's result would misattribute it.
		setFailure(null);
		return true;
	};
	const fail = (error: Error) =>
		setFailure({ message: error.message, code: error instanceof ApiActionError ? error.code : undefined });

	const target = failover?.nextTarget ?? null;
	// `available` and a target are one question: a block that claims availability
	// without naming a rung cannot label a button, and the renderer may not
	// invent one.
	const canContinue = failover?.available === true && target !== null;
	const guidanceKey = failure?.code ? failureGuidanceKeys[failure.code] : undefined;

	return (
		<section aria-labelledby="session-pause-heading" className="rounded-md border border-border bg-muted/40 p-3">
			<div className="flex items-center gap-2">
				<h3 id="session-pause-heading" className="text-sm font-medium">
					{t("inspector.pause.title")}
				</h3>
				<Badge variant="warning">{t(pause.reason === "usage_limit" ? "inspector.pause.reasonUsageLimit" : "inspector.pause.reasonOperator")}</Badge>
				{/* The two paused cells read differently and need different action. */}
				<Badge variant={agentDead ? "error" : "outline"}>{t(agentDead ? "inspector.pause.agentStopped" : "inspector.pause.agentRunning")}</Badge>
			</div>

			<dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs text-muted-foreground">
				<dt>{t("inspector.pause.detectedBy")}</dt>
				<dd>
					{t(
						pause.detectedBy === "structured_envelope"
							? "inspector.pause.detectedStructured"
							: "inspector.pause.detectedOperator",
					)}
				</dd>
				{pause.harness ? (
					<>
						<dt>{t("inspector.pause.harness")}</dt>
						<dd>{pause.harness}</dd>
					</>
				) : null}
				<dt>{t("inspector.pause.since")}</dt>
				<dd>
					<time dateTime={pause.pausedAt}>{new Date(pause.pausedAt).toLocaleString()}</time>
				</dd>
				{pause.retryAfter ? (
					<>
						<dt>{t("inspector.pause.providerWindow")}</dt>
						{/* Deliberately NOT a countdown. Nothing schedules against this;
						    rendering a timer would imply an automatic resume that does
						    not exist. */}
						<dd>
							{t("inspector.pause.reopensAt")} <time dateTime={pause.retryAfter}>{new Date(pause.retryAfter).toLocaleString()}</time>
							<span className="ml-1">{t("inspector.pause.notAutomatic")}</span>
						</dd>
					</>
				) : null}
				{failover && failover.roleId ? (
					<>
						{/* The role is invariant across a Continue (contract §6 rule 7) —
						    worth showing precisely because the harness is not. */}
						<dt>{t("inspector.pause.role")}</dt>
						<dd className="font-mono break-all">{failover.roleId}</dd>
					</>
				) : null}
				{failover && failover.maxAttempts > 0 ? (
					<>
						<dt>{t("inspector.pause.failoverAttempts")}</dt>
						<dd>
							{t("inspector.pause.failoverAttemptsValue", {
								used: failover.attemptsUsed,
								max: failover.maxAttempts,
							})}
						</dd>
					</>
				) : null}
				<dt>{t("inspector.pause.incident")}</dt>
				<dd className="font-mono break-all" data-testid="session-pause-incident">
					{displayedIncidentId}
				</dd>
			</dl>

			{failure ? (
				<div role="alert" className="mt-2 text-xs text-destructive">
					<p>{failure.message}</p>
					{guidanceKey ? <p className="mt-1 text-muted-foreground">{t(guidanceKey)}</p> : null}
				</div>
			) : null}

			{/* Three controls, three treatments, one row. Never a menu: collapsing
			    them would put "lift the pin", "relaunch the process" and "move to
			    another harness" behind the same affordance. */}
			<div className="mt-3 flex flex-wrap items-center gap-2">
				<Button
					size="sm"
					disabled={busy}
					onClick={() => {
						if (!claim()) return;
						resume.mutate(displayedIncidentId, { onError: fail, onSettled: settle });
					}}
					aria-label={t("inspector.pause.resumeAria", { incidentId: displayedIncidentId })}
				>
					{t(resume.isPending ? "inspector.pause.resuming" : "inspector.pause.resume")}
				</Button>

				{canRestart ? (
					<Button
						size="sm"
						variant="outline"
						disabled={busy}
						onClick={() => {
							if (!claim()) return;
							restart.mutate(undefined, { onError: fail, onSettled: settle });
						}}
						type="button"
					>
						<Play className="size-icon-sm" aria-hidden="true" />
						{restart.isPending ? t("inspector.resumingAgent") : t("inspector.resumeAgent")}
					</Button>
				) : null}

				{failover ? (
					<Button
						size="sm"
						variant="secondary"
						disabled={busy || !canContinue}
						aria-describedby={canContinue ? undefined : reasonId}
						onClick={() => {
							if (!canContinue || !claim()) return;
							// The DISPLAYED incident, not failover.incidentId and not a
							// re-read of the pin.
							continueOn.mutate(displayedIncidentId, { onError: fail, onSettled: settle });
						}}
						type="button"
						aria-label={
							target
								? t("inspector.pause.continueAria", {
										target: failoverTargetLabel(target),
										incidentId: displayedIncidentId,
									})
								: t("inspector.pause.continueNoTarget")
						}
					>
						<ArrowRightLeft className="size-icon-sm" aria-hidden="true" />
						{continueOn.isPending
							? t("inspector.pause.continuing")
							: target
								? t("inspector.pause.continue", { target: failoverTargetLabel(target) })
								: t("inspector.pause.continueNoTarget")}
					</Button>
				) : null}
			</div>

			<p className="mt-2 text-xs text-muted-foreground">
				{t(agentDead ? "inspector.pause.hintStopped" : "inspector.pause.hintRunning")}
			</p>

			{failover ? (
				// A disabled control that does not say why is a dead end: the reason
				// is machine-readable in the read model precisely so this line can be
				// specific without the renderer inventing prose.
				<p className="mt-1 text-xs text-muted-foreground" id={reasonId}>
					{canContinue ? t("inspector.pause.continueHint") : t(failoverReasonKeys[failover.reason])}
				</p>
			) : null}
		</section>
	);
}
