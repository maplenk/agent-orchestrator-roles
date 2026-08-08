import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: postMock },
	ApiActionError: class ApiActionError extends Error {},
}));
vi.mock("../lib/preview-mode", () => ({ usesPreviewWorkspaceData: false }));

import { useOrchestratorSwitch } from "./useOrchestratorSwitch";

function wrapper({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({
		defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
	});
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

beforeEach(() => {
	postMock.mockReset().mockResolvedValue({
		data: { session: {} },
		error: undefined,
		response: { status: 200 },
	});
});

describe("useOrchestratorSwitch", () => {
	it("submits the exact read-model harness and model pair", async () => {
		const { result } = renderHook(() => useOrchestratorSwitch("orch-1"), { wrapper });

		await act(() =>
			result.current.mutateAsync({
				kind: "switch",
				target: { harness: "codex", model: "" },
			}),
		);

		expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/switch", {
			params: { path: { sessionId: "orch-1" } },
			body: { targetHarness: "codex", targetModel: "" },
		});
	});

	it("uses the distinct fresh-conversation operation", async () => {
		const { result } = renderHook(() => useOrchestratorSwitch("orch-1"), { wrapper });

		await act(() => result.current.mutateAsync({ kind: "fresh" }));

		expect(postMock).toHaveBeenCalledWith(
			"/api/v1/sessions/{sessionId}/fresh-conversation",
			{ params: { path: { sessionId: "orch-1" } }, body: {} },
		);
	});
});
