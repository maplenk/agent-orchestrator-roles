import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { agentsQueryKey } from "../hooks/useAgentsQuery";
import { apiClient } from "../lib/api-client";
import { CreateProjectAgentSheet, defaultAuthorizedAgent, RequiredAgentField } from "./CreateProjectAgentSheet";

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: vi.fn(), POST: vi.fn() },
	apiErrorMessage: () => "agent catalog request failed",
}));

const testCatalog = {
	supported: [
		{ id: "claude-code", label: "claude-code" },
		{ id: "codex", label: "codex" },
	],
	installed: [
		{ id: "claude-code", label: "claude-code", authStatus: "authorized" as const },
		{ id: "codex", label: "codex", authStatus: "authorized" as const },
	],
	authorized: [
		{ id: "claude-code", label: "claude-code", authStatus: "authorized" as const },
		{ id: "codex", label: "codex", authStatus: "authorized" as const },
	],
};

function renderSheet(onSubmit = vi.fn().mockResolvedValue(undefined)) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	queryClient.setQueryData(agentsQueryKey, testCatalog);
	render(
		<QueryClientProvider client={queryClient}>
			<CreateProjectAgentSheet
				isCreating={false}
				kind="single_repo"
				onOpenChange={() => undefined}
				onSubmit={onSubmit}
				open={true}
				path="/repo/new-project"
			/>
		</QueryClientProvider>,
	);
	return onSubmit;
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	const escaped = optionName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	await userEvent.click(await screen.findByRole("option", { name: new RegExp(escaped, "i") }));
}

describe("CreateProjectAgentSheet", () => {
	beforeEach(() => {
		vi.clearAllMocks();
		vi.mocked(apiClient.GET).mockResolvedValue({ data: testCatalog, error: undefined } as never);
		vi.mocked(apiClient.POST).mockResolvedValue({ data: testCatalog, error: undefined } as never);
	});

	it("refreshes the provider catalog whenever the sheet opens", async () => {
		renderSheet();

		await waitFor(() => expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/agents/refresh"));
		expect(apiClient.POST).toHaveBeenCalledTimes(1);
	});

	it("chooses the highest-priority authorized default agent", () => {
		expect(
			defaultAuthorizedAgent([
				{ id: "opencode", label: "OpenCode", authStatus: "authorized" },
				{ id: "codex", label: "Codex", authStatus: "authorized" },
			]),
		).toBe("codex");
	});

	it("falls back to the alphabetically first authorized agent when no priority agent is authorized", () => {
		expect(
			defaultAuthorizedAgent([
				{ id: "goose", label: "Goose", authStatus: "authorized" },
				{ id: "devin", label: "Devin", authStatus: "authorized" },
			]),
		).toBe("devin");
	});

	it("uses the compact trigger size for agent fields", () => {
		render(
			<RequiredAgentField
				id="agent"
				label="Agent"
				onChange={() => undefined}
				placeholder="Project default"
				value="claude-code"
			/>,
		);

		expect(screen.getByLabelText("Agent")).toHaveAttribute("data-size", "sm");
	});

	it("caps the agent menu height with a theme token", async () => {
		render(
			<RequiredAgentField id="agent" label="Agent" onChange={() => undefined} placeholder="Project default" value="" />,
		);

		await userEvent.click(screen.getByLabelText("Agent"));

		expect(await screen.findByRole("listbox")).toHaveClass("max-h-select-menu-max!");
	});

	// Muse is a supported harness that the daemon reports as installed, so the
	// picker offers it like any other. Which interfaces it can run is a SERVER
	// capability answered at spawn (SESSION_MODE_UNSUPPORTED for Chat) — a
	// harness list that pre-filtered on it would hide an agent that works fine in
	// Terminal UI, on a fact the renderer does not hold.
	it("offers an installed Muse as a selectable harness", async () => {
		const catalog = [
			{ id: "codex", label: "codex", authStatus: "authorized" as const },
			{ id: "muse", label: "muse", authStatus: "authorized" as const },
		];
		render(
			<RequiredAgentField
				id="agent"
				label="Agent"
				onChange={() => undefined}
				placeholder="Project default"
				value=""
				supported={catalog}
				installed={catalog}
				authorized={catalog}
			/>,
		);

		await userEvent.click(screen.getByLabelText("Agent"));

		const muse = await screen.findByRole("option", { name: /muse/i });
		expect(muse).not.toHaveAttribute("aria-disabled", "true");
		// Authorized agents carry no status chip; a "Needs install"/"Needs auth"
		// badge here would mean the catalog was ignored.
		expect(within(muse).queryByText(/Needs/)).not.toBeInTheDocument();
	});

	it("creates without intake when the toggle is left off", async () => {
		const onSubmit = renderSheet();

		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit).toHaveBeenCalledWith({
			workerAgent: "claude-code",
			orchestratorAgent: "claude-code",
			trackerIntake: undefined,
		});
	});

	it("blocks submit when intake is enabled with no assignee, then passes the intake payload once one is set", async () => {
		const onSubmit = renderSheet();
		await chooseOption(screen.getByLabelText("Worker agent"), "claude-code");
		await chooseOption(screen.getByLabelText("Orchestrator agent"), "codex");

		await userEvent.click(screen.getByLabelText("Enable issue intake"));
		// Enabled with no eligibility rule → submit stays disabled (compact sheet
		// carries no inline guard prose; gating is the disabled button).
		expect(screen.getByRole("button", { name: "Create and start" })).toBeDisabled();

		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");
		await userEvent.click(screen.getByRole("button", { name: "Create and start" }));

		await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
		expect(onSubmit).toHaveBeenCalledWith({
			workerAgent: "claude-code",
			orchestratorAgent: "codex",
			trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
		});
	});

	it("keeps the create sheet minimal: info tooltip instead of prose, no repo row or credential hint", async () => {
		renderSheet();
		// Info affordance is present even before enabling; the descriptive prose is not.
		expect(screen.getByLabelText("What does enabling issue intake do?")).toBeInTheDocument();
		expect(screen.queryByText(/Auto-spawn worker sessions from matching tracker issues/)).not.toBeInTheDocument();

		await userEvent.click(screen.getByLabelText("Enable issue intake"));
		expect(screen.queryByText("Repository")).not.toBeInTheDocument();
		expect(screen.queryByText(/Reads credentials from/)).not.toBeInTheDocument();
	});
});
