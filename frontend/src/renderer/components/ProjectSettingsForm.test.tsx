import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, postMock, navigateMock } = vi.hoisted(() => ({
	getMock: vi.fn(),
	putMock: vi.fn(),
	postMock: vi.fn(),
	navigateMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", async (importOriginal) => {
	const actual = await importOriginal<typeof import("@tanstack/react-router")>();
	return {
		...actual,
		useNavigate: () => navigateMock,
	};
});

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: getMock,
		PUT: putMock,
		POST: postMock,
	},
	apiErrorCode: (error: unknown) =>
		typeof error === "object" && error !== null && "code" in error
			? String((error as { code: unknown }).code)
			: undefined,
	apiErrorRequestId: (error: unknown) =>
		typeof error === "object" && error !== null && "requestId" in error
			? String((error as { requestId: unknown }).requestId)
			: undefined,
	apiErrorMessage: (error: unknown) => {
		if (error instanceof Error) return error.message;
		if (typeof error === "object" && error !== null && "message" in error) {
			return String((error as { message: unknown }).message);
		}
		return "Request failed";
	},
}));

import { ProjectSettingsForm, type ProjectSettingsSaveState, type ProjectSettingsSection } from "./ProjectSettingsForm";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { WorkspaceSummary } from "../types/workspace";

const defaultRoleMapSHA256 = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
const savedRoleMapSHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff";

function TestProjectSettings({
	projectId,
	section,
}: {
	projectId: string;
	section?: ProjectSettingsSection;
}) {
	const [saveState, setSaveState] = useState<ProjectSettingsSaveState>({
		isPending: false,
		showSaving: false,
		validationError: null,
		mutationError: null,
		saved: false,
		replacementError: null,
	});
	return (
		<>
			<ProjectSettingsForm projectId={projectId} section={section} onSaveState={setSaveState} />
			{saveState.validationError && <span>{saveState.validationError}</span>}
			{saveState.mutationError && <span>{saveState.mutationError}</span>}
			{saveState.saved && <span>{"Saved"}</span>}
			{saveState.replacementError && <span>{`Orchestrator restart failed: ${saveState.replacementError}`}</span>}
		</>
	);
}

function renderSettings(projectId = "proj-1", workspaces?: WorkspaceSummary[], section?: ProjectSettingsSection) {
	const queryClient = new QueryClient({
		defaultOptions: {
			queries: { retry: false },
			mutations: { retry: false },
		},
	});
	if (workspaces) {
		queryClient.setQueryData(workspaceQueryKey, workspaces);
	}
	render(
		<QueryClientProvider client={queryClient}>
			<TestProjectSettings projectId={projectId} section={section} />
		</QueryClientProvider>,
	);
	return queryClient;
}

async function chooseOption(trigger: HTMLElement, optionName: string) {
	await userEvent.click(trigger);
	const escaped = optionName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	await userEvent.click(await screen.findByRole("menuitem", { name: new RegExp(`^${escaped}$`, "i") }));
}

function submitSettings() {
	fireEvent.submit(document.getElementById("project-settings-form")!);
}

const agentCatalogResponse = {
	data: {
		supported: [
			{ id: "claude-code", label: "Claude Code" },
			{ id: "codex", label: "Codex" },
			{ id: "copilot", label: "GitHub Copilot" },
			{ id: "cursor", label: "Cursor" },
			{ id: "goose", label: "Goose" },
			{ id: "kilocode", label: "Kilo Code" },
			{ id: "kiro", label: "Kiro" },
			{ id: "opencode", label: "OpenCode" },
			{ id: "pi", label: "Pi" },
		],
		installed: [
			{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
			{ id: "codex", label: "Codex", authStatus: "authorized" },
			{ id: "copilot", label: "GitHub Copilot", authStatus: "authorized" },
			{ id: "cursor", label: "Cursor", authStatus: "authorized" },
			{ id: "goose", label: "Goose", authStatus: "authorized" },
			{ id: "kilocode", label: "Kilo Code", authStatus: "authorized" },
			{ id: "kiro", label: "Kiro", authStatus: "unknown" },
			{ id: "opencode", label: "OpenCode", authStatus: "authorized" },
			{ id: "pi", label: "Pi", authStatus: "authorized" },
		],
		authorized: [
			{ id: "claude-code", label: "Claude Code", authStatus: "authorized" },
			{ id: "codex", label: "Codex", authStatus: "authorized" },
			{ id: "copilot", label: "GitHub Copilot", authStatus: "authorized" },
			{ id: "cursor", label: "Cursor", authStatus: "authorized" },
			{ id: "goose", label: "Goose", authStatus: "authorized" },
			{ id: "kilocode", label: "Kilo Code", authStatus: "authorized" },
			{ id: "opencode", label: "OpenCode", authStatus: "authorized" },
			{ id: "pi", label: "Pi", authStatus: "authorized" },
		],
	},
	error: undefined,
};

function mockProject(project: Record<string, unknown>) {
	getMock.mockImplementation(async (path: string) => {
		if (path === "/api/v1/agents") return agentCatalogResponse;
		if (path === "/api/v1/agents/{agent}/models") {
			return {
				data: {
					agentId: "test-agent",
					selectionMode: "text",
					models: [],
					allowCustom: true,
					source: "manual",
					fetchedAt: "2026-07-31T00:00:00Z",
					stale: false,
				},
				error: undefined,
			};
		}
		return {
			data: {
				status: "ok",
				project: { roleMapSha256: defaultRoleMapSHA256, ...project },
			},
			error: undefined,
		};
	});
}

beforeEach(() => {
	getMock.mockReset();
	putMock.mockReset();
	postMock.mockReset();
	navigateMock.mockReset();
	putMock.mockImplementation(async (path: string, request: { body?: { roleMap?: unknown } }) => ({
		data: {
			project: path === "/api/v1/projects/{id}/role-map"
				? { roleMapSha256: savedRoleMapSHA256, config: { roleMap: request.body?.roleMap } }
				: {},
		},
		error: undefined,
	}));
	postMock.mockResolvedValue({
		data: { orchestrator: { id: "proj-1-orch-2" } },
		error: undefined,
		response: { status: 200 },
	});
});

describe("ProjectSettingsForm", () => {
	it("keeps the settings envelope isolated from TaskComposer's cached project shape", async () => {
		const queryClient = new QueryClient({
			defaultOptions: {
				queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
				mutations: { retry: false },
			},
		});
		const composerProject = {
			id: "proj-1",
			name: "Composer cache",
			kind: "single_repo",
			path: "/repo/composer-cache",
			repo: "",
			defaultBranch: "main",
			config: { worker: { agent: "codex" } },
		};
		queryClient.setQueryData(["project", "proj-1"], composerProject);
		mockProject({
			id: "proj-1",
			name: "Settings response",
			kind: "single_repo",
			path: "/repo/settings-response",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		render(
			<QueryClientProvider client={queryClient}>
				<ProjectSettingsForm projectId="proj-1" />
			</QueryClientProvider>,
		);

		expect(await screen.findByDisplayValue("Settings response")).toBeInTheDocument();
		expect(queryClient.getQueryData(["project", "proj-1"])).toBe(composerProject);
		expect(queryClient.getQueryData(["project-settings", "proj-1"])).toMatchObject({ status: "ok" });
	});

	it("refuses a healthy project response that omits the role-map revision", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") return agentCatalogResponse;
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Missing revision",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "roles");
		expect(await screen.findByText("Could not load project.")).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("keeps a dirty role draft and base revision when an explicit refetch fails", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "1111111111111111111111111111111111111111111111111111111111111111",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
					},
				},
			},
		};
		let failProjectRead = false;
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") return agentCatalogResponse;
			if (failProjectRead) return { data: undefined, error: { message: "refresh offline" } };
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "roles");
		const strict = await screen.findByRole("switch", { name: "Strict delegation" });
		await userEvent.click(strict);
		failProjectRead = true;
		await userEvent.click(screen.getByRole("button", { name: "Reload latest role map" }));

		expect(await screen.findByRole("alert")).toHaveTextContent("refresh offline");
		expect(screen.getByRole("switch", { name: "Strict delegation" })).toBeChecked();
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/role-map", {
			params: { path: { id: "proj-1" } },
			body: expect.objectContaining({
				expectedRoleMapSha256: project.roleMapSha256,
				roleMap: expect.objectContaining({ strictDelegation: true }),
			}),
		});
	});

	it("keeps a dirty revision A draft across an unsolicited successful revision B refetch", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "2222222222222222222222222222222222222222222222222222222222222222",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator-a",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
					},
				},
			},
		};
		mockProject(project);
		putMock.mockResolvedValueOnce({
			data: undefined,
			error: { code: "PROJECT_ROLE_MAP_CONFLICT", message: "revision A is stale" },
		});
		const queryClient = renderSettings("proj-1", undefined, "roles");
		await userEvent.click(await screen.findByRole("switch", { name: "Strict delegation" }));

		project.roleMapSha256 = "3333333333333333333333333333333333333333333333333333333333333333";
		project.config = {
			roleMap: {
				...project.config.roleMap,
				roles: {
					orchestrator: {
						...project.config.roleMap.roles.orchestrator,
						template: "orchestrator-b",
					},
				},
			},
		};
		await act(async () => {
			await queryClient.refetchQueries({ queryKey: ["project-settings", "proj-1"] });
		});

		expect(screen.getByRole("switch", { name: "Strict delegation" })).toBeChecked();
		expect(screen.getByRole("textbox", { name: "Template profile ID for orchestrator" })).toHaveValue("orchestrator-a");
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/role-map", {
			params: { path: { id: "proj-1" } },
			body: expect.objectContaining({
				expectedRoleMapSha256: "2222222222222222222222222222222222222222222222222222222222222222",
				roleMap: expect.objectContaining({ strictDelegation: true }),
			}),
		});
	});

	it("does not have its own close button (dialog handles closing)", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByLabelText("Project name");

		// Close button is now in SettingsDialog, not in the form itself
		expect(screen.queryByRole("button", { name: "Close settings" })).not.toBeInTheDocument();
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("does not navigate on Escape (dialog handles closing)", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();
		await screen.findByLabelText("Project name");

		await userEvent.keyboard("{Escape}");

		// Escape is handled by the Radix Dialog in SettingsDialog, not the form
		expect(navigateMock).not.toHaveBeenCalled();
	});

	it("atomically saves the project display name and config without changing its stable ID", async () => {
		mockProject({
			id: "tg_content_factory_5863f66be3",
			name: "tg_content_factory_5863f66be3",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("tg_content_factory_5863f66be3");

		const projectName = await screen.findByLabelText("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "TG Content Factory");
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "tg_content_factory_5863f66be3" } },
			body: expect.objectContaining({
				displayName: "TG Content Factory",
				expectedRoleMapSha256: defaultRoleMapSHA256,
			}),
		});
		expect(screen.getByText("tg_content_factory_5863f66be3")).toBeInTheDocument();
	});

	it("renders git scp-style remotes as clickable https links", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const repoLink = await screen.findByRole("link", { name: "git@github.com:acme/project-one.git" });
		expect(repoLink).toHaveAttribute("href", "https://github.com/acme/project-one");
	});

	it("renders ssh remotes as clickable https links", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "ssh://git@github.com/acme/project-one.git",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const repoLink = await screen.findByRole("link", { name: "ssh://git@github.com/acme/project-one.git" });
		expect(repoLink).toHaveAttribute("href", "https://github.com/acme/project-one");
	});

	it("loads agents fields and saves without dropping hidden workflow config", async () => {
		const roleMap = {
			role_map_schema_version: 1,
			orchestratorRole: "orchestrator",
			roles: {
				orchestrator: {
					template: "orchestrator",
					harness: "claude-code",
					permissions: { workspaceWrites: true, canSpawn: true },
				},
			},
		};
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				sessionPrefix: "po",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				worker: {
					agent: "codex",
					agentConfig: { model: "worker-model" },
				},
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "claude-opus-4-5",
					permissions: "auto",
				},
				reviewers: [{ harness: "claude-code" }],
				roleMap,
			},
		});

		renderSettings("proj-1", undefined, "agents");

		expect(screen.queryByLabelText("Default branch")).not.toBeInTheDocument();
		expect(await screen.findByLabelText("Worker model")).toHaveValue("worker-model");
		expect(screen.getByLabelText("Orchestrator model")).toHaveValue("claude-opus-4-5");

		const workerAgent = screen.getByRole("button", { name: "Default worker agent" });
		const orchestratorAgent = screen.getByRole("button", { name: "Default orchestrator agent" });
		const permissionMode = screen.getByRole("button", { name: "Permission mode" });
		expect(workerAgent).toHaveTextContent("codex");
		expect(orchestratorAgent).toHaveTextContent("claude-code");
		expect(permissionMode).toHaveTextContent("Auto");

		await chooseOption(workerAgent, "OpenCode");
		await chooseOption(orchestratorAgent, "Goose");
		await userEvent.type(screen.getByLabelText("Worker model"), "openai/gpt-5.4");
		await userEvent.type(screen.getByLabelText("Orchestrator model"), "anthropic/claude-sonnet");
		await userEvent.click(permissionMode);
		await userEvent.click(await screen.findByRole("menuitem", { name: "Bypass permissions" }));

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "proj-1" } },
			body: {
				displayName: "Project One",
				expectedRoleMapSha256: defaultRoleMapSHA256,
				config: expect.objectContaining({
					// Hidden workflow config is preserved
					defaultBranch: "develop",
					sessionPrefix: "po",
					env: { FOO: "bar" },
					reviewers: [{ harness: "claude-code" }],
					roleMap,
					// Agents changes applied
					worker: {
						agent: "opencode",
						agentConfig: { model: "openai/gpt-5.4" },
					},
					orchestrator: {
						agent: "goose",
						agentConfig: { model: "anthropic/claude-sonnet" },
					},
					agentConfig: {
						permissions: "bypass-permissions",
					},
				}),
			},
		});
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Saved")).toBeInTheDocument();
	}, 20_000);

	it("loads workflow fields correctly", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			config: {
				defaultBranch: "develop",
				sessionPrefix: "po",
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				reviewers: [{ harness: "claude-code" }],
			},
		});

		renderSettings("proj-1", undefined, "workflow");

		expect(await screen.findByLabelText("Default branch")).toHaveValue("develop");
		expect(screen.getByLabelText("Session prefix")).toHaveValue("po");
		const reviewerAgent = screen.getByRole("button", { name: "Default reviewer agent" });
		expect(reviewerAgent).toHaveTextContent("claude-code");
	});

	it("shows the full model catalog again after selecting a model", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") {
				return {
					data: {
						agentId: "codex",
						selectionMode: "catalog",
						models: [
							{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol", isDefault: true },
							{ id: "gpt-5.5", label: "GPT-5.5" },
							{ id: "gpt-5.4", label: "GPT-5.4" },
						],
						allowCustom: true,
						source: "official-catalog",
						fetchedAt: "2026-07-31T00:00:00Z",
						stale: false,
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						roleMapSha256: defaultRoleMapSHA256,
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "codex" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		const workerModel = await screen.findByRole("button", { name: "Worker model" });
		await userEvent.click(workerModel);
		expect((await screen.findAllByRole("menuitem")).map((item) => item.textContent)).toEqual([
			"Agent default",
			"GPT-5.6 SolDefault",
			"GPT-5.5",
			"GPT-5.4",
			"Custom model…",
		]);
		// A compact catalog stays immediately scannable and does not spend a row on search.
		expect(screen.queryByRole("searchbox", { name: "Search worker model" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: /GPT-5\.4/ }));
		expect(workerModel).toHaveTextContent("GPT-5.4");

		await userEvent.click(workerModel);
		expect(await screen.findByRole("menuitem", { name: /GPT-5\.6 Sol/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /GPT-5\.5/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /GPT-5\.4/ })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Custom model…" })).toBeInTheDocument();
	});

	it("shows a warning when refreshing a cached model catalog fails", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") {
				return {
					data: {
						agentId: "codex",
						selectionMode: "catalog",
						models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol" }],
						allowCustom: true,
						source: "official-catalog",
						fetchedAt: "2026-07-31T00:00:00Z",
						stale: false,
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						roleMapSha256: defaultRoleMapSHA256,
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "codex" },
						},
					},
				},
				error: undefined,
			};
		});
		postMock.mockResolvedValue({ data: undefined, error: { message: "model refresh unavailable" } });

		renderSettings("proj-1", undefined, "agents");

		await userEvent.click(await screen.findByRole("button", { name: "Refresh worker model list" }));
		expect(await screen.findByText("model refresh unavailable")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Worker model" })).toHaveTextContent("Agent default");
	});

	it("shows cached models immediately and deduplicates background revalidation", async () => {
		const cachedCatalog = {
			agentId: "codex",
			selectionMode: "catalog" as const,
			models: [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol" }],
			allowCustom: true,
			source: "official-catalog",
			fetchedAt: "2026-07-31T00:00:00Z",
			validatedAt: "2026-07-31T00:00:00Z",
			refreshRecommended: true,
			stale: false,
		};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") return agentCatalogResponse;
			if (path === "/api/v1/agents/{agent}/models") return { data: cachedCatalog, error: undefined };
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						roleMapSha256: defaultRoleMapSHA256,
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "codex" },
						},
					},
				},
				error: undefined,
			};
		});
		postMock.mockResolvedValue({
			data: { ...cachedCatalog, refreshRecommended: false, validatedAt: "2026-08-03T00:00:00Z" },
			error: undefined,
		});

		renderSettings("proj-1", undefined, "agents");

		expect(await screen.findByRole("button", { name: "Worker model" })).toHaveTextContent("Agent default");
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/agents/{agent}/models/refresh", {
			params: {
				path: { agent: "codex" },
				query: { projectId: "proj-1", revalidate: true },
			},
		});
	});

	it("shows the daemon validation message when the atomic settings save fails", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});
		putMock.mockResolvedValue({
			data: undefined,
			error: { message: "invalid permissions" },
		});

		renderSettings();

		const projectName = await screen.findByLabelText("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "Updated Project");
		submitSettings();

		expect(await screen.findByText("invalid permissions")).toBeInTheDocument();
		expect(screen.queryByText("Saved")).not.toBeInTheDocument();
		expect(postMock).not.toHaveBeenCalled();
	});

	it("requires an explicit manual conversion before saving a legacy automatic ladder", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				roleMap: {
					role_map_schema_version: 1,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
						implementor: {
							template: "implementor",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
					failover: {
						mode: "automatic",
						roles: {
							implementor: [{ harness: "claude-code" }, { harness: "codex", model: "gpt-5" }],
						},
					},
				},
			},
		});

		renderSettings("proj-1", undefined, "roles");
		expect(await screen.findByRole("alert")).toHaveTextContent(
			"Automatic failover requires reviewed structured limit detection, which is not promoted for any harness.",
		);
		expect(screen.getByText("Automatic (unavailable)")).toBeInTheDocument();
		submitSettings();
		expect(await screen.findByText(/^Convert automatic failover to manual before saving\./)).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
		await userEvent.click(screen.getByRole("button", { name: "Convert to manual" }));
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(screen.getByText("Manual")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Discard role-map changes" }));
		expect(await screen.findByText("Automatic (unavailable)")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Convert to manual" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Convert to manual" }));
		const implementorCard = screen.getByRole("group", { name: "implementor" });
		await userEvent.click(within(implementorCard).getByRole("button", { name: "Move rung down — implementor rung 1" }));
		expect(within(implementorCard).getByRole("button", { name: "Move rung up — implementor rung 2" })).toHaveFocus();
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/role-map", {
			params: { path: { id: "proj-1" } },
			body: {
				expectedRoleMapSha256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			roleMap: expect.objectContaining({
					failover: {
						mode: "manual",
						roles: {
							implementor: [{ harness: "codex", model: "gpt-5" }, { harness: "claude-code" }],
						},
					},
				}),
			},
		});
	});

	it("rejects a blank project name before sending the settings update", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings();

		const projectName = await screen.findByLabelText("Project name");
		await userEvent.clear(projectName);
		await userEvent.type(projectName, "   ");
		submitSettings();

		expect(await screen.findByText("Project name is required.")).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("requires worker and orchestrator agents for existing projects missing role config", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {},
		});

		renderSettings("proj-1", undefined, "agents");

		expect(await screen.findByText("Worker and orchestrator agents are required.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Default worker agent" })).toHaveTextContent("Select worker agent");
		expect(screen.getByRole("button", { name: "Default orchestrator agent" })).toHaveTextContent(
			"Select orchestrator agent",
		);

		submitSettings();

		expect(await screen.findAllByText("Worker and orchestrator agents are required.")).toHaveLength(2);
		expect(putMock).not.toHaveBeenCalled();
	});

	it("uses the localized default label for the project reviewer picker", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewerAgent = await screen.findByRole("button", { name: "Default reviewer agent" });
		expect(reviewerAgent).toHaveTextContent("Project default");

		await userEvent.click(reviewerAgent);
		expect(await screen.findByRole("menuitem", { name: "Project default" })).toBeInTheDocument();
	});

	it("disables agent selectors while the initial agent catalog is loading", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return new Promise(() => {});
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						roleMapSha256: defaultRoleMapSHA256,
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "agents");

		expect(await screen.findByRole("button", { name: "Default worker agent" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Default orchestrator agent" })).toBeDisabled();
	});

	it("offers both interactive Kiro and Pi reviewers", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "workflow");
		const reviewer = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewer);
		const labels = (await screen.findAllByRole("menuitem")).map((option) => option.textContent);
		expect(labels).toContain("KiroAuth unknown");
		expect(labels).toContain("Pi");
	});

	it("offers Muse Code as a reviewer", async () => {
		const muse = { id: "muse", label: "Muse Code", authStatus: "authorized" };
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "muse" },
				orchestrator: { agent: "claude-code" },
			},
		});
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [...agentCatalogResponse.data.supported, muse],
						installed: [...agentCatalogResponse.data.installed, muse],
						authorized: [...agentCatalogResponse.data.authorized, muse],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "muse" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewer = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewer);

		expect(await screen.findByRole("menuitem", { name: /Muse Code/ })).toBeInTheDocument();
	});

	it("orders reviewers using the default agent priority", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "workflow");

		await userEvent.click(await screen.findByRole("button", { name: "Default reviewer agent" }));
		const reviewerLabels = (await screen.findAllByRole("menuitem"))
			.map((option) => option.textContent)
			.filter((label) => label !== "Project default");

		expect(reviewerLabels).toEqual([
			"Claude Code",
			"Codex",
			"Cursor",
			"OpenCode",
			"GitHub Copilot",
			"Goose",
			"Kilo Code",
			"Pi",
			"KiroAuth unknown",
		]);
	});

	it("offers the experimental host-trusted reviewer set", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: { worker: { agent: "qwen" }, orchestrator: { agent: "claude-code" } },
		};
		const qwen = { id: "qwen", label: "Qwen Code", authStatus: "authorized" };
		const devin = { id: "devin", label: "Devin", authStatus: "authorized" };
		const droid = { id: "droid", label: "Droid", authStatus: "authorized" };
		const kimi = { id: "kimi", label: "Kimi", authStatus: "authorized" };
		const aider = { id: "aider", label: "Aider", authStatus: "authorized" };
		const amp = { id: "amp", label: "Amp", authStatus: "authorized" };
		const experimental = [
			{ id: "agy", label: "Agy", authStatus: "authorized" },
			{ id: "auggie", label: "Auggie", authStatus: "authorized" },
			{ id: "autohand", label: "Autohand", authStatus: "authorized" },
			{ id: "cline", label: "Cline", authStatus: "authorized" },
			{ id: "continue", label: "Continue", authStatus: "authorized" },
			{ id: "crush", label: "Crush", authStatus: "authorized" },
			{ id: "grok", label: "Grok", authStatus: "authorized" },
			{ id: "vibe", label: "Vibe", authStatus: "authorized" },
		];
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [...agentCatalogResponse.data.supported, qwen, devin, droid, kimi, aider, amp, ...experimental],
						installed: [...agentCatalogResponse.data.installed, qwen, devin, droid, kimi, aider, amp, ...experimental],
						authorized: [...agentCatalogResponse.data.authorized, qwen, devin, droid, kimi, aider, amp, ...experimental],
					},
					error: undefined,
				};
			}
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewer = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewer);
		const options = await screen.findAllByRole("menuitem");
		const labels = options.map((option) => option.textContent);
		expect(labels).toContain("Qwen Code");
		expect(labels).toContain("Agy");
		expect(labels).toContain("Continue");
		expect(labels).toContain("Goose");
		expect(labels).toContain("Vibe");
		expect(labels).toContain("Devin");
		expect(labels).toContain("Droid");
		expect(labels).toContain("Kimi");
		expect(labels).toContain("Aider");
		expect(labels).toContain("Amp");
		expect(labels).toContain("Auggie");
		expect(labels).toContain("Autohand");
		expect(labels).toContain("Cline");
		expect(labels).toContain("Crush");
		expect(labels).toContain("Grok");
	});

	it("warns when an experimental reviewer is selected", async () => {
		const kimchi = { id: "kimchi", label: "Kimchi", authStatus: "authorized" };
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [...agentCatalogResponse.data.supported, kimchi],
						installed: [...agentCatalogResponse.data.installed, kimchi],
						authorized: [...agentCatalogResponse.data.authorized, kimchi],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: { worker: { agent: "codex" }, orchestrator: { agent: "claude-code" } },
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "workflow");
		await chooseOption(await screen.findByRole("button", { name: "Default reviewer agent" }), "Kimchi");
		expect(screen.getByRole("status")).toHaveTextContent("Experimental host-trusted reviewer");
	});

	it("shows unknown-auth agents as selectable with a warning in project settings", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "agents");

		const workerAgent = await screen.findByRole("button", { name: "Default worker agent" });
		await userEvent.click(workerAgent);
		const options = await screen.findAllByRole("menuitem");
		expect(options.map((option) => option.textContent)).toEqual([
			"Claude Code",
			"Codex",
			"Cursor",
			"OpenCode",
			"GitHub Copilot",
			"Goose",
			"Kilo Code",
			"Pi",
			"KiroAuth unknown",
		]);
		expect(options[8]).not.toHaveAttribute("aria-disabled", "true");
	});

	it("shows Copilot as a reviewer option and saves it in the reviewers payload", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewer = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewer);
		const copilot = await screen.findByRole("menuitem", { name: "GitHub Copilot" });
		expect(copilot).not.toHaveAttribute("aria-disabled", "true");
		await userEvent.click(copilot);
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith(
			"/api/v1/projects/{id}",
			expect.objectContaining({
				body: expect.objectContaining({
					config: expect.objectContaining({ reviewers: [{ harness: "copilot" }] }),
				}),
			}),
		);
	});

	it("disables the Copilot reviewer when its binary is missing", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [{ id: "copilot", label: "GitHub Copilot" }],
						installed: [],
						authorized: [],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "workflow");

		await userEvent.click(await screen.findByRole("button", { name: "Default reviewer agent" }));
		const copilot = (await screen.findAllByRole("menuitem")).find((option) =>
			option.textContent?.includes("GitHub Copilot"),
		);
		expect(copilot).toHaveTextContent("Needs install");
		expect(copilot).toHaveAttribute("aria-disabled", "true");
	});

	it("shows the standard unknown-auth warning for an installed Copilot reviewer", async () => {
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [{ id: "copilot", label: "GitHub Copilot" }],
						installed: [{ id: "copilot", label: "GitHub Copilot", authStatus: "unknown" }],
						authorized: [],
					},
					error: undefined,
				};
			}
			return {
				data: {
					status: "ok",
					project: {
						id: "proj-1",
						name: "Project One",
						kind: "single_repo",
						path: "/repo/project-one",
						repo: "",
						defaultBranch: "main",
						config: {
							worker: { agent: "codex" },
							orchestrator: { agent: "claude-code" },
						},
					},
				},
				error: undefined,
			};
		});

		renderSettings("proj-1", undefined, "workflow");

		await userEvent.click(await screen.findByRole("button", { name: "Default reviewer agent" }));
		const copilot = (await screen.findAllByRole("menuitem")).find((option) =>
			option.textContent?.includes("GitHub Copilot"),
		);
		expect(copilot).toHaveTextContent("Auth unknown");
		expect(copilot).not.toHaveAttribute("aria-disabled", "true");
	});

	it("offers Kilo Code as a configured reviewer", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
			},
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewer = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewer);
		expect(await screen.findByRole("menuitem", { name: "Kilo Code" })).toBeEnabled();
	});

	it("offers the experimental Agy reviewer", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: { worker: { agent: "agy" }, orchestrator: { agent: "claude-code" } },
		};
		const agy = { id: "agy", label: "Agy", authStatus: "authorized" };
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [...agentCatalogResponse.data.supported, agy],
						installed: [...agentCatalogResponse.data.installed, agy],
						authorized: [...agentCatalogResponse.data.authorized, agy],
					},
					error: undefined,
				};
			}
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "workflow");

		const reviewerAgent = await screen.findByRole("button", { name: "Default reviewer agent" });
		await userEvent.click(reviewerAgent);
		const options = await screen.findAllByRole("menuitem");
		expect(options.map((option) => option.textContent)).toContain("Agy");
	});

	it("shows scratch identity and saves only scratch-supported settings", async () => {
		mockProject({
			id: "scratch",
			name: "Scratch",
			kind: "scratch",
			path: "/home/me/.ao/scratch/default",
			repo: "",
			defaultBranch: "",
			config: {
				defaultBranch: "main",
				sessionPrefix: "ao",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				agentRules: "keep work small",
				worker: { agent: "codex" },
				orchestrator: { agent: "claude-code" },
				agentConfig: {
					model: "gpt-5-codex",
					permissions: "auto",
				},
				reviewers: [{ harness: "codex" }],
				trackerIntake: { enabled: true, provider: "github", assignee: "octocat" },
			},
		});

		renderSettings("scratch");

		const kindRow = (await screen.findByText("Type")).closest(".settings-row-bar");
		expect(kindRow).toHaveTextContent("Scratch project");
		expect(screen.queryByLabelText("Default branch")).not.toBeInTheDocument();
		expect(screen.queryByLabelText("Session prefix")).not.toBeInTheDocument();
		expect(screen.queryByText("Reviewers")).not.toBeInTheDocument();
		expect(screen.queryByText("Tracker intake")).not.toBeInTheDocument();

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}", {
			params: { path: { id: "scratch" } },
			body: {
				displayName: "Scratch",
				expectedRoleMapSha256: defaultRoleMapSHA256,
				config: {
					env: { FOO: "bar" },
					sessionPrefix: "ao",
					symlinks: [".env"],
					postCreate: ["npm install"],
					agentRules: "keep work small",
					worker: { agent: "codex", agentConfig: { model: "gpt-5-codex" } },
					orchestrator: { agent: "claude-code", agentConfig: { model: "gpt-5-codex" } },
					agentConfig: {
						permissions: "auto",
					},
				},
			},
		});
		expect(postMock).not.toHaveBeenCalled();
	});

	it("saves GitHub tracker intake settings, deriving the repo from the project's git origin", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					roleMapSha256: defaultRoleMapSHA256,
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "intake");

		await userEvent.click(await screen.findByLabelText("Enable issue intake"));

		// Repository is display-only, derived from the project's own git origin — no input to
		// fill. Assignee is the only eligibility rule in v1.
		expect(screen.getByRole("link", { name: "acme/project-one" })).toHaveAttribute(
			"href",
			"https://github.com/acme/project-one",
		);
		await userEvent.type(screen.getByLabelText("Assignee"), "octocat");

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		const body = putMock.mock.calls[0]?.[1]?.body;
		expect(body.config.trackerIntake).toEqual({
			enabled: true,
			provider: "github",
			assignee: "octocat",
		});
	});

	it("blocks save when intake is enabled with no assignee", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					roleMapSha256: defaultRoleMapSHA256,
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "git@github.com:acme/project-one.git",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", undefined, "intake");

		await userEvent.click(await screen.findByLabelText("Enable issue intake"));
		submitSettings();

		expect(await screen.findAllByText("Enabling intake requires an assignee.")).toHaveLength(2);
		expect(putMock).not.toHaveBeenCalled();
	});

	it("restarts when the saved orchestrator agent already differs from the running orchestrator", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					roleMapSha256: defaultRoleMapSHA256,
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "goose" },
					},
				},
			},
			error: undefined,
		});

		renderSettings("proj-1", [
			{
				id: "proj-1",
				name: "Project One",
				path: "/repo/project-one",
				orchestratorAgent: "goose",
				sessions: [
					{
						id: "proj-1-orchestrator",
						workspaceId: "proj-1",
						workspaceName: "Project One",
						title: "Orchestrator",
						provider: "claude-code",
						kind: "orchestrator",
						branch: "ao/proj-1-orchestrator",
						status: "working",
						createdAt: "2026-07-03T00:00:00Z",
						updatedAt: "2026-07-03T00:00:00Z",
						prs: [],
					},
				],
			},
		], "agents");

		const orchestratorAgent = await screen.findByRole("button", { name: "Default orchestrator agent" });
		expect(orchestratorAgent).toHaveTextContent("goose");

		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(postMock).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj-1", clean: true },
		});
	});

	it("keeps the config save successful when orchestrator replacement fails", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "ok",
				project: {
					id: "proj-1",
					roleMapSha256: defaultRoleMapSHA256,
					name: "Project One",
					kind: "single_repo",
					path: "/repo/project-one",
					repo: "",
					defaultBranch: "main",
					config: {
						worker: { agent: "codex" },
						orchestrator: { agent: "claude-code" },
					},
				},
			},
			error: undefined,
		});
		postMock.mockResolvedValue({
			data: undefined,
			error: { message: "missing goose binary" },
			response: { status: 500 },
		});

		const queryClient = renderSettings("proj-1", undefined, "agents");
		const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");

		const orchestratorAgent = await screen.findByRole("button", { name: "Default orchestrator agent" });
		await chooseOption(orchestratorAgent, "goose");
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		expect(await screen.findByText("Saved")).toBeInTheDocument();
		expect(await screen.findByText("Orchestrator restart failed: missing goose binary")).toBeInTheDocument();
		expect(screen.queryByText("Save failed")).not.toBeInTheDocument();
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["project", "proj-1"] });
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
	});

	it("sends an exact role-map CAS payload with false permissions and ordered failover", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "git@github.com:acme/project-one.git",
			defaultBranch: "main",
			roleMapSha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			config: {
				defaultBranch: "develop",
				env: { FOO: "bar" },
				symlinks: [".env"],
				postCreate: ["npm install"],
				trackerIntake: { enabled: true, repo: "acme/project-one", assignee: "octocat" },
				worker: { agent: "codex", agentConfig: { model: "worker-model" } },
				orchestrator: { agent: "claude-code" },
				roleMap: {
					role_map_schema_version: 1,
					strictDelegation: false,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
						implementor: {
							template: "implementor",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
							when: ["backend"],
						},
						reviewer: {
							template: "reviewer",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
					failover: {
						mode: "manual",
						roles: {
							implementor: [
								{ harness: "claude-code", model: "sonnet" },
								{ harness: "codex", model: "gpt-5" },
							],
							reviewer: [{ harness: "claude-code" }],
						},
					},
				},
			},
		});

		renderSettings("proj-1", undefined, "roles");
		expect(await screen.findByRole("button", { name: "Remove role orchestrator" })).toBeDisabled();
		await userEvent.click(await screen.findByRole("switch", { name: "Strict delegation" }));

		const implementorCard = screen.getByRole("group", { name: "implementor" });
		expect(implementorCard).not.toBeNull();
		const firstRungModel = within(implementorCard).getByRole("textbox", { name: "Model for implementor failover rung 1" });
		await userEvent.clear(firstRungModel);
		await userEvent.type(firstRungModel, "claude-sonnet");
		expect(firstRungModel).toHaveValue("claude-sonnet");
		expect(firstRungModel).toHaveFocus();
		await userEvent.click(within(implementorCard).getByRole("button", { name: "Move rung down — implementor rung 1" }));

		await userEvent.click(screen.getByRole("button", { name: "Remove role reviewer" }));
		await userEvent.type(screen.getByRole("textbox", { name: "New role ID" }), "verifier");
		await userEvent.click(screen.getByRole("button", { name: "Add role" }));
		submitSettings();

		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
		expect(putMock).toHaveBeenCalledWith("/api/v1/projects/{id}/role-map", {
			params: { path: { id: "proj-1" } },
			body: {
				expectedRoleMapSha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				roleMap: {
						role_map_schema_version: 1,
						strictDelegation: true,
						orchestratorRole: "orchestrator",
						roles: {
							orchestrator: {
								template: "orchestrator",
								harness: "claude-code",
								permissions: { workspaceWrites: true, canSpawn: true },
							},
							implementor: {
								template: "implementor",
								harness: "codex",
								permissions: { workspaceWrites: true, canSpawn: false },
								when: ["backend"],
							},
							verifier: {
								template: "verifier",
								harness: "codex",
								permissions: { workspaceWrites: true, canSpawn: false },
							},
						},
						failover: {
							mode: "manual",
							roles: {
								implementor: [
									{ harness: "codex", model: "gpt-5" },
									{ harness: "claude-code", model: "claude-sonnet" },
								],
							},
						},
				},
			},
		});
		expect(postMock).not.toHaveBeenCalled();
	});

	it("refuses an invalid strict role map before mutation", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
				},
			},
		});

		renderSettings("proj-1", undefined, "roles");
		await userEvent.click(await screen.findByRole("switch", { name: "Strict delegation" }));
		submitSettings();

		expect(await screen.findByText("Strict delegation requires role “orchestrator” to have spawn permission.")).toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("does not author a failover rung when no alternative harness is available", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
					},
				},
			},
		};
		getMock.mockImplementation(async (path: string) => {
			if (path === "/api/v1/agents") {
				return {
					data: {
						supported: [{ id: "claude-code", label: "Claude Code" }],
						installed: [{ id: "claude-code", label: "Claude Code", authStatus: "authorized" }],
						authorized: [{ id: "claude-code", label: "Claude Code", authStatus: "authorized" }],
					},
					error: undefined,
				};
			}
			return { data: { status: "ok", project }, error: undefined };
		});

		renderSettings("proj-1", undefined, "roles");
		const addRung = await screen.findByRole("button", { name: "Add failover rung for orchestrator" });
		expect(addRung).toBeDisabled();
		expect(addRung).toHaveAttribute("aria-describedby", "no-alternative-harness-orchestrator");
		expect(screen.getByText("No alternative harness is available for orchestrator.")).toBeVisible();
		await userEvent.click(addRung);
		expect(screen.queryByRole("textbox", { name: /orchestrator failover rung/i })).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("discards an unsaved role-map draft without mutation", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					strictDelegation: false,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
						implementor: {
							template: "implementor",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
					failover: {
						mode: "manual",
						roles: {
							implementor: [
								{ harness: "claude-code", model: "sonnet" },
								{ harness: "goose", model: "llama" },
							],
						},
					},
				},
			},
		});

		renderSettings("proj-1", undefined, "roles");
		const strict = await screen.findByRole("switch", { name: "Strict delegation" });
		const oldSecondRung = screen.getByRole("textbox", { name: "Model for implementor failover rung 2" });
		await userEvent.click(strict);
		await userEvent.click(screen.getByRole("button", { name: "Remove rung — implementor rung 1" }));
		expect(strict).toBeChecked();
		await userEvent.click(screen.getByRole("button", { name: "Discard role-map changes" }));
		expect(screen.getByRole("switch", { name: "Strict delegation" })).not.toBeChecked();
		const restoredFirstRung = screen.getByRole("textbox", { name: "Model for implementor failover rung 1" });
		expect(restoredFirstRung).toHaveValue("sonnet");
		expect(screen.getByRole("textbox", { name: "Model for implementor failover rung 2" })).toHaveValue("llama");
		expect(restoredFirstRung).not.toBe(oldSecondRung);
		expect(putMock).not.toHaveBeenCalled();
	});

	it("renders an unreadable project as identity-only and exposes no mutations", async () => {
		getMock.mockResolvedValue({
			data: {
				status: "degraded",
				project: {
					id: "broken-project",
					name: "Broken Project",
					kind: "single_repo",
					path: "/repo/broken",
					resolveError: "project config is unreadable: role_map_schema_version 99",
				},
			},
			error: undefined,
		});

		renderSettings("broken-project", undefined, "roles");
		expect(await screen.findByRole("alert")).toHaveTextContent("Broken Project");
		expect(screen.getByRole("alert")).toHaveTextContent("project config is unreadable: role_map_schema_version 99");
		expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
		expect(screen.queryByRole("switch", { name: "Strict delegation" })).not.toBeInTheDocument();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("keeps the draft and exact daemon validation error when capability validation rejects save", async () => {
		mockProject({
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					strictDelegation: false,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
						implementor: {
							template: "implementor",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
				},
			},
		});
		putMock.mockResolvedValue({
			data: undefined,
			error: { message: "roles[orchestrator]: claude-code does not enforce read-only workspace access" },
		});

		renderSettings("proj-1", undefined, "roles");
		const strict = await screen.findByRole("switch", { name: "Strict delegation" });
		await userEvent.click(strict);
		await userEvent.click(screen.getByRole("switch", { name: "Workspace writes for orchestrator" }));
		submitSettings();

		expect(await screen.findByText("roles[orchestrator]: claude-code does not enforce read-only workspace access")).toBeInTheDocument();
		expect(strict).toBeChecked();
		expect(screen.queryByText("Saved.")).not.toBeInTheDocument();
	});

	it("keeps a stale draft on conflict, then reloads revision B for the next CAS", async () => {
		const project = {
			id: "proj-1",
			name: "Project One",
			kind: "single_repo",
			path: "/repo/project-one",
			repo: "",
			defaultBranch: "main",
			roleMapSha256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			config: {
				roleMap: {
					role_map_schema_version: 1,
					strictDelegation: false,
					orchestratorRole: "orchestrator",
					roles: {
						orchestrator: {
							template: "orchestrator",
							harness: "claude-code",
							permissions: { workspaceWrites: true, canSpawn: true },
						},
						implementor: {
							template: "implementor",
							harness: "codex",
							permissions: { workspaceWrites: true, canSpawn: false },
						},
					},
				},
			},
		};
		mockProject(project);
		putMock.mockResolvedValueOnce({
			data: undefined,
			error: { code: "PROJECT_ROLE_MAP_CONFLICT", message: "Role map changed since it was loaded; reload before saving" },
		});

		renderSettings("proj-1", undefined, "roles");
		const strict = await screen.findByRole("switch", { name: "Strict delegation" });
		await userEvent.click(strict);
		submitSettings();

		expect(await screen.findByText("Role map changed since it was loaded; reload before saving")).toBeInTheDocument();
		expect(strict).toBeChecked();
		expect(screen.queryByText("Saved.")).not.toBeInTheDocument();

		project.roleMapSha256 = "abababababababababababababababababababababababababababababababab";
		project.config = {
			...project.config,
			roleMap: {
				...project.config.roleMap,
				strictDelegation: false,
				roles: {
					...project.config.roleMap.roles,
					orchestrator: {
						...project.config.roleMap.roles.orchestrator,
						template: "orchestrator-v2",
					},
				},
			},
		};
		await userEvent.click(screen.getByRole("button", { name: "Reload latest role map" }));
		await waitFor(() => expect(screen.getByRole("switch", { name: "Strict delegation" })).not.toBeChecked());
		expect(screen.getByRole("textbox", { name: "Template profile ID for orchestrator" })).toHaveValue("orchestrator-v2");
		expect(screen.queryByText("Role map changed since it was loaded; reload before saving")).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("switch", { name: "Strict delegation" }));
		submitSettings();
		await waitFor(() => expect(putMock).toHaveBeenCalledTimes(2));
		expect(putMock).toHaveBeenNthCalledWith(2, "/api/v1/projects/{id}/role-map", {
			params: { path: { id: "proj-1" } },
			body: {
				expectedRoleMapSha256: "abababababababababababababababababababababababababababababababab",
				roleMap: expect.objectContaining({
					strictDelegation: true,
					roles: expect.objectContaining({
						orchestrator: expect.objectContaining({ template: "orchestrator-v2" }),
					}),
				}),
			},
		});
	});
});
