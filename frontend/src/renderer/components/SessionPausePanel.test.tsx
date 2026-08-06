import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SessionPausePanel } from "./SessionPausePanel";
import type { SessionPause, WorkspaceSession } from "@/types/workspace";

const post = vi.fn();
vi.mock("@/lib/api-client", async () => {
	const actual = await vi.importActual<typeof import("@/lib/api-client")>("@/lib/api-client");
	return { ...actual, apiClient: { POST: (...args: unknown[]) => post(...args) } };
});

function session(overrides: Partial<WorkspaceSession> = {}): WorkspaceSession {
	return {
		id: "proj-1",
		workspaceId: "proj",
		workspaceName: "proj",
		title: "worker",
		provider: "claude-code",
		status: "idle",
		updatedAt: new Date().toISOString(),
		activity: { state: "idle", lastActivityAt: "" },
		...overrides,
	} as WorkspaceSession;
}

const pin: SessionPause = {
	incidentId: "limit-abc123",
	reason: "usage_limit",
	detectedBy: "structured_envelope",
	harness: "codex",
	pausedAt: "2026-08-06T12:00:00Z",
	retryAfter: "2026-08-06T18:00:00Z",
};

function renderPanel(s: WorkspaceSession) {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={qc}>
			<SessionPausePanel session={s} />
		</QueryClientProvider>,
	);
}

beforeEach(() => post.mockReset());

describe("SessionPausePanel", () => {
	it("renders nothing when the session is not paused", () => {
		const { container } = renderPanel(session());
		expect(container).toBeEmptyDOMElement();
	});

	it("shows reason, harness, incident, time and retry information", () => {
		renderPanel(session({ pause: pin }));
		expect(screen.getByText("Usage limit")).toBeInTheDocument();
		expect(screen.getByText("Harness (structured report)")).toBeInTheDocument();
		expect(screen.getByText("codex")).toBeInTheDocument();
		expect(screen.getByText("limit-abc123")).toBeInTheDocument();
		// retryAfter is INFORMATION. Rendering it as a countdown would imply an
		// automatic resume that does not exist.
		expect(screen.getByText(/informational — AO will not resume on its own/)).toBeInTheDocument();
	});

	it("distinguishes paused-with-a-live-agent from paused-with-a-dead-one", () => {
		const live = renderPanel(session({ pause: pin, activity: { state: "idle", lastActivityAt: "" } }));
		expect(screen.getByText("Agent running")).toBeInTheDocument();
		live.unmount();

		renderPanel(session({ pause: pin, activity: { state: "exited", lastActivityAt: "" } }));
		expect(screen.getByText("Agent stopped")).toBeInTheDocument();
		// Resume must not imply restart on the dead cell — that is the whole
		// reason the contract insists on separate controls.
		expect(screen.getByText(/restarting it is a separate step/)).toBeInTheDocument();
	});

	it("submits the incident id it is DISPLAYING", async () => {
		post.mockResolvedValue({ error: undefined, response: { status: 200 } });
		renderPanel(session({ pause: pin }));

		await userEvent.click(screen.getByRole("button", { name: /Resume this session/ }));
		await waitFor(() => expect(post).toHaveBeenCalledTimes(1));

		const [path, opts] = post.mock.calls[0] as [string, { body: { incidentId: string } }];
		expect(path).toBe("/api/v1/sessions/{sessionId}/resume");
		// Re-reading the current pin at submit time is the stale-resume bug: an
		// action raised for one incident landing after another replaced it would
		// resume the wrong one.
		expect(opts.body.incidentId).toBe("limit-abc123");
	});

	it("surfaces the daemon's own code and message on conflict", async () => {
		post.mockResolvedValue({
			error: { code: "PAUSE_INCIDENT_MISMATCH", message: "A different incident now holds this session" },
			response: { status: 409 },
		});
		renderPanel(session({ pause: pin }));

		await userEvent.click(screen.getByRole("button", { name: /Resume this session/ }));
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("A different incident now holds this session");
		// The CODE has to reach the human too, or "re-read" is indistinguishable
		// from "the daemon broke".
		expect(alert).toHaveTextContent("PAUSE_INCIDENT_MISMATCH");
	});

	it("shows a pending state while the request is in flight", async () => {
		let release: (v: unknown) => void = () => {};
		post.mockReturnValue(new Promise((r) => (release = r)));
		renderPanel(session({ pause: pin }));

		const button = screen.getByRole("button", { name: /Resume this session/ });
		await userEvent.click(button);
		await waitFor(() => expect(screen.getByRole("button", { name: /Resume this session/ })).toBeDisabled());
		expect(screen.getByText("Resuming…")).toBeInTheDocument();
		release({ error: undefined, response: { status: 200 } });
	});

	it("labels an operator pause differently from a detected one", () => {
		renderPanel(session({ pause: { ...pin, reason: "operator", detectedBy: "operator", harness: undefined } }));
		expect(screen.getByText("Requested by a person")).toBeInTheDocument();
		expect(screen.queryByText("Harness (structured report)")).not.toBeInTheDocument();
	});
});
