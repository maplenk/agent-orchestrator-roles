import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
	get: vi.fn(),
	post: vi.fn(),
	capture: vi.fn(),
	agentValues: [] as string[],
}));

vi.mock("../hooks/useAgentsQuery", () => ({
	agentsQueryKey: ["agents"],
	agentsQueryOptions: { queryKey: ["agents"], queryFn: async () => ({}) },
	refreshAgents: vi.fn(),
	refreshAgentsIfStale: vi.fn(async () => undefined),
}));

vi.mock("./CreateProjectAgentSheet", () => ({
	RequiredAgentField: ({
		value,
		onChange,
		triggerClassName,
	}: {
		value: string;
		onChange: (value: string) => void;
		triggerClassName?: string;
	}) => {
		h.agentValues.push(value);
		return (
			<button
				type="button"
				aria-label="Agent"
				className={triggerClassName}
				data-testid="agent-field"
				data-value={value}
				onClick={() => onChange(value === "codex" ? "claude-code" : "codex")}
			/>
		);
	},
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

const task = () => screen.getByRole("textbox", { name: "Task" });

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
	vi.unstubAllGlobals();
	h.agentValues.length = 0;
});

describe("TaskComposer", () => {
	it("starts a promptless worker when the task is empty", async () => {
		const onCreated = vi.fn();
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-empty" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={onCreated} />
			</Wrap>,
		);

		expect(task()).toHaveAttribute("placeholder", "e.g. Fix the flaky checkout test (optional)…");
		expect(screen.getByRole("button", { name: "Start task" })).toBeEnabled();
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ projectId: "proj-1", brief: "" }) }),
			),
		);
		expect(onCreated).toHaveBeenCalledWith("sess-empty");
	});

	it("keeps prompt guidance in the field instead of adding a separate footer row", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(task()).toHaveAttribute("placeholder", "e.g. Fix the flaky checkout test (optional)…");
		expect(screen.queryByText("Start now — details can come later.")).not.toBeInTheDocument();
		expect(screen.queryByText("Shift+Enter for a new line")).not.toBeInTheDocument();
		fireEvent.change(task(), { target: { value: "Investigate the failure" } });
		expect(task()).toHaveValue("Investigate the failure");
	});

	it("keeps agent and model in equal stable toolbar tracks", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const runControls = screen.getByRole("group", { name: "Runs with" });
		expect(runControls).toHaveClass("composer-run-controls");
		expect(runControls.closest(".composer-toolbar")).not.toBeNull();
		expect(runControls.querySelectorAll(".composer-toolbar-slot")).toHaveLength(2);
		expect(screen.getByTestId("agent-field").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(screen.getByLabelText("Model").closest(".composer-toolbar-slot")).not.toBeNull();
		expect(runControls.querySelector(".composer-toolbar-divider")).not.toBeNull();
	});

	it("keeps the file attach control inside the prompt surface", () => {
		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(screen.getByRole("button", { name: "Add file" }).closest(".composer-prompt-surface")).not.toBeNull();
	});

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
				body: expect.not.objectContaining({ attachments: expect.anything() }),
			}),
		);
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

	it("attaches a selected file and sends it in the delegate body", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
		fireEvent.change(input, { target: { files: [file] } });

		expect(await screen.findByText("notes.txt")).toBeInTheDocument();

		fireEvent.change(task(), { target: { value: "Use the notes" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		const body = h.post.mock.calls[0][1].body as {
			attachments?: Array<{ mimeType: string; data: string }>;
		};
		expect(body.attachments).toHaveLength(1);
		expect(body.attachments?.[0].mimeType).toBe("text/plain");
		expect(body.attachments?.[0].data.length).toBeGreaterThan(0);
	});

	it("waits for a selected file read before submitting", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });
		let finishRead!: () => void;
		class SlowFileReader {
			error: Error | null = null;
			result: string | ArrayBuffer | null = null;
			onerror: (() => void) | null = null;
			onload: (() => void) | null = null;

			readAsDataURL(file: File) {
				finishRead = () => {
					this.result = `data:${file.type};base64,AQID`;
					this.onload?.();
				};
			}
		}
		vi.stubGlobal("FileReader", SlowFileReader);

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		fireEvent.change(input, {
			target: { files: [new File([new Uint8Array([1, 2, 3])], "slow.txt", { type: "text/plain" })] },
		});
		fireEvent.change(task(), { target: { value: "Use the slow file" } });
		fireEvent.click(screen.getByText("Start task"));

		expect(h.post).not.toHaveBeenCalled();

		await act(async () => finishRead());
		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).toMatchObject({
			attachments: [{ mimeType: "text/plain", data: "AQID" }],
		});
	});

	it("removes a selected file before submitting", async () => {
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-1" } });

		const { container } = render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1, 2, 3])], "notes.txt", { type: "text/plain" });
		fireEvent.change(input, { target: { files: [file] } });

		expect(await screen.findByText("notes.txt")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Remove notes.txt" }));
		await waitFor(() => expect(screen.queryByText("notes.txt")).not.toBeInTheDocument());

		fireEvent.change(task(), { target: { value: "No attachment now" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() => expect(h.post).toHaveBeenCalledTimes(1));
		expect(h.post.mock.calls[0][1].body).not.toHaveProperty("attachments");
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
		// The composer waits for the project config before it will submit, so
		// that it never sends a shape a strict role map would refuse.
		await waitFor(() => expect(screen.getByText("Start task").closest("button")).toBeEnabled());
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

	it("preselects the project worker agent and spawns with it", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true } };
			}
			return {
				data: { status: "ok", project: { agent: "claude-code", config: { worker: { agent: "codex" } } } },
			};
		});
		h.post.mockResolvedValueOnce({ data: { workerId: "sess-3" } });

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "codex"));

		fireEvent.change(task(), { target: { value: "Ship it" } });
		fireEvent.click(screen.getByText("Start task"));

		await waitFor(() =>
			expect(h.post).toHaveBeenCalledWith(
				"/api/v1/orchestrators/delegate",
				expect.objectContaining({ body: expect.objectContaining({ agent: "codex" }) }),
			),
		);
	});

	it("renders a known default agent without an empty intermediate selection", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});
		const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		queryClient.setQueryData(["project", "proj-1"], { agent: "codex", config: {} });

		render(
			<QueryClientProvider client={queryClient}>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</QueryClientProvider>,
		);

		expect(await screen.findByDisplayValue("gpt-5.6-sol")).toBeInTheDocument();
		expect(h.agentValues).not.toContain("");
	});

	it("falls back to the global default agent when the project sets no worker agent", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return { data: { agent: "claude-code", selectionMode: "text", models: [], allowCustom: true } };
			}
			return { data: { status: "ok", project: { agent: "claude-code", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		await waitFor(() => expect(screen.getByTestId("agent-field")).toHaveAttribute("data-value", "claude-code"));
	});

	it("preselects the agent's default model when the project configures none", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [
							{ id: "gpt-5", label: "GPT-5" },
							{ id: "gpt-5-codex", label: "GPT-5 Codex", isDefault: true },
						],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByDisplayValue("gpt-5-codex")).toBeInTheDocument();
	});

	it("clears a stale model while the newly selected agent catalog resolves", async () => {
		let resolveClaudeCatalog!: (value: {
			data: {
				agent: string;
				selectionMode: "text";
				models: Array<{ id: string; label: string; isDefault: boolean }>;
				allowCustom: boolean;
			};
		}) => void;
		h.get.mockImplementation(async (path: string, request?: { params?: { path?: { agent?: string } } }) => {
			if (path.includes("/models")) {
				if (request?.params?.path?.agent === "claude-code") {
					return new Promise((resolve) => {
						resolveClaudeCatalog = resolve;
					});
				}
				return {
					data: {
						agent: "codex",
						selectionMode: "text",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		expect(await screen.findByDisplayValue("gpt-5.6-sol")).toBeInTheDocument();
		fireEvent.click(screen.getByTestId("agent-field"));

		expect(screen.queryByDisplayValue("gpt-5.6-sol")).not.toBeInTheDocument();
		expect(screen.getByRole("status", { name: "Loading models…" })).toBeInTheDocument();

		await act(async () => {
			resolveClaudeCatalog({
				data: {
					agent: "claude-code",
					selectionMode: "text",
					models: [{ id: "opus[1m]", label: "opus[1m]", isDefault: true }],
					allowCustom: true,
				},
			});
		});
		expect(await screen.findByDisplayValue("opus[1m]")).toBeInTheDocument();
	});

	it("shows the same no-override label on the trigger and in the menu", async () => {
		h.get.mockImplementation(async (path: string) => {
			if (path.includes("/models")) {
				return {
					data: {
						agent: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5", label: "GPT-5" }],
						allowCustom: true,
					},
				};
			}
			return { data: { status: "ok", project: { agent: "codex", config: {} } } };
		});

		render(
			<Wrap>
				<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
			</Wrap>,
		);

		const picker = await screen.findByRole("button", { name: "Model" });
		expect(picker).toHaveTextContent("Use codex's default");

		await userEvent.click(picker);
		expect(await screen.findByRole("menuitem", { name: "Use codex's default" })).toBeInTheDocument();
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
		// mode is orthogonal to the role, so the strict branch passes through
		// whatever interface was asked for rather than pinning one — nobody asked
		// here, so nothing is sent and the daemon's default stands.
		expect(body.mode).toBeUndefined();
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

// Mode and role are orthogonal: a role binds harness, model and policy, never
// the interface. So the TUI fallback must not drop the role to change
// interface — that would silently turn a role-pinned worker into a free-form
// one, which on a strict map the daemon refuses outright.
it("keeps the role when falling back to Terminal UI", async () => {
	h.get.mockImplementation(async (path: string) => {
		if (path.includes("/models")) {
			return { data: { agent: "codex", selectionMode: "text", models: [], allowCustom: true, refreshRecommended: false } };
		}
		return {
			data: {
				status: "ok",
				project: {
					config: {
						roleMap: {
							role_map_schema_version: 1,
							strictDelegation: true,
							orchestratorRole: "orchestrator",
							roles: {
								orchestrator: { harness: "codex", template: "o", permissions: { canSpawn: true, workspaceWrites: false } },
								implementor: { harness: "codex", template: "i", permissions: { canSpawn: false, workspaceWrites: true } },
							},
						},
					},
				},
			},
		};
	});
	h.post
		.mockResolvedValueOnce({ error: { code: "SESSION_MODE_ROLE_FORBIDDEN" } })
		.mockResolvedValueOnce({ data: { workerId: "sess-tui" } });

	render(
		<Wrap>
			<TaskComposer projectId="proj-1" onCreated={vi.fn()} />
		</Wrap>,
	);

	await userEvent.click(await screen.findByRole("button", { name: "Role" }));
	await userEvent.click(screen.getByRole("menuitem", { name: "implementor" }));
	fireEvent.change(task(), { target: { value: "Do the thing" } });
	await waitFor(() => expect(screen.getByText("Start task").closest("button")).toBeEnabled());
	fireEvent.click(screen.getByText("Start task"));

	const fallback = await screen.findByRole("button", { name: "Create as Terminal UI" });
	fireEvent.click(fallback);

	await waitFor(() => expect(h.post).toHaveBeenCalledTimes(2));
	const retry = h.post.mock.calls[1][1].body;
	expect(retry.mode).toBe("tui");
	expect(retry.roleId).toBe("implementor");
	// And still no free-form target alongside it.
	expect(retry.agent).toBeUndefined();
	expect(retry.model).toBeUndefined();
});
