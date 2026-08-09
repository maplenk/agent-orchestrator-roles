import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";

type GeneratedAgentSwitch = components["schemas"]["AgentSwitch"];
type AgentSwitchOptionsResponse = components["schemas"]["AgentSwitchOptionsResponse"];

export type AgentSwitchTarget = components["schemas"]["AgentSwitchTarget"];

// Keep forward compatibility with newer daemons so unknown errors can fall
// back to a generic label instead of becoming impossible to represent.
export type AgentSwitch = Omit<GeneratedAgentSwitch, "errorCode"> & {
	errorCode?: string;
};

const terminalAgentSwitchStates = new Set<AgentSwitch["state"]>(["completed", "failed"]);

export const agentSwitchesQueryKey = (sessionId: string) => ["session-agent-switches", sessionId] as const;
export const agentSwitchOptionsQueryKey = (sessionId: string) => ["session-agent-switch-options", sessionId] as const;

export function isTerminalAgentSwitch(agentSwitch: AgentSwitch): boolean {
	return terminalAgentSwitchStates.has(agentSwitch.state);
}

export function agentSwitchNeedsRecovery(agentSwitch: AgentSwitch): boolean {
	return agentSwitch.state === "starting_target" && agentSwitch.errorCode === "target_start_unconfirmed";
}

export function agentSwitchNeedsManualDelivery(agentSwitch: AgentSwitch): boolean {
	return agentSwitch.state === "failed" && agentSwitch.errorCode === "delivery_unconfirmed";
}

export function findActiveAgentSwitch(agentSwitches: AgentSwitch[]): AgentSwitch | undefined {
	return agentSwitches.find(
		(agentSwitch) => !isTerminalAgentSwitch(agentSwitch) && !agentSwitchNeedsRecovery(agentSwitch),
	);
}

export function findRecoveryRequiredAgentSwitch(agentSwitches: AgentSwitch[]): AgentSwitch | undefined {
	return agentSwitches.find(agentSwitchNeedsRecovery);
}

export function agentSwitchesRefetchInterval(agentSwitches: AgentSwitch[]): 1_000 | false {
	return findActiveAgentSwitch(agentSwitches) ? 1_000 : false;
}

async function fetchAgentSwitches(sessionId: string): Promise<AgentSwitch[]> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/agent-switches", {
		params: { path: { sessionId } },
	});
	if (error) {
		throw new Error(apiErrorMessage(error, "Unable to load agent switch status"));
	}
	return data?.switches ?? [];
}

async function fetchAgentSwitchOptions(sessionId: string): Promise<AgentSwitchOptionsResponse> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/agent-switch-options", {
		params: { path: { sessionId } },
	});
	if (error || !data) {
		throw new Error(apiErrorMessage(error, "Unable to load agent switch options"));
	}
	return data;
}

export function useAgentSwitches(sessionId: string) {
	return useQuery({
		queryKey: agentSwitchesQueryKey(sessionId),
		enabled: Boolean(sessionId),
		queryFn: () => (usesPreviewWorkspaceData ? Promise.resolve([]) : fetchAgentSwitches(sessionId)),
		// Once a durable saga is active, keep its phase fresh even if the CDC
		// connection is temporarily unavailable. Recovery-required records are
		// intentionally static until an external recovery changes them.
		refetchInterval: (query) =>
			agentSwitchesRefetchInterval((query.state.data as AgentSwitch[] | undefined) ?? []),
		retry: 1,
	});
}

export function useAgentSwitchOptions(sessionId: string) {
	return useQuery({
		queryKey: agentSwitchOptionsQueryKey(sessionId),
		enabled: Boolean(sessionId),
		queryFn: () =>
			usesPreviewWorkspaceData
				? Promise.resolve<AgentSwitchOptionsResponse>({
						available: false,
						current: { harness: "claude-code", model: "" },
						targets: [],
						reason: "no_target",
					})
				: fetchAgentSwitchOptions(sessionId),
		retry: 1,
	});
}
