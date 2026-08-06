import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), capture: vi.fn() }));

vi.mock("../hooks/useAgentsQuery", () => ({
	agentsQueryKey: ["agents"],
	agentsQueryOptions: { queryKey: ["agents"], queryFn: async () => ({}) },
	refreshAgents: vi.fn(),
}));

vi.mock("./CreateProjectAgentSheet", () => ({
	RequiredAgentField: () => <div data-testid="agent-field" />,
}));

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: h.get,
		POST: h.post,
	},
	apiErrorCode: (error: { code?: string }) => error?.code,
	apiErrorMessage: (_e: unknown, fallback = "err") => fallback,
}));

vi.mock("../lib/telemetry", () => ({ captureRendererEvent: h.capture }));

import { TaskComposer } from "./TaskComposer";

function Wrap({ children }: { children: ReactNode }) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const task = () => screen.getByPlaceholderText(/Describe the change/i);

beforeEach(() => {
	h.get.mockImplementation(async (path: string) => {
		if (path.includes("/models")) {
			return {
				data: {
					agent: "codex",
					selectionMode: "text",
					models: [],
					allowCustom: true,
					refreshRecommended: false,
				},
			};
		}
		return { data: { status: "ok", project: { config: {} } } };
	});
});

afterEach(() => {
	h.get.mockReset();
	h.post.mockReset();
	h.capture.mockReset();
});

describe("TaskComposer", () => {
	it("emits busy state around an in-flight create and reports the new session", async () => {
		const onSubmittingChange = vi.fn();
		const onCreated = vi.fn();
		let resolveCreate!: (value: { data: { workerId: string } }) => void;
		h.post.mockReturnValueOnce(new Promise((resolve) => (resolveCreate = resolve)));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "Do the thing" } });
		// The composer will not submit until it knows whether the project's role
		// map is strict, so the button is disabled for that first tick.
		await waitFor(() => expect(screen.getByText("Start task").closest("button")).toBeEnabled());
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(true));
		expect(h.post).toHaveBeenCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({
				body: expect.objectContaining({ projectId: "proj-1", brief: "Do the thing" }),
			}),
		);

		await act(async () => resolveCreate({ data: { workerId: "sess-1" } }));
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-1"));
		await waitFor(() => expect(onSubmittingChange).toHaveBeenLastCalledWith(false));
	});

	it("clears busy state when a create rejects", async () => {
		const onSubmittingChange = vi.fn();
		h.post.mockRejectedValueOnce(new Error("nope"));

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onSubmittingChange={onSubmittingChange} />
			</Wrap>,
		);

		fireEvent.change(task(), { target: { value: "B" } });
		await waitFor(() => expect(screen.getByText("Start task").closest("button")).toBeEnabled());
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(screen.getByText("nope")).toBeInTheDocument());
		expect(onSubmittingChange).toHaveBeenLastCalledWith(false);
	});

	it("offers an explicit Terminal UI retry after Chat preflight fails", async () => {
		h.post
			.mockResolvedValueOnce({ error: { code: "CHAT_DRIVER_UNAVAILABLE" } })
			.mockResolvedValueOnce({ data: { workerId: "sess-tui" } });
		const onCreated = vi.fn();

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));

		const fallback = await screen.findByRole("button", { name: "Create as Terminal UI" });
		fireEvent.click(fallback);
		await waitFor(() => expect(onCreated).toHaveBeenCalledWith("sess-tui"));
		expect(h.post).toHaveBeenLastCalledWith(
			"/api/v1/orchestrators/delegate",
			expect.objectContaining({ body: expect.objectContaining({ mode: "tui" }) }),
		);
	});

	it("reports dirty then clears it on unmount", () => {
		const onDirtyChange = vi.fn();
		const { unmount } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} onDirtyChange={onDirtyChange} />
			</Wrap>,
		);
		fireEvent.change(task(), { target: { value: "T" } });
		expect(onDirtyChange).toHaveBeenLastCalledWith(true);
		unmount();
		expect(onDirtyChange).toHaveBeenLastCalledWith(false);
	});

	it("uses the project worker model as the new task model default", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [],
						allowCustom: true,
						refreshRecommended: false,
					},
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						config: { worker: { agent: "codex", agentConfig: { model: "gpt-5" } } },
					},
				},
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-2" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const model = await screen.findByDisplayValue("gpt-5");
		fireEvent.change(model, { target: { value: "gpt-5.1" } });
		fireEvent.change(task(), { target: { value: "Use the selected model" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({
					body: expect.objectContaining({ model: "gpt-5.1" }),
				}),
			),
		);
	});
});

// Strict delegation is the daemon's rule; these pin that the composer stops
// offering shapes the role map would refuse, rather than discovering the
// refusal after a person has typed a brief.
describe("TaskComposer under a strict role map", () => {
	const strictConfig = {
		status: "ok",
		project: {
			config: {
				roleMap: {
					role_map_schema_version: 1,
					strictDelegation: true,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: { harness: "claude-code", template: "o.md", permissions: { canSpawn: true, workspaceWrites: false } },
						reviewer: { harness: "codex", template: "r.md", permissions: { canSpawn: false, workspaceWrites: false } },
						implementor: { harness: "codex", model: "gpt-5.6", template: "i.md", permissions: { canSpawn: false, workspaceWrites: true } },
					},
				},
			},
		},
	};

	function mockProject(project: unknown) {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true, refreshRecommended: false } };
			}
			return { data: project };
		});
	}

	function renderComposer() {
		return render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
	}

	it("replaces the free-form agent field with the map's roles", async () => {
		mockProject(strictConfig);
		renderComposer();

		await screen.findByRole("button", { name: "Role" });
		// The harness picker is the free-form target: sending one alongside a
		// role is HARNESS_OVERRIDE_FORBIDDEN, so it is not offered at all.
		expect(screen.queryByTestId("agent-field")).not.toBeInTheDocument();
	});

	it("does not offer the orchestrator role as a delegation target", async () => {
		mockProject(strictConfig);
		renderComposer();

		await userEvent.click(await screen.findByRole("button", { name: "Role" }));
		expect(screen.getByRole("menuitem", { name: "implementor" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "reviewer" })).toBeInTheDocument();
		// Delegation spawns a worker; a strict map binds the orchestrator role to
		// KindOrchestrator only, so offering it would offer a refused target.
		expect(screen.queryByRole("menuitem", { name: "orchestrator" })).not.toBeInTheDocument();
	});

	it("refuses to submit without a role instead of letting the daemon reject it", async () => {
		mockProject(strictConfig);
		renderComposer();

		await screen.findByRole("button", { name: "Role" });
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(screen.getByText("Choose a role before delegating.")).toBeInTheDocument());
		expect(h.post).not.toHaveBeenCalled();
	});

	it("sends the role and NEITHER agent nor model", async () => {
		mockProject(strictConfig);
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });
		renderComposer();

		await userEvent.click(await screen.findByRole("button", { name: "Role" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "implementor" }));
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalled());
		const body = h.post.mock.calls[0][1].body;
		expect(body.roleId).toBe("implementor");
		// Absent, not empty: the daemon refuses a harness or model sent with a
		// role under a strict map.
		expect(body.agent).toBeUndefined();
		expect(body.model).toBeUndefined();
	});

	it("shows the role's binding as a fact rather than an editable field", async () => {
		mockProject(strictConfig);
		renderComposer();

		await userEvent.click(await screen.findByRole("button", { name: "Role" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "implementor" }));
		expect(await screen.findByText("codex · gpt-5.6")).toBeInTheDocument();
	});

	it("blocks delegation when the map defines no worker role", async () => {
		mockProject({
			status: "ok",
			project: {
				config: {
					roleMap: {
						role_map_schema_version: 1,
						strictDelegation: true,
						roles: { orchestrator: { harness: "claude-code", template: "o.md", permissions: { canSpawn: true, workspaceWrites: false } } },
					},
				},
			},
		});
		renderComposer();

		expect(await screen.findByText(/defines no role a worker may be delegated to/)).toBeInTheDocument();
		expect(screen.getByText("Start task").closest("button")).toBeDisabled();
	});

	it("leaves a non-strict project on the free-form path", async () => {
		mockProject({
			status: "ok",
			project: { config: { roleMap: { role_map_schema_version: 1, roles: { implementor: { harness: "codex", template: "i.md", permissions: { canSpawn: false, workspaceWrites: true } } } } } },
		});
		h.post.mockResolvedValue({ data: { workerId: "sess-1" } });
		renderComposer();

		expect(await screen.findByTestId("agent-field")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Role" })).not.toBeInTheDocument();

		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.click(screen.getByText("Start task"));
		await waitFor(() => expect(h.post).toHaveBeenCalled());
		expect(h.post.mock.calls[0][1].body.roleId).toBeUndefined();
	});
});

// A failed config read is not a non-strict project. isPending goes false and
// data stays undefined, which reads as "not strict" unless something says
// otherwise — and then the composer offers the free-form form to a project that
// may well refuse it.
describe("TaskComposer when the project config cannot be read", () => {
	function mockRejectedConfig() {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true, refreshRecommended: false } };
			}
			return { data: undefined, error: { code: "PROJECT_UNAVAILABLE", message: "daemon unreachable" } };
		});
	}

	function renderComposer() {
		return render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);
	}

	it("does not fall back to the free-form agent and model form", async () => {
		mockRejectedConfig();
		renderComposer();

		await screen.findByRole("alert");
		expect(screen.queryByTestId("agent-field")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Role" })).not.toBeInTheDocument();
	});

	it("blocks submission and says why, rather than letting the daemon refuse it", async () => {
		mockRejectedConfig();
		renderComposer();

		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent(/delegation rules could not be read/);
		expect(screen.getByText("Start task").closest("button")).toBeDisabled();

		// Enter-to-submit bypasses the disabled button, so the rule has to live
		// in submit() too.
		fireEvent.change(task(), { target: { value: "Do the thing" } });
		fireEvent.submit(task().closest("form") as HTMLFormElement);
		await waitFor(() => expect(screen.getAllByRole("alert").length).toBeGreaterThan(0));
		expect(h.post).not.toHaveBeenCalled();
	});

	it("offers a retry that refetches the config", async () => {
		mockRejectedConfig();
		renderComposer();

		await screen.findByRole("alert");
		const before = h.get.mock.calls.length;
		fireEvent.click(screen.getByText("Try again"));
		await waitFor(() => expect(h.get.mock.calls.length).toBeGreaterThan(before));
	});
});
