import type { MessageKey } from "../i18n/messages";
import { agentSwitchErrorLabelKeys, type AgentSwitchErrorCode } from "../i18n/key-maps";
import type { AgentSwitch, AgentSwitchOptionsReason } from "../hooks/useAgentSwitches";

const agentSwitchStateLabelKeys: Record<AgentSwitch["state"], MessageKey> = {
	preparing_handoff: "switchAgent.state.preparingHandoff",
	stopping_source: "switchAgent.state.stoppingSource",
	source_stopped: "switchAgent.state.sourceStopped",
	starting_target: "switchAgent.state.startingTarget",
	target_ready: "switchAgent.state.targetReady",
	delivering_context: "switchAgent.state.deliveringContext",
	completed: "switchAgent.state.completed",
	failed: "switchAgent.state.failed",
};

const actionErrorLabelKeys: Record<string, MessageKey> = {
	AGENT_BINARY_NOT_FOUND: "switchAgent.error.targetBinaryMissing",
	AGENT_SWITCH_DELIVERY_UNCONFIRMED: "switchAgent.error.deliveryUnconfirmed",
	AGENT_SWITCH_IDEMPOTENCY_CONFLICT: "switchAgent.error.idempotencyConflict",
	AGENT_SWITCH_IN_PROGRESS: "switchAgent.error.inProgress",
	AGENT_SWITCH_RECOVERY_REQUIRED: "switchAgent.error.recoveryRequired",
	ALREADY_USING_HARNESS: "switchAgent.error.alreadyCurrent",
	LAUNCH_COMMAND_TOO_LONG: "switchAgent.error.launchTooLong",
	SWITCH_CHAT_UNSUPPORTED: "switchAgent.error.chatUnsupported",
	SWITCH_IN_PROGRESS: "switchAgent.error.inProgress",
	SWITCH_NOT_SUPPORTED: "switchAgent.error.unsupported",
	SWITCH_PAUSED: "switchAgent.error.paused",
	SWITCH_POST_STOP: "switchAgent.error.recoveryRequired",
	SWITCH_RECOVERY_REQUIRED: "switchAgent.error.recoveryRequired",
	SWITCH_TARGET_UNAUTHORIZED: "switchAgent.error.targetUnauthorized",
	SWITCH_UNCERTAIN: "switchAgent.error.recoveryRequired",
	TARGET_AGENT_UNAUTHORIZED: "switchAgent.error.targetAgentUnauthorized",
	TARGET_MODEL_REQUIRED: "switchAgent.error.targetModelRequired",
	UNSUPPORTED_SWITCH_HARNESS: "switchAgent.error.unsupported",
	WORKER_SESSION_REQUIRED: "switchAgent.error.workerOnly",
};

const optionsReasonLabelKeys: Record<AgentSwitchOptionsReason, MessageKey> = {
	"": "switchAgent.error.unknown",
	worker_session_required: "switchAgent.error.workerOnly",
	terminated: "switchAgent.error.sourceSessionTerminated",
	paused: "switchAgent.error.paused",
	agent_switch_in_progress: "switchAgent.error.inProgress",
	switch_chat_unsupported: "switchAgent.error.chatUnsupported",
	role_pin_required: "switchAgent.unavailable.rolePinRequired",
	role_map_required: "switchAgent.unavailable.roleMapRequired",
	role_not_in_map: "switchAgent.unavailable.roleNotInMap",
	no_target: "switchAgent.unavailable.noTarget",
};

export function agentSwitchStateLabelKey(agentSwitch: AgentSwitch): MessageKey {
	if (agentSwitch.state === "failed") {
		if (!agentSwitch.errorCode) return "switchAgent.state.failed";
		return (
			agentSwitchErrorLabelKeys[agentSwitch.errorCode as AgentSwitchErrorCode] ??
			"switchAgent.error.unknown"
		);
	}
	return agentSwitchPhaseLabelKey(agentSwitch.state);
}

export function agentSwitchPhaseLabelKey(state: AgentSwitch["state"]): MessageKey {
	return agentSwitchStateLabelKeys[state] ?? "switchAgent.state.unknown";
}

export function agentSwitchStartModeLabelKey(agentSwitch: AgentSwitch): MessageKey | undefined {
	if (agentSwitch.targetStartMode === "resumed") return "switchAgent.startMode.retained";
	if (agentSwitch.targetStartMode === "fresh") return "switchAgent.startMode.fresh";
	return undefined;
}

export function agentSwitchActionErrorLabelKey(error: unknown): MessageKey {
	const code =
		typeof error === "object" && error !== null && "code" in error
			? (error as { code?: unknown }).code
			: undefined;
	return typeof code === "string"
		? (actionErrorLabelKeys[code] ?? "switchAgent.error.unknown")
		: "switchAgent.error.unknown";
}

export function agentSwitchActionErrorCode(error: unknown): string | undefined {
	if (typeof error !== "object" || error === null || !("code" in error)) return undefined;
	const code = (error as { code?: unknown }).code;
	return typeof code === "string" && code !== "" ? code : undefined;
}

export function agentSwitchOptionsReasonLabelKey(reason: string | undefined): MessageKey {
	return optionsReasonLabelKeys[reason as AgentSwitchOptionsReason] ?? "switchAgent.error.unknown";
}
