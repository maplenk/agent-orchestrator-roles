import type { TFunction } from "i18next";
import { ArrowRightLeft, ChevronDown, Loader2, RefreshCw } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { OrchestratorSwitchAction } from "../hooks/useOrchestratorSwitch";
import { apiErrorCode, apiErrorMessage } from "../lib/api-client";
import type { SessionSwitchTarget, SessionSwitchView } from "../types/workspace";
import { TopbarButton, TopbarKillError } from "./TopbarButton";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

type SwitchMutation = {
	isPending: boolean;
	variables?: OrchestratorSwitchAction;
	error: Error | null;
	reset: () => void;
	mutate: (action: OrchestratorSwitchAction) => void;
};

function harnessLabel(harness: string): string {
	if (harness === "claude-code") return "Claude Code";
	if (harness === "codex") return "Codex";
	return harness;
}

function errorMessage(error: Error, t: TFunction): string {
	switch (apiErrorCode(error)) {
		case "SWITCH_IN_PROGRESS":
			return t("orchestrator.switch.errorInProgress");
		case "SWITCH_POST_STOP":
			return t("orchestrator.switch.errorPostStop");
		case "SWITCH_UNCERTAIN":
			return t("orchestrator.switch.errorUncertain");
		case "SWITCH_NOT_SUPPORTED":
		case "SWITCH_CHAT_UNSUPPORTED":
		case "ORCHESTRATOR_CROSS_HARNESS_UNSUPPORTED":
			return t("orchestrator.switch.errorUnsupported");
		case "SWITCH_PAUSED":
			return t("orchestrator.switch.paused");
		case "SWITCH_TARGET_UNAUTHORIZED":
		case "TARGET_MODEL_REQUIRED":
			return t("orchestrator.switch.errorUnauthorized");
		default:
			return apiErrorMessage(error);
	}
}

function reasonMessage(reason: SessionSwitchView["reason"] | undefined, t: TFunction): string {
	switch (reason) {
		case "no_role_pin":
			return t("orchestrator.switch.noRolePin");
		case "no_role_map":
			return t("orchestrator.switch.noRoleMap");
		case "role_not_in_map":
			return t("orchestrator.switch.roleMissing");
		case "no_target":
			return t("orchestrator.switch.noTarget");
		case "in_progress":
			return t("orchestrator.switch.inProgress");
		case "paused":
			return t("orchestrator.switch.paused");
		case "terminated":
			return t("orchestrator.switch.terminated");
		case "unavailable":
		case undefined:
			return t("orchestrator.switch.unavailable");
		default:
			return "";
	}
}

export function OrchestratorSwitchControl({
	switchState,
	mutation,
	disabled,
}: {
	switchState?: SessionSwitchView;
	mutation: SwitchMutation;
	disabled?: boolean;
}) {
	const { t } = useTranslation();
	const durablePending = switchState?.pending;
	const localAction = mutation.variables;
	const pending = mutation.isPending || Boolean(durablePending);
	const pendingTarget = durablePending?.to ?? (localAction?.kind === "switch" ? localAction.target : undefined);
	const pendingCopy =
		localAction?.kind === "fresh" || durablePending?.kind === "orchestrator_fresh_conversation"
			? t("orchestrator.switch.freshPending")
			: t("orchestrator.switch.switchingTo", {
					target: pendingTarget ? harnessLabel(pendingTarget.harness) : t("orchestrator.switch.target"),
				});
	const unavailable = reasonMessage(switchState?.reason, t);
	const switchDisabled = disabled || pending || !switchState?.available || switchState.targets.length === 0;
	const freshDisabled = disabled || pending;
	const error = mutation.error ? errorMessage(mutation.error, t) : "";
	const targetLabel = (target: SessionSwitchTarget) =>
		`${harnessLabel(target.harness)} · ${target.model || t("orchestrator.switch.providerDefault")}`;

	if (pending) {
		return (
			<>
				<div
					className="flex h-control-lg items-center gap-1.5 rounded-md border border-border bg-raised px-2.5 text-xs text-muted-foreground"
					role="status"
					aria-live="polite"
					title={durablePending?.generationId || pendingCopy}
				>
					<Loader2 aria-hidden="true" className="size-icon-sm animate-spin" />
					<span className="whitespace-nowrap">{pendingCopy}</span>
				</div>
				{error ? (
					<TopbarKillError>{error}</TopbarKillError>
				) : durablePending ? (
					<span className="max-w-64 text-caption text-muted-foreground" role="note">
						{t("orchestrator.switch.pendingRecovery")}
					</span>
				) : null}
			</>
		);
	}

	return (
		<>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<TopbarButton
						aria-label={t("orchestrator.switch.action")}
						disabled={switchDisabled}
						title={switchDisabled ? unavailable : t("orchestrator.switch.action")}
						variant="accent"
					>
						<ArrowRightLeft aria-hidden="true" className="size-icon-sm" />
						{t("orchestrator.switch.action")}
						<ChevronDown aria-hidden="true" className="size-icon-sm" />
					</TopbarButton>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end">
					<DropdownMenuLabel>{t("orchestrator.switch.chooseTarget")}</DropdownMenuLabel>
					{switchState?.targets.map((target) => (
						<DropdownMenuItem
							key={`${target.harness}\u0000${target.model}`}
							onSelect={() => {
								mutation.reset();
								mutation.mutate({ kind: "switch", target });
							}}
						>
							<ArrowRightLeft aria-hidden="true" />
							{targetLabel(target)}
						</DropdownMenuItem>
					))}
				</DropdownMenuContent>
			</DropdownMenu>
			<TopbarButton
				aria-label={t("orchestrator.switch.freshAction")}
				disabled={freshDisabled}
				onClick={() => {
					mutation.reset();
					mutation.mutate({ kind: "fresh" });
				}}
				title={t("orchestrator.switch.freshHint")}
				variant="accent"
			>
				<RefreshCw aria-hidden="true" className="size-icon-sm" />
				{t("orchestrator.switch.freshAction")}
			</TopbarButton>
			{!switchState?.available && unavailable ? (
				<span className="max-w-64 text-caption text-muted-foreground" role="status">
					{unavailable}
				</span>
			) : null}
			{error ? <TopbarKillError>{error}</TopbarKillError> : null}
		</>
	);
}
