import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { apiClient, apiErrorMessage } from "@/lib/api-client";
import { workspaceQueryKey } from "@/hooks/useWorkspaceQuery";
import type { WorkspaceSession } from "@/types/workspace";

/**
 * The paused surface, built to docs/roles/PHASE3A_PAUSE_CONTRACT.md.
 *
 * The contract's central point is that a paused session is TWO independent
 * facts — pinned, and agent alive-or-dead — because boot deliberately stops
 * relaunching a paused session, so one whose agent dies stays active with a
 * dead runtime. Resume moves it on the pause axis ONLY; it never starts a
 * process. So Resume and Restart are separate controls with separate copy, and
 * a single button would misrepresent the bottom-right cell.
 *
 * Resume submits the incident id THIS PANEL IS DISPLAYING, never a re-read. An
 * action raised for one incident landing after another replaced it would
 * otherwise resume the wrong one, on evidence nobody looked at — the daemon
 * refuses that with PAUSE_INCIDENT_MISMATCH and the panel surfaces it as
 * "re-read", not "retry".
 */
export function SessionPausePanel({ session }: { session: WorkspaceSession }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<string | null>(null);
	const pause = session.pause;

	// Liveness is an independent fact, read from activity — never inferred from
	// the pause pin.
	const agentDead = session.activity?.state === "exited" || session.isTerminated === true;

	const resume = useMutation({
		mutationFn: async (incidentId: string) => {
			const { error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/resume", {
				params: { path: { sessionId: session.id } },
				body: { incidentId },
			});
			// The daemon's own code and message, not a generic failure:
			// PAUSE_INCIDENT_MISMATCH and RUNTIME_SESSION_CONFLICT are both
			// things the human can act on.
			if (error) throw new Error(apiErrorMessage(error, `Resume failed (${response.status})`));
		},
		onSuccess: async () => {
			setFailure(null);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
		onError: (err: Error) => setFailure(err.message),
	});

	if (!pause) return null;

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
				<dt>{t("inspector.pause.incident")}</dt>
				<dd className="font-mono break-all">{pause.incidentId}</dd>
			</dl>

			{failure ? (
				<p role="alert" className="mt-2 text-xs text-destructive">
					{failure}
				</p>
			) : null}

			<div className="mt-3 flex flex-wrap items-center gap-2">
				<Button
					size="sm"
					disabled={resume.isPending}
					onClick={() => resume.mutate(pause.incidentId)}
					aria-label={t("inspector.pause.resumeAria", { incidentId: pause.incidentId })}
				>
					{t(resume.isPending ? "inspector.pause.resuming" : "inspector.pause.resume")}
				</Button>
				<span className="text-xs text-muted-foreground">
					{t(agentDead ? "inspector.pause.hintStopped" : "inspector.pause.hintRunning")}
				</span>
			</div>
		</section>
	);
}
