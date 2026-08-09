import { ArrowLeftRight, FileWarning, LoaderCircle, TriangleAlert, X } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
	createSwitchAgentIdempotencyKey,
	clearSwitchAgentState,
	type SwitchAgentHarness,
	useRecoverAgentSwitch,
	useSwitchAgent,
	useSwitchAgentState,
} from "../hooks/useSwitchAgent";
import {
	findActiveAgentSwitch,
	agentSwitchNeedsManualDelivery,
	findRecoveryRequiredAgentSwitch,
	isTerminalAgentSwitch,
	type AgentSwitch,
	type AgentSwitchTarget,
	useAgentSwitchOptions,
	useAgentSwitches,
} from "../hooks/useAgentSwitches";
import {
	agentSwitchActionErrorCode,
	agentSwitchActionErrorLabelKey,
	agentSwitchOptionsReasonLabelKey,
	agentSwitchStartModeLabelKey,
	agentSwitchStateLabelKey,
} from "../lib/agent-switch-presentation";
import { agentLabel } from "../lib/agent-options";
import type { WorkspaceSession } from "../types/workspace";
import { AgentAvatar } from "./AgentAvatar";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "./ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

export const SWITCH_AGENT_OPTIONS = [
	{ value: "claude-code", label: "Claude Code" },
	{ value: "codex", label: "Codex" },
] as const satisfies ReadonlyArray<{ value: SwitchAgentHarness; label: string }>;

export function canSwitchAgentHarness(value: string): value is SwitchAgentHarness {
	return SWITCH_AGENT_OPTIONS.some((option) => option.value === value);
}

function switchTargetKey(target: AgentSwitchTarget): string {
	return `${target.harness}\u001f${target.model}`;
}

function usedFallbackContext(agentSwitch: AgentSwitch): boolean {
	return (
		agentSwitch.state === "completed" &&
		!agentSwitch.semanticHandoffIncluded &&
		agentSwitch.sourceTranscriptStatus === "unavailable"
	);
}

type SwitchAgentDialogProps = {
	open: boolean;
	session: WorkspaceSession;
	onOpenChange: (open: boolean) => void;
};

export function SwitchAgentDialog({
	open,
	session,
	onOpenChange,
}: SwitchAgentDialogProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const noteId = useId();
	const targetId = useId();
	const historyId = useId();
	const [selectedTargetKey, setSelectedTargetKey] = useState("");
	const [note, setNote] = useState("");
	const [manualDeliveryAcknowledged, setManualDeliveryAcknowledged] = useState(false);
	const switchAgent = useSwitchAgent();
	const recoverAgentSwitch = useRecoverAgentSwitch();
	const switchMutation = useSwitchAgentState(session.id);
	const idempotencyKeyRef = useRef("");
	if (!idempotencyKeyRef.current) {
		idempotencyKeyRef.current =
			switchMutation.error && !agentSwitchActionErrorCode(switchMutation.error)
				? (switchMutation.input?.idempotencyKey ?? createSwitchAgentIdempotencyKey())
				: createSwitchAgentIdempotencyKey();
	}
	const switchesQuery = useAgentSwitches(session.id);
	const optionsQuery = useAgentSwitchOptions(session.id);
	const switches = switchesQuery.data ?? [];
	const authorizedTargets = (optionsQuery.data?.targets ?? []).filter((target) =>
		canSwitchAgentHarness(target.harness),
	);
	const selectedTarget = authorizedTargets.find((target) => switchTargetKey(target) === selectedTargetKey);
	const activeSwitch = findActiveAgentSwitch(switches);
	const recoverySwitch = findRecoveryRequiredAgentSwitch(switches);
	const latestSwitch = switches[0];
	const pendingInput = switchMutation.input;
	const switchInProgress = Boolean(
		!recoverySwitch && (activeSwitch || (switchMutation.isPending && pendingInput)),
	);
	const terminalHistory = switches.filter(isTerminalAgentSwitch).slice(0, 5);
	const manualDeliveryRequired = Boolean(
		(latestSwitch && agentSwitchNeedsManualDelivery(latestSwitch)) ||
			agentSwitchActionErrorCode(switchMutation.error) === "AGENT_SWITCH_DELIVERY_UNCONFIRMED",
	);
	const manualDeliveryBlocking = manualDeliveryRequired && !manualDeliveryAcknowledged;
	const checkingStatus = switchesQuery.isPending || optionsQuery.isPending;
	const statusError = switchesQuery.isError || optionsQuery.isError;
	const optionsUnavailable = Boolean(
		!checkingStatus &&
			(!optionsQuery.data?.available || authorizedTargets.length === 0),
	);
	const switchBlocked = Boolean(
		recoverySwitch ||
			manualDeliveryBlocking ||
			switchInProgress ||
			checkingStatus ||
			statusError ||
			optionsUnavailable,
	);

	useEffect(() => {
		if (selectedTarget && authorizedTargets.some((target) => switchTargetKey(target) === selectedTargetKey)) {
			return;
		}
		setSelectedTargetKey(authorizedTargets[0] ? switchTargetKey(authorizedTargets[0]) : "");
	}, [authorizedTargets, selectedTarget, selectedTargetKey]);

	const resetDraftAttempt = () => {
		idempotencyKeyRef.current = createSwitchAgentIdempotencyKey();
		if (!switchMutation.error) return;
		clearSwitchAgentState(queryClient, session.id);
	};

	const submit = () => {
		if (
			switchMutation.isPending ||
			checkingStatus ||
			activeSwitch ||
			recoverySwitch ||
			!selectedTarget ||
			!canSwitchAgentHarness(selectedTarget.harness)
		) return;
		switchAgent.mutate({
			session,
			targetHarness: selectedTarget.harness,
			targetModel: selectedTarget.model,
			note,
			idempotencyKey: idempotencyKeyRef.current,
		});
		onOpenChange(false);
	};

	const error = switchMutation.error;
	const stateLabel = (agentSwitch: AgentSwitch) => t(agentSwitchStateLabelKey(agentSwitch));
	const startModeLabel = (agentSwitch: AgentSwitch) => {
		const key = agentSwitchStartModeLabelKey(agentSwitch);
		return key ? t(key) : null;
	};

	if (session.kind !== "worker") return null;

	return (
		<Dialog open={open} onOpenChange={onOpenChange}>
			<DialogContent showCloseButton={false} className={settingsDialogContentClass}>
				<DialogClose asChild>
					<button
						type="button"
						className="settings-dialog-close-button settings-close-button"
						aria-label={t("switchAgent.close")}
					>
						<X className="size-5" aria-hidden="true" />
					</button>
				</DialogClose>

				<form
					className="contents"
					onSubmit={(event) => {
						event.preventDefault();
						submit();
					}}
				>
					<div className={settingsDialogHeaderClass}>
						<DialogTitle className="settings-dialog-title">{t("switchAgent.title")}</DialogTitle>
						<DialogDescription className="text-control leading-4 text-settings-muted">
							{t("switchAgent.description", { current: agentLabel(session.provider) })}
						</DialogDescription>
					</div>

					<div className={settingsDialogBodyClass}>
						{recoverySwitch ? (
							<div
								aria-label={t("switchAgent.recovery.action")}
								className="flex items-start gap-2 rounded-md border border-warning/35 bg-warning/5 px-3 py-2.5"
								role="alert"
							>
								<TriangleAlert className="mt-0.5 size-icon-sm shrink-0 text-warning" aria-hidden="true" />
								<div className="min-w-0">
									<div className="text-control font-medium text-foreground">
										{t("switchAgent.recovery.title")}
									</div>
									<p className="mt-0.5 text-caption leading-4 text-settings-muted">
										{t("switchAgent.recovery.description")}
									</p>
									<p className="mt-1 text-micro text-settings-muted">
										{t("switchAgent.phaseLabel", { phase: stateLabel(recoverySwitch) })}
									</p>
								</div>
							</div>
						) : manualDeliveryBlocking ? (
							<div
								aria-label={t("switchAgent.delivery.title")}
								className="flex items-start gap-2 rounded-md border border-warning/35 bg-warning/5 px-3 py-2.5"
								role="alert"
							>
								<FileWarning className="mt-0.5 size-icon-sm shrink-0 text-warning" aria-hidden="true" />
								<div>
									<div className="text-control font-medium text-foreground">
										{t("switchAgent.delivery.title")}
									</div>
									<p className="mt-0.5 text-caption leading-4 text-settings-muted">
										{t("switchAgent.delivery.description")}
									</p>
									<p className="mt-1 text-caption leading-4 text-settings-muted">
										{t("switchAgent.delivery.guidance")}
									</p>
								</div>
							</div>
						) : switchInProgress ? (
							<div aria-live="polite" className="flex items-start gap-2 py-1" role="status">
								<LoaderCircle className="mt-0.5 size-icon-sm shrink-0 animate-spin" aria-hidden="true" />
								<div>
									<div className="text-control font-medium text-foreground">
										{t("switchAgent.progressTitle", {
											source: agentLabel(activeSwitch?.fromHarness ?? pendingInput?.session.provider ?? session.provider),
											target: agentLabel(activeSwitch?.targetHarness ?? pendingInput?.targetHarness ?? session.provider),
										})}
									</div>
									<p className="mt-0.5 text-caption text-settings-muted">
										{activeSwitch
											? t("switchAgent.phaseLabel", { phase: stateLabel(activeSwitch) })
											: t("switchAgent.switching")}
									</p>
									{activeSwitch && startModeLabel(activeSwitch) ? (
										<p className="mt-1 text-micro text-settings-muted">
											{startModeLabel(activeSwitch)}
										</p>
									) : null}
								</div>
							</div>
						) : checkingStatus ? (
							<div className="inline-flex items-center gap-2 text-control text-settings-muted" role="status">
								<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								{t("switchAgent.checkingStatus")}
							</div>
						) : statusError ? (
							<p className="text-caption leading-4 text-error" role="alert">
								{t("switchAgent.error.loadStatus")}
							</p>
						) : optionsUnavailable ? (
							<div className="rounded-md border border-border px-3 py-2.5" role="status">
								<div className="text-control font-medium text-foreground">
									{t("switchAgent.unavailable.title")}
								</div>
								<p className="mt-0.5 text-caption leading-4 text-settings-muted">
									{t(agentSwitchOptionsReasonLabelKey(optionsQuery.data?.reason))}
								</p>
							</div>
						) : (
							<>
								<div className="flex flex-col gap-1.5">
									<label className="settings-field-label" htmlFor={targetId}>
										{t("switchAgent.targetLabel")}
									</label>
									<Select
										onValueChange={(value) => {
											if (!authorizedTargets.some((target) => switchTargetKey(target) === value)) return;
											resetDraftAttempt();
											setSelectedTargetKey(value);
										}}
										value={selectedTargetKey}
									>
										<SelectTrigger id={targetId} className="settings-field-control w-full">
											<SelectValue />
										</SelectTrigger>
										<SelectContent
											align="start"
											className="max-h-64 w-(--radix-select-trigger-width) [&_[data-slot=select-scroll-down-button]]:hidden [&_[data-slot=select-scroll-up-button]]:hidden"
											position="popper"
										>
											{authorizedTargets.map((target) => (
													<SelectItem
														className="[&>span:last-child]:w-full"
														key={switchTargetKey(target)}
														value={switchTargetKey(target)}
													>
														<span className="flex w-full items-center gap-2">
															<AgentAvatar className="size-icon-lg" decorative provider={target.harness} />
															<span className="min-w-0 flex-1 truncate">{agentLabel(target.harness)}</span>
															{target.model ? (
																<>
																	<span className="sr-only">, </span>
																	<span className="shrink-0 text-micro text-settings-muted">
																		{target.model}
																	</span>
																</>
															) : null}
														</span>
													</SelectItem>
											))}
										</SelectContent>
									</Select>
								</div>

								<div className="flex flex-col items-start gap-1.5">
									<label className="settings-field-label" htmlFor={noteId}>
										{t("switchAgent.noteLabel")}
									</label>
									<textarea
										id={noteId}
										className="settings-field-control min-h-(--size-textarea-min) resize-y py-2.5"
										maxLength={4096}
										onChange={(event) => {
											resetDraftAttempt();
											setNote(event.target.value);
										}}
										placeholder={t("switchAgent.notePlaceholder")}
										value={note}
									/>
								</div>
							</>
						)}

						{terminalHistory.length > 0 ? (
							<section aria-labelledby={historyId} className="flex flex-col gap-1.5">
								<h3 className="settings-field-label" id={historyId}>
									{t("switchAgent.historyTitle")}
								</h3>
								<ul className="max-h-36 divide-y divide-border/60 overflow-y-auto" data-testid="agent-switch-history">
									{terminalHistory.map((entry) => (
										<li className="flex items-center justify-between gap-3 py-1.5 first:pt-0 last:pb-0" key={entry.id}>
											<div className="min-w-0">
												<div className="truncate text-caption text-foreground/80">
													{t("switchAgent.historyEntry", {
														source: agentLabel(entry.fromHarness),
														target: agentLabel(entry.targetHarness),
													})}
												</div>
												{usedFallbackContext(entry) ? (
													<span className="mt-0.5 inline-flex items-center gap-1 text-micro text-warning/90">
														<FileWarning className="size-3" aria-hidden="true" />
														{t("switchAgent.historyFallbackContext")}
													</span>
												) : null}
												{startModeLabel(entry) ? (
													<span className="mt-0.5 block text-micro text-settings-muted">
														{startModeLabel(entry)}
													</span>
												) : null}
											</div>
											<span className="shrink-0 text-micro text-settings-muted">{stateLabel(entry)}</span>
										</li>
									))}
								</ul>
							</section>
						) : null}

						{error && !manualDeliveryRequired ? (
							<p className="text-caption leading-4 text-error" role="alert">
								{t(agentSwitchActionErrorLabelKey(error))}
							</p>
						) : null}
						{recoverAgentSwitch.error ? (
							<p className="text-caption leading-4 text-error" role="alert">
								{t("switchAgent.recovery.failed")}
							</p>
						) : null}
					</div>

					<div className={settingsDialogFooterClass}>
						<DialogClose asChild>
							<button className="settings-footer-button" type="button">
								{switchBlocked ? t("switchAgent.closeButton") : t("confirm.cancel")}
							</button>
						</DialogClose>
						{!switchBlocked ? (
							<button
								className="settings-footer-button settings-footer-button-primary"
								type="submit"
							>
								<ArrowLeftRight className="size-icon-sm" aria-hidden="true" />
								{t("switchAgent.confirm")}
							</button>
						) : recoverySwitch ? (
							<button
								className="settings-footer-button settings-footer-button-primary"
								disabled={recoverAgentSwitch.isPending}
								onClick={() =>
									recoverAgentSwitch.mutate({ sessionId: session.id, switchId: recoverySwitch.id })
								}
								type="button"
							>
								{recoverAgentSwitch.isPending ? (
									<LoaderCircle className="size-icon-sm animate-spin" aria-hidden="true" />
								) : (
									<TriangleAlert className="size-icon-sm" aria-hidden="true" />
								)}
								{recoverAgentSwitch.isPending
									? t("switchAgent.recovery.recovering")
									: t("switchAgent.recovery.confirm")}
							</button>
						) : manualDeliveryBlocking ? (
							<button
								className="settings-footer-button settings-footer-button-primary"
								onClick={() => {
									resetDraftAttempt();
									setManualDeliveryAcknowledged(true);
								}}
								type="button"
							>
								{t("switchAgent.delivery.confirm")}
							</button>
						) : null}
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
