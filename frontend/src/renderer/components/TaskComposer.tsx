import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "./ui/button";
import { Label } from "./ui/label";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { captureRendererEvent } from "../lib/telemetry";
import { agentsQueryKey, agentsQueryOptions, refreshAgents } from "../hooks/useAgentsQuery";
import {
	agentModelsQueryKey,
	agentModelsQueryOptions,
	refreshAgentModels,
	revalidateAgentModels,
	type AgentModelCatalog,
} from "../hooks/useAgentModelsQuery";
import { AgentModelCombobox } from "./settings/AgentModelCombobox";
import { SettingsOptionMenu } from "./settings/SettingsOptionMenu";

type Project = components["schemas"]["Project"];
type DelegateAgent = components["schemas"]["DelegateTaskRequest"]["agent"];

type RoleMap = NonNullable<components["schemas"]["ProjectConfig"]["roleMap"]>;

type CreateTaskInput = {
	projectId: string;
	brief: string;
	agent?: DelegateAgent;
	model?: string;
	roleId?: string;
};

/**
 * Roles a worker may be delegated to, in a stable order.
 *
 * The orchestrator role is excluded: delegation spawns a worker, and a strict
 * map auto-binds the orchestrator role for KindOrchestrator only — offering it
 * here would be offering a target the daemon would refuse.
 */
export function delegatableRoles(map: RoleMap | undefined): string[] {
	if (!map?.roles) return [];
	const orchestratorRole = map.orchestratorRole ?? "orchestrator";
	return Object.keys(map.roles)
		.filter((id) => id !== orchestratorRole)
		.sort((a, b) => a.localeCompare(b));
}

const newTaskSelectSurfaceClass =
	"h-control-form w-full flex-1 justify-between rounded-md border border-transparent bg-input/50 px-3 py-2 text-control text-foreground transition-[color,box-shadow,background-color,border-color] hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30";

export type TaskComposerProps = {
	projectId?: string;
	onCreated: (sessionId: string) => void;
	onCancel?: () => void;
	onDirtyChange?: (dirty: boolean) => void;
	onSubmittingChange?: (submitting: boolean) => void;
	autoFocusTitle?: boolean;
};

export function TaskComposer({
	projectId,
	onCreated,
	onCancel,
	onDirtyChange,
	onSubmittingChange,
	autoFocusTitle,
}: TaskComposerProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const promptId = useId();
	const modelId = useId();
	const agentId = useId();
	const [prompt, setPrompt] = useState("");
	const [model, setModel] = useState("");
	const [mode, setMode] = useState("");
	const [agent, setAgent] = useState("");
	const [role, setRole] = useState("");
	const [agentTouched, setAgentTouched] = useState(false);
	const [modelTouched, setModelTouched] = useState(false);
	const [isSubmitting, setIsSubmitting] = useState(false);
	const [error, setError] = useState<string | undefined>();
	const createTask = useCallback(
		async (input: CreateTaskInput): Promise<string> => {
			void captureRendererEvent("ao.renderer.task_create_requested", { project_id: input.projectId });
			try {
				const { data, error } = await apiClient.POST("/api/v1/orchestrators/delegate", {
					body: {
						projectId: input.projectId,
						brief: input.brief,
						agent: input.agent,
						model: input.model,
						roleId: input.roleId,
					},
				});
				if (error) throw new Error(apiErrorMessage(error, t("newTask.unableToStart")));
				if (!data?.workerId) throw new Error(t("newTask.noSession"));
				void captureRendererEvent("ao.renderer.task_create_succeeded", { project_id: input.projectId });
				return data.workerId;
			} catch (err) {
				void captureRendererEvent("ao.renderer.task_create_failed", { project_id: input.projectId });
				void queryClient.invalidateQueries({ queryKey: agentsQueryKey });
				throw err instanceof Error ? err : new Error(t("newTask.unableToStart"));
			}
		},
		[queryClient, t],
	);

	const projectQuery = useQuery({
		queryKey: ["project", projectId],
		enabled: Boolean(projectId),
		queryFn: async () => {
			const { data, error: apiError } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId as string } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError));
			if (data?.status !== "ok") throw new Error(t("newTask.configUnavailable"));
			return data.project as Project;
		},
	});
	const agentsQuery = useQuery(agentsQueryOptions);
	const refreshAgentsMutation = useMutation({
		mutationFn: refreshAgents,
		onSuccess: (next) => queryClient.setQueryData(agentsQueryKey, next),
	});
	const defaultWorkerAgent = projectQuery.data?.config?.worker?.agent ?? "";
	const selectedAgent = agent || defaultWorkerAgent;
	const defaultWorkerModel =
		projectQuery.data?.config?.worker?.agentConfig?.model ?? projectQuery.data?.config?.agentConfig?.model ?? "";
	const defaultWorkerMode =
		projectQuery.data?.config?.worker?.agentConfig?.mode ?? projectQuery.data?.config?.agentConfig?.mode ?? "";
	const defaultModelForSelectedAgent = selectedAgent === defaultWorkerAgent ? defaultWorkerModel : "";
	const defaultModeForSelectedAgent = selectedAgent === defaultWorkerAgent ? defaultWorkerMode : "";
	const agentCatalog = agentsQuery.data;

	// Strict delegation is the daemon's rule, read here only to stop offering
	// inputs it would refuse. The composer never decides that a spawn is legal —
	// it declines to send a shape already known to be rejected.
	const roleMap = projectQuery.data?.config?.roleMap;
	const strictDelegation = roleMap?.strictDelegation === true;
	const roleOptions = delegatableRoles(roleMap);
	// Until the config has loaded, strictness is unknown. Submitting a free-form
	// agent in that window is exactly the request a strict map rejects, so the
	// composer waits rather than guessing.
	//
	// A FAILED read is the same situation wearing different clothes: isPending
	// goes false and data stays undefined, so `strictDelegation` reads false and
	// the free-form form would come back — treating "we could not find out" as
	// "not strict". Unknown has to stay unknown, and the human needs to see why.
	const configPending = Boolean(projectId) && projectQuery.isPending;
	const configUnavailable = Boolean(projectId) && projectQuery.isError;
	const configError =
		projectQuery.error instanceof Error ? projectQuery.error.message : undefined;
	const noDelegatableRole = strictDelegation && roleOptions.length === 0;
	const binding = strictDelegation && role ? roleMap?.roles?.[role] : undefined;

	useEffect(() => {
		if (!agentTouched) setAgent(defaultWorkerAgent);
	}, [agentTouched, defaultWorkerAgent]);
	// A role id is only meaningful inside one project's map.
	useEffect(() => setRole(""), [projectId]);
	useEffect(() => {
		if (!modelTouched) {
			setModel(defaultModelForSelectedAgent);
			setMode(defaultModeForSelectedAgent);
		}
	}, [defaultModelForSelectedAgent, defaultModeForSelectedAgent, modelTouched]);

	const isDirty = prompt.trim() !== "" || modelTouched;
	useEffect(() => {
		onDirtyChange?.(isDirty);
	}, [isDirty, onDirtyChange]);
	useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

	useEffect(() => {
		onSubmittingChange?.(isSubmitting);
	}, [isSubmitting, onSubmittingChange]);
	useEffect(() => () => onSubmittingChange?.(false), [onSubmittingChange]);

	const submit = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (!projectId || isSubmitting) return;

		const cleanPrompt = prompt.trim();
		const cleanModel = model.trim();
		const cleanMode = mode.trim();
		const requestedModel =
			modelTouched && (cleanModel !== defaultModelForSelectedAgent || cleanMode !== defaultModeForSelectedAgent)
				? cleanModel || cleanMode || undefined
				: undefined;
		if (!cleanPrompt) {
			setError(t("newTask.taskRequired"));
			return;
		}
		if (configUnavailable) {
			// The disabled button is an affordance; this is the gate. A form
			// submitted by Enter, or by a test, must hit the same rule.
			setError(t("newTask.configUnavailable"));
			return;
		}
		if (strictDelegation && !role) {
			setError(t("newTask.roleRequired"));
			return;
		}

		setIsSubmitting(true);
		setError(undefined);
		try {
			// Under a strict map the role carries the harness and the model, and
			// sending either alongside it is HARNESS_OVERRIDE_FORBIDDEN. So they
			// are not merely hidden in the UI — they are not sent.
			const sessionId = await createTask(
				strictDelegation
					? { projectId, brief: prompt, roleId: role }
					: {
							projectId,
							brief: prompt,
							agent: agentTouched && agent ? (agent as CreateTaskInput["agent"]) : undefined,
							model: requestedModel,
					  },
			);
			onCreated(sessionId);
		} catch (err) {
			setError(err instanceof Error ? err.message : t("newTask.unableToStart"));
		} finally {
			setIsSubmitting(false);
		}
	};

	return (
		<form onSubmit={submit} className="space-y-4 p-(--size-modal-padding)">
			<div className="space-y-1.5">
				<div className="flex items-center justify-between">
					<label className="text-xs font-medium text-muted-foreground" htmlFor={promptId}>
						{t("newTask.task")}
					</label>
				</div>
				<div className="rounded-md border border-border transition">
					<textarea
						id={promptId}
						autoFocus={autoFocusTitle}
						className="min-h-textarea-min w-full resize-y rounded-md bg-transparent px-3 py-2 text-control leading-relaxed text-foreground outline-none transition placeholder:text-passive focus-visible:border-accent focus-visible:ring-2 focus-visible:ring-accent-weak"
						placeholder={t("newTask.taskPlaceholder")}
						value={prompt}
						onChange={(event) => setPrompt(event.target.value)}
						onKeyDown={(event) => {
							if (event.key === "Enter" && !event.shiftKey && !event.altKey && !event.nativeEvent.isComposing) {
								event.preventDefault();
								event.currentTarget.form?.requestSubmit();
							}
						}}
					/>
				</div>
				<p className="text-caption text-muted-foreground">{t("newTask.enterHint")}</p>
			</div>

			{configPending ? (
				<p className="text-caption text-muted-foreground">{t("newTask.configLoading")}</p>
			) : configUnavailable ? (
				<div
					role="alert"
					className="space-y-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
				>
					<p>{t("newTask.configUnavailable")}</p>
					{configError ? <p className="font-mono break-all">{configError}</p> : null}
					<button
						type="button"
						className="underline underline-offset-2 hover:no-underline disabled:pointer-events-none disabled:opacity-50"
						disabled={projectQuery.isFetching}
						onClick={() => void projectQuery.refetch()}
					>
						{t("newTask.configRetry")}
					</button>
				</div>
			) : noDelegatableRole ? (
				<p className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
					{t("newTask.noWorkerRole")}
				</p>
			) : strictDelegation ? (
				<div className="space-y-1.5">
					<Label className="text-xs font-medium text-muted-foreground">{t("newTask.role")}</Label>
					<SettingsOptionMenu
						aria-label={t("newTask.role")}
						value={role || "__none__"}
						options={[
							{ value: "__none__", label: t("newTask.rolePlaceholder") },
							...roleOptions.map((id) => ({ value: id, label: id })),
						]}
						triggerClassName={newTaskSelectSurfaceClass}
						onChange={(next) => setRole(next === "__none__" ? "" : next)}
					/>
					{/* The binding is shown, never edited: it is what the daemon will
					    launch, and an editable copy of it would be a target the map
					    could reject. */}
					<p className="text-caption text-muted-foreground">
						{binding ? `${binding.harness}${binding.model ? ` · ${binding.model}` : ""}` : t("newTask.roleLocked")}
					</p>
				</div>
			) : (
				<div className="grid gap-3 sm:grid-cols-[1fr_1fr]">
					<div className="space-y-1.5">
						<RequiredAgentField
							id={agentId}
							label={t("newTask.agent")}
							placeholder={t("newTask.projectDefault")}
							value={agent}
							authorized={agentCatalog?.authorized}
							installed={agentCatalog?.installed}
							supported={agentCatalog?.supported}
							disabled={agentsQuery.isFetching && agentCatalog === undefined}
							onChange={(value) => {
								setAgent(value);
								setAgentTouched(true);
								setModelTouched(false);
							}}
						/>
						<button
							type="button"
							className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:pointer-events-none disabled:opacity-50"
							disabled={refreshAgentsMutation.isPending}
							onClick={() => refreshAgentsMutation.mutate()}
						>
							{refreshAgentsMutation.isPending ? t("newTask.refreshingAgents") : t("newTask.refreshAgents")}
						</button>
					</div>
					<div className="space-y-1.5">
						<Label className="text-xs font-medium text-muted-foreground" htmlFor={modelId}>
							{t("newTask.model")}
						</Label>
						<TaskModelPicker
							id={modelId}
							agentId={selectedAgent}
							projectId={projectId ?? ""}
							value={model}
							mode={mode}
							onModelChange={(value) => {
								setModel(value);
								setMode("");
								setModelTouched(true);
							}}
							onModeChange={(value) => {
								setMode(value);
								setModel("");
								setModelTouched(true);
							}}
						/>
					</div>
				</div>
			)}

			{error && (
				<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
					{error}
				</div>
			)}

			{refreshAgentsMutation.isError && (
				<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
					{refreshAgentsMutation.error instanceof Error
						? refreshAgentsMutation.error.message
						: t("newTask.refreshFailed")}
				</div>
			)}

			<div className="flex items-center justify-end gap-3 pt-1">
				{onCancel && (
					<Button type="button" variant="footer" disabled={isSubmitting} onClick={onCancel}>
						{t("newTask.cancel")}
					</Button>
				)}
				<Button type="submit" variant="footer-primary" disabled={isSubmitting || !projectId || configPending || configUnavailable || noDelegatableRole}>
					{isSubmitting ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> : null}
					{isSubmitting ? t("newTask.starting") : t("newTask.start")}
				</Button>
			</div>
		</form>
	);
}

function TaskModelPicker({
	id,
	agentId,
	projectId,
	value,
	mode,
	onModelChange,
	onModeChange,
}: {
	id: string;
	agentId: string;
	projectId: string;
	value: string;
	mode: string;
	onModelChange: (value: string) => void;
	onModeChange: (value: string) => void;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [customAgentId, setCustomAgentId] = useState<string | null>(null);
	const query = useQuery(agentModelsQueryOptions(agentId, projectId));
	const catalog: AgentModelCatalog | undefined = query.data;
	const revalidationQuery = useQuery({
		queryKey: ["agent-model-revalidation", agentId, projectId, catalog?.validatedAt ?? ""],
		queryFn: () => revalidateAgentModels(agentId, projectId),
		enabled: agentId !== "" && catalog?.refreshRecommended === true,
		staleTime: Number.POSITIVE_INFINITY,
		retry: false,
	});
	useEffect(() => {
		if (revalidationQuery.data) {
			queryClient.setQueryData(agentModelsQueryKey(agentId, projectId), revalidationQuery.data);
		}
	}, [agentId, projectId, queryClient, revalidationQuery.data]);
	const refreshMutation = useMutation({
		mutationFn: () => refreshAgentModels(agentId, projectId),
		onSuccess: (catalog) => queryClient.setQueryData(agentModelsQueryKey(agentId, projectId), catalog),
	});
	const warning =
		(refreshMutation.isError
			? refreshMutation.error instanceof Error
				? refreshMutation.error.message
				: t("settings.models.refreshFailed")
			: undefined) ??
		(revalidationQuery.isError
			? revalidationQuery.error instanceof Error
				? revalidationQuery.error.message
				: t("settings.models.validateFailed")
			: undefined) ??
		catalog?.warning ??
		(query.isError ? (query.error instanceof Error ? query.error.message : t("settings.models.loadFailed")) : undefined);

	if (catalog?.selectionMode === "mode") {
		const options = [
			{ value: "__default__", label: t("settings.models.agentDefault") },
			...(catalog.models ?? []).map((item) => ({ value: item.id, label: item.label })),
		];
		return (
			<>
				<div className="flex min-w-0 items-center gap-2">
					<SettingsOptionMenu
						aria-label={t("newTask.model")}
						value={mode || "__default__"}
						options={options}
						triggerClassName={newTaskSelectSurfaceClass}
						onChange={(nextMode) => onModeChange(nextMode === "__default__" ? "" : nextMode)}
					/>
				</div>
				<TaskModelRefreshButton
					pending={refreshMutation.isPending}
					disabled={agentId === ""}
					onClick={() => refreshMutation.mutate()}
				/>
				{warning && <p className="text-xs text-warning">{warning}</p>}
			</>
		);
	}

	const hasCatalog = catalog?.selectionMode === "catalog" && (catalog.models?.length ?? 0) > 0;
	const modelIsInCatalog = catalog?.models?.some((item) => item.id === value) ?? false;
	const showCustomInput = hasCatalog && (customAgentId === agentId || (value !== "" && !modelIsInCatalog));
	const selectCatalogModel = (nextModel: string) => {
		setCustomAgentId(null);
		onModelChange(nextModel);
	};
	const selectCustomModel = (nextModel: string) => {
		setCustomAgentId(agentId);
		onModelChange(nextModel);
	};

	return (
		<>
			<div className="flex min-w-0 items-center gap-2">
				{hasCatalog && !showCustomInput ? (
					<AgentModelCombobox
						aria-label={t("newTask.model")}
						value={value}
						models={catalog.models ?? []}
						allowCustom={catalog.allowCustom}
						onChange={selectCatalogModel}
						onCustom={selectCustomModel}
						triggerClassName={newTaskSelectSurfaceClass}
					/>
				) : (
					<>
						<input
							id={id}
							className="h-control-form flex min-w-0 flex-1 rounded-md border border-transparent bg-input/50 px-3 py-2 text-control text-foreground outline-none transition-[color,box-shadow,background-color,border-color] placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/30 disabled:cursor-not-allowed disabled:opacity-50"
							value={value}
							disabled={agentId === ""}
							onChange={(event) => onModelChange(event.target.value)}
							placeholder={query.isFetching ? t("settings.models.loading") : t("newTask.projectDefault")}
						/>
						{hasCatalog && (
							<AgentModelCombobox
								aria-label={t("settings.models.optionsAria", { label: t("newTask.model") })}
								value={value}
								models={catalog.models ?? []}
								allowCustom={catalog.allowCustom}
								onChange={selectCatalogModel}
								onCustom={selectCustomModel}
								triggerLabel={t("settings.models.browse")}
								triggerClassName="shrink-0"
							/>
						)}
					</>
				)}
			</div>
			<TaskModelRefreshButton
				pending={refreshMutation.isPending}
				disabled={agentId === ""}
				onClick={() => refreshMutation.mutate()}
			/>
			{warning && <p className="text-xs text-warning">{warning}</p>}
		</>
	);
}

function TaskModelRefreshButton({
	pending,
	disabled,
	onClick,
}: {
	pending: boolean;
	disabled: boolean;
	onClick: () => void;
}) {
	const { t } = useTranslation();
	return (
		<button
			type="button"
			className="text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:pointer-events-none disabled:opacity-50"
			disabled={disabled || pending}
			onClick={onClick}
		>
			{pending ? t("newTask.refreshingModels") : t("newTask.refreshModels")}
		</button>
	);
}
