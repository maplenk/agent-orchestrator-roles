import { type QueryClient, useMutation, useMutationState, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { ApiActionError, apiClient } from "../lib/api-client";
import type { WorkspaceSession } from "../types/workspace";
import { agentSwitchOptionsQueryKey, agentSwitchesQueryKey, type AgentSwitch } from "./useAgentSwitches";
import { workspaceQueryKey } from "./useWorkspaceQuery";

export type SwitchAgentHarness = components["schemas"]["SwitchAgentRequest"]["targetHarness"];

export type SwitchAgentInput = {
	session: WorkspaceSession;
	targetHarness: SwitchAgentHarness;
	targetModel?: string;
	note: string;
	idempotencyKey: string;
};

export const switchAgentMutationKey = ["switch-agent"] as const;

type SwitchAgentMutationState = {
	error: unknown;
	input?: SwitchAgentInput;
	status: "error" | "idle" | "pending" | "success";
	submittedAt: number;
};

function useSwitchAgentMutations() {
	return useMutationState<SwitchAgentMutationState>({
		filters: { mutationKey: switchAgentMutationKey },
		select: (mutation) => ({
			error: mutation.state.error,
			input: mutation.state.variables as SwitchAgentInput | undefined,
			status: mutation.state.status,
			submittedAt: mutation.state.submittedAt,
		}),
	});
}

export function useSwitchAgentState(sessionId: string) {
	const mutations = useSwitchAgentMutations();
	let latest: SwitchAgentMutationState | undefined;
	let pending: SwitchAgentMutationState | undefined;
	for (const mutation of mutations) {
		if (mutation.input?.session.id !== sessionId) continue;
		if (!latest || mutation.submittedAt > latest.submittedAt) latest = mutation;
		if (
			mutation.status === "pending" &&
			(!pending || mutation.submittedAt > pending.submittedAt)
		) {
			pending = mutation;
		}
	}

	return {
		error: !pending && latest?.status === "error" ? latest.error : null,
		input: pending?.input ?? (latest?.status === "error" ? latest.input : undefined),
		isPending: Boolean(pending),
	};
}

export function clearSwitchAgentState(queryClient: QueryClient, sessionId: string) {
	const mutationCache = queryClient.getMutationCache();
	for (const mutation of mutationCache.findAll({ mutationKey: switchAgentMutationKey })) {
		const input = mutation.state.variables as SwitchAgentInput | undefined;
		if (input?.session.id === sessionId && mutation.state.status !== "pending") {
			mutationCache.remove(mutation);
		}
	}
}

export function createSwitchAgentIdempotencyKey(): string {
	return crypto.randomUUID();
}

type AgentSwitchMutationResponse = {
	data?: { switch: AgentSwitch };
	error?: unknown;
	response?: Response;
};

// These canonical Phase 4 routes are owned by the API lane. Keep the temporary
// cast isolated here so this branch can typecheck without editing generated
// schema.ts; npm run api removes the schema lag when the lanes merge.
async function postCanonicalAgentSwitch(
	sessionId: string,
	body: {
		idempotencyKey: string;
		note?: string;
		targetHarness: SwitchAgentHarness;
		targetModel?: string;
	},
): Promise<AgentSwitchMutationResponse> {
	const post = apiClient.POST as unknown as (
		path: "/api/v1/sessions/{sessionId}/agent-switches",
		options: { params: { path: { sessionId: string } }; body: typeof body },
	) => Promise<AgentSwitchMutationResponse>;
	return post("/api/v1/sessions/{sessionId}/agent-switches", {
		params: { path: { sessionId } },
		body,
	});
}

async function postAgentSwitchRecovery(
	sessionId: string,
	switchId: string,
): Promise<AgentSwitchMutationResponse> {
	const post = apiClient.POST as unknown as (
		path: "/api/v1/sessions/{sessionId}/agent-switches/{switchId}/recover",
		options: { params: { path: { sessionId: string; switchId: string } } },
	) => Promise<AgentSwitchMutationResponse>;
	return post("/api/v1/sessions/{sessionId}/agent-switches/{switchId}/recover", {
		params: { path: { sessionId, switchId } },
	});
}

export function useSwitchAgent() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: switchAgentMutationKey,
		mutationFn: async ({ session, targetHarness, targetModel, note, idempotencyKey }: SwitchAgentInput) => {
			const body: {
				targetHarness: SwitchAgentHarness;
				targetModel?: string;
				note?: string;
				idempotencyKey: string;
			} = { targetHarness, idempotencyKey };
			const normalizedModel = targetModel?.trim();
			if (normalizedModel) body.targetModel = normalizedModel;
			const normalizedNote = note.trim();
			if (normalizedNote) body.note = normalizedNote;

			const { data, error } = await postCanonicalAgentSwitch(session.id, body);
			if (error) {
				throw new ApiActionError(error, "Failed to switch agent");
			}
			return data?.switch;
		},
		onSuccess: (agentSwitch, variables) => {
			if (!agentSwitch) return;
			queryClient.setQueryData<AgentSwitch[]>(
				agentSwitchesQueryKey(variables.session.id),
				(current = []) => [agentSwitch, ...current.filter((entry) => entry.id !== agentSwitch.id)],
			);
		},
		// A post-stop failure can legitimately leave the selected target as the
		// current (exited or delivery-unconfirmed) owner. Always refresh the
		// session projection, even when the mutation surfaces an error.
		onSettled: async (_data, _error, variables) => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
				queryClient.invalidateQueries({ queryKey: agentSwitchOptionsQueryKey(variables.session.id) }),
				queryClient.invalidateQueries({ queryKey: agentSwitchesQueryKey(variables.session.id) }),
			]);
		},
	});
}

export function useRecoverAgentSwitch() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationKey: ["recover-agent-switch"],
		mutationFn: async ({ sessionId, switchId }: { sessionId: string; switchId: string }) => {
			const { data, error } = await postAgentSwitchRecovery(sessionId, switchId);
			if (error) throw new ApiActionError(error, "Failed to recover agent switch");
			if (!data?.switch) throw new Error("Agent switch recovery returned no switch");
			return data.switch;
		},
		onSuccess: (agentSwitch) => {
			queryClient.setQueryData<AgentSwitch[]>(
				agentSwitchesQueryKey(agentSwitch.sessionId),
				(current = []) => [agentSwitch, ...current.filter((entry) => entry.id !== agentSwitch.id)],
			);
		},
		onSettled: async (_data, _error, variables) => {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
				queryClient.invalidateQueries({ queryKey: agentSwitchOptionsQueryKey(variables.sessionId) }),
				queryClient.invalidateQueries({ queryKey: agentSwitchesQueryKey(variables.sessionId) }),
			]);
		},
	});
}
