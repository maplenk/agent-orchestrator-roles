import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { ApiActionError, apiClient } from "../lib/api-client";
import { usesPreviewWorkspaceData } from "../lib/preview-mode";
import type { SessionSwitchTarget } from "../types/workspace";
import { workspaceQueryKey } from "./useWorkspaceQuery";

export type OrchestratorSwitchAction =
	| { kind: "switch"; target: SessionSwitchTarget }
	| { kind: "fresh" };

type SwitchResponse = components["schemas"]["SwitchWorkerResponse"] | undefined;

/**
 * Execute one operator-selected orchestrator lifecycle action. The switch
 * target is an exact pair copied from the session read model; the backend
 * authorizes it again and remains authoritative.
 */
export function useOrchestratorSwitch(
	sessionId: string,
): UseMutationResult<SwitchResponse, Error, OrchestratorSwitchAction> {
	const queryClient = useQueryClient();

	return useMutation<SwitchResponse, Error, OrchestratorSwitchAction>({
		mutationFn: async (action) => {
			if (usesPreviewWorkspaceData) return undefined;
			if (action.kind === "fresh") {
				const { data, error, response } = await apiClient.POST(
					"/api/v1/sessions/{sessionId}/fresh-conversation",
					{ params: { path: { sessionId } }, body: {} },
				);
				if (error) throw new ApiActionError(error, `Failed to start a fresh conversation (${response.status})`);
				return data;
			}

			const { data, error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/switch", {
				params: { path: { sessionId } },
				body: {
					targetHarness: action.target.harness,
					targetModel: action.target.model,
				},
			});
			if (error) throw new ApiActionError(error, `Failed to switch orchestrator (${response.status})`);
			return data;
		},
		onSettled: async () => {
			// Post-stop and uncertain failures can leave a durable fence. Re-read on
			// both success and failure so the UI never trusts only local mutation state.
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
}

