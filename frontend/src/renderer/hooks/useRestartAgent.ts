import { useMutation, useQueryClient, type UseMutationResult } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { ApiActionError, apiClient } from "../lib/api-client";
import { aoBridge } from "../lib/bridge";
import { usesPreviewWorkspaceData as usePreviewData } from "../lib/preview-mode";
import { workspaceQueryKey } from "./useWorkspaceQuery";

type ResumeAgentResponse = { resumeMode?: string } | undefined;

/**
 * "Restart agent" — relaunch a process for a session whose agent is gone, on the
 * SAME harness (PHASE3A_PAUSE_CONTRACT §2, PHASE3B_MVP_CONTRACT §2).
 *
 * Extracted from SessionInspector because two surfaces now offer this control
 * and they must be the same operation, not two implementations that drift:
 *
 *  - the Activity section, for an exited session that is not paused;
 *  - the pause panel, where it sits beside Resume and Continue so the three
 *    distinct operations read as one set.
 *
 * Sharing the mutation is also what lets the pause panel disable Restart while a
 * Continue is in flight — a caller holding the returned mutation can read
 * `isPending` and pass its own `disabled` — and keeps the saved-prompt fallback
 * notification in exactly one place.
 *
 * It deliberately does NOT touch the pause pin. Restarting a paused session
 * leaves it paused; lifting the pin is Resume's job and nothing else's.
 */
export function useRestartAgent(sessionId: string): UseMutationResult<ResumeAgentResponse, Error, void> {
	const { t } = useTranslation();
	const queryClient = useQueryClient();

	return useMutation<ResumeAgentResponse, Error, void>({
		mutationFn: async () => {
			if (usePreviewData) return undefined;
			const { data, error, response } = await apiClient.POST("/api/v1/sessions/{sessionId}/resume-agent", {
				params: { path: { sessionId } },
			});
			if (error) throw new ApiActionError(error, `Failed to resume agent (${response.status})`);
			return data;
		},
		onSuccess: async (data) => {
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			if (data?.resumeMode === "saved_prompt") {
				void aoBridge.notifications
					.show({
						id: `resume-agent-fallback:${sessionId}:${Date.now()}`,
						title: t("inspector.startedFromPrompt"),
						body: t("inspector.resumeFallbackBody"),
					})
					.catch((err) => {
						console.warn("Unable to show resume fallback notification", err);
					});
			}
		},
	});
}
