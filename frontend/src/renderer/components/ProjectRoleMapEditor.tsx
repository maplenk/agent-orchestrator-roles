import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import type { TFunction } from "i18next";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { components } from "../../api/schema";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Switch } from "./ui/switch";
import { SettingsRow } from "./settings/SettingsRow";
import { SettingsSection } from "./settings/SettingsSection";

export type RoleMap = components["schemas"]["DomainRoleMap"];
type RoleBinding = components["schemas"]["DomainRoleBinding"];
type FailoverTarget = components["schemas"]["DomainFailoverTarget"];

export type RoleHarnessOption = {
	id: string;
	label: string;
};

const ROLE_ID_PATTERN = /^[a-z0-9_-]+$/;

export function createRoleMap(workerHarness: string, orchestratorHarness: string): RoleMap {
	return {
		role_map_schema_version: 1,
		strictDelegation: false,
		orchestratorRole: "orchestrator",
		roles: {
			orchestrator: {
				harness: orchestratorHarness || "claude-code",
				template: "orchestrator",
				permissions: { canSpawn: true, workspaceWrites: true },
			},
			implementor: {
				harness: workerHarness || "codex",
				template: "implementor",
				permissions: { canSpawn: false, workspaceWrites: true },
			},
		},
		failover: { mode: "manual", roles: {} },
	};
}

export function validateRoleMapDraft(roleMap: RoleMap | undefined, t: TFunction): string | null {
	if (!roleMap) return t("settings.roles.validation.enable");
	if (roleMap.role_map_schema_version !== 1) {
		return t("settings.roles.validation.unsupportedSchema", { schema: roleMap.role_map_schema_version });
	}
	if (roleMap.failover?.mode === "automatic") {
		return t("settings.roles.validation.automaticUnavailable");
	}
	const roles = roleMap.roles ?? {};
	const roleIDs = Object.keys(roles);
	if (roleIDs.length === 0) return t("settings.roles.validation.addRole");
	const orchestratorRole = roleMap.orchestratorRole?.trim() || "orchestrator";
	if (!roles[orchestratorRole]) return t("settings.roles.validation.orchestratorMissing", { role: orchestratorRole });
	for (const roleID of roleIDs) {
		if (!ROLE_ID_PATTERN.test(roleID)) {
			return t("settings.roles.validation.roleId", { role: roleID });
		}
		const binding = roles[roleID];
		const template = binding.template.trim();
		if (!template) return t("settings.roles.validation.templateRequired", { role: roleID });
		if (template.toLowerCase().endsWith(".md") || template.includes("/") || template.includes("\\")) {
			return t("settings.roles.validation.templateProfile", { role: roleID });
		}
		if (!binding.harness.trim()) return t("settings.roles.validation.harnessRequired", { role: roleID });
		if (binding.model?.trim().toLowerCase() === "default") {
			return t("settings.roles.validation.modelDefault", { role: roleID });
		}
	}
	if (roleMap.strictDelegation && !roles[orchestratorRole].permissions.canSpawn) {
		return t("settings.roles.validation.strictSpawn", { role: orchestratorRole });
	}
	for (const [roleID, targets] of Object.entries(roleMap.failover?.roles ?? {})) {
		if (!roles[roleID]) return t("settings.roles.validation.failoverRole", { role: roleID });
		const binding = roles[roleID];
		const seenTargets = new Set([effectiveTargetKey(binding.harness, binding.model)]);
		for (const [index, target] of targets.entries()) {
			if (!target.harness.trim()) return t("settings.roles.validation.failoverHarness", { role: roleID, rung: index + 1 });
			if (target.model?.trim().toLowerCase() === "default") {
				return t("settings.roles.validation.failoverModel", { role: roleID, rung: index + 1 });
			}
			const targetKey = effectiveTargetKey(target.harness, target.model);
			if (seenTargets.has(targetKey)) {
				return t("settings.roles.validation.failoverDuplicate", { role: roleID, rung: index + 1 });
			}
			seenTargets.add(targetKey);
		}
	}
	return null;
}

export function ProjectRoleMapEditor({
	value,
	onChange,
	harnesses,
	defaultWorkerHarness,
	defaultOrchestratorHarness,
}: {
	value: RoleMap | undefined;
	onChange: (value: RoleMap) => void;
	harnesses: RoleHarnessOption[];
	defaultWorkerHarness: string;
	defaultOrchestratorHarness: string;
}) {
	const { t } = useTranslation();
	const [newRoleID, setNewRoleID] = useState("");
	const [addError, setAddError] = useState<string | null>(null);
	const nextRungID = useRef(0);
	const editorRootRef = useRef<HTMLDivElement>(null);
	const [rungKeys, setRungKeys] = useState<Record<string, string[]>>(() => initialRungKeys(value));
	const [pendingRungFocus, setPendingRungFocus] = useState<{ key: string; action: "up" | "down" } | null>(null);

	useEffect(() => {
		if (!pendingRungFocus) return;
		const button = editorRootRef.current?.querySelector<HTMLButtonElement>(
			`button[data-rung-key="${pendingRungFocus.key}"][data-rung-action="${pendingRungFocus.action}"]`,
		);
		button?.focus();
		setPendingRungFocus(null);
	}, [pendingRungFocus, value]);

	if (!value) {
		return (
			<SettingsSection title={t("settings.roles.title")}>
				<p className="px-1 text-xs leading-5 text-settings-muted">{t("settings.roles.description")}</p>
				<Button
					type="button"
					variant="outline"
					onClick={() => onChange(createRoleMap(defaultWorkerHarness, defaultOrchestratorHarness))}
				>
					<Plus aria-hidden="true" />
					{t("settings.roles.enable")}
				</Button>
			</SettingsSection>
		);
	}

	const roles = value.roles ?? {};
	const roleIDs = Object.keys(roles);
	const orchestratorRole = value.orchestratorRole?.trim() || "orchestrator";
	const failoverMode = value.failover?.mode || "manual";
	const harnessOptions = withCurrentHarnesses(harnesses, value);
	const updateBinding = (roleID: string, patch: Partial<RoleBinding>) => {
		const binding = roles[roleID];
		if (!binding) return;
		onChange({
			...value,
			roles: {
				...roles,
				[roleID]: { ...binding, ...patch },
			},
		});
	};
	const updateTargets = (roleID: string, targets: FailoverTarget[]) => {
		const failoverRoles = { ...(value.failover?.roles ?? {}) };
		if (targets.length > 0) failoverRoles[roleID] = targets;
		else delete failoverRoles[roleID];
		onChange({
			...value,
			failover: {
				...value.failover,
				mode: value.failover?.mode || "manual",
				roles: failoverRoles,
			},
		});
	};
	const addTarget = (roleID: string, targets: FailoverTarget[], target: FailoverTarget) => {
		setRungKeys((current) => ({
			...current,
			[roleID]: [...(current[roleID] ?? []), `draft-rung-${++nextRungID.current}`],
		}));
		updateTargets(roleID, [...targets, target]);
	};
	const moveTarget = (roleID: string, targets: FailoverTarget[], from: number, to: number) => {
		const rungKey = rungKeys[roleID]?.[from];
		if (rungKey) {
			setPendingRungFocus({ key: rungKey, action: to < from ? "down" : "up" });
		}
		setRungKeys((current) => ({ ...current, [roleID]: move(current[roleID] ?? [], from, to) }));
		updateTargets(roleID, move(targets, from, to));
	};
	const removeTarget = (roleID: string, targets: FailoverTarget[], index: number) => {
		setRungKeys((current) => ({
			...current,
			[roleID]: (current[roleID] ?? []).filter((_, keyIndex) => keyIndex !== index),
		}));
		updateTargets(roleID, targets.filter((_, targetIndex) => targetIndex !== index));
	};
	const addRole = () => {
		const roleID = newRoleID.trim();
		if (!ROLE_ID_PATTERN.test(roleID)) {
			setAddError(t("settings.roles.invalidRoleId"));
			return;
		}
		if (roles[roleID]) {
			setAddError(t("settings.roles.duplicateRole"));
			return;
		}
		onChange({
			...value,
			roles: {
				...roles,
				[roleID]: {
					harness: defaultWorkerHarness || harnessOptions[0]?.id || "codex",
					template: roleID,
					permissions: { canSpawn: false, workspaceWrites: true },
				},
			},
		});
		setNewRoleID("");
		setAddError(null);
	};
	const removeRole = (roleID: string) => {
		if (roleID === orchestratorRole) return;
		const nextRoles = { ...roles };
		delete nextRoles[roleID];
		const failoverRoles = { ...(value.failover?.roles ?? {}) };
		delete failoverRoles[roleID];
		setRungKeys((current) => {
			const next = { ...current };
			delete next[roleID];
			return next;
		});
		onChange({
			...value,
			roles: nextRoles,
			failover: { ...value.failover, roles: failoverRoles },
		});
	};

	return (
		<div ref={editorRootRef} className="contents">
			<SettingsSection title={t("settings.roles.title")}>
				<p className="px-1 text-xs leading-5 text-settings-muted">{t("settings.roles.description")}</p>
				<SettingsRow label={t("settings.roles.strictDelegation")}>
					<Switch
						aria-label={t("settings.roles.strictDelegation")}
						checked={value.strictDelegation ?? false}
						onCheckedChange={(strictDelegation) => onChange({ ...value, strictDelegation })}
					/>
				</SettingsRow>
				<SettingsRow label={t("settings.roles.orchestratorRole")}>
					<select
						aria-label={t("settings.roles.orchestratorRole")}
						className="settings-inline-input"
						value={orchestratorRole}
						onChange={(event) => onChange({ ...value, orchestratorRole: event.target.value })}
					>
						{roleIDs.map((roleID) => (
							<option key={roleID} value={roleID}>{roleID}</option>
						))}
					</select>
				</SettingsRow>
				<SettingsRow label={t("settings.roles.failoverMode")}>
					<span className="text-sm text-foreground">
						{failoverMode === "automatic"
							? t("settings.roles.automaticModeUnavailable")
							: t("settings.roles.manualMode")}
					</span>
				</SettingsRow>
				<p className="px-1 text-xs leading-5 text-settings-muted">{t("settings.roles.bindingPolicyNewSessions")}</p>
				<p className="px-1 text-xs leading-5 text-settings-muted">{t("settings.roles.failoverCurrentPolicy")}</p>
				{failoverMode === "automatic" && (
					<div role="alert" className="mx-1 flex flex-col items-start gap-2 rounded-md border border-warning/40 bg-warning/10 p-3">
						<p className="text-xs leading-5 text-warning">{t("settings.roles.automaticUnavailable")}</p>
						<Button
							type="button"
							variant="outline"
							size="sm"
							onClick={() => onChange({
								...value,
								failover: { ...value.failover, mode: "manual", roles: value.failover?.roles ?? {} },
							})}
						>
							{t("settings.roles.convertToManual")}
						</Button>
					</div>
				)}
			</SettingsSection>

			<SettingsSection title={t("settings.roles.bindings")}>
				{roleIDs.map((roleID) => {
					const binding = roles[roleID];
					const targets = value.failover?.roles?.[roleID] ?? [];
					const alternativeHarness = firstAlternativeHarness(binding.harness, harnessOptions);
					return (
						<fieldset key={roleID} className="flex flex-col gap-3 rounded-lg border border-border bg-card/40 p-3">
							<legend className="sr-only">{roleID}</legend>
							<div className="flex items-center justify-between gap-3">
								<span className="text-sm font-semibold text-foreground" aria-hidden="true">{roleID}</span>
								<Button
									type="button"
									variant="ghost"
									size="icon-sm"
									disabled={roleID === orchestratorRole}
									aria-label={t("settings.roles.removeRole", { role: roleID })}
									title={roleID === orchestratorRole ? t("settings.roles.cannotRemoveOrchestrator") : undefined}
									onClick={() => removeRole(roleID)}
								>
									<Trash2 aria-hidden="true" />
								</Button>
							</div>
							<div className="grid gap-3 sm:grid-cols-2">
								<EditorField label={t("settings.roles.template")}>
									<Input
										aria-label={t("settings.roles.fieldAria", { field: t("settings.roles.template"), role: roleID })}
										value={binding.template}
										onChange={(event) => updateBinding(roleID, { template: event.target.value })}
									/>
								</EditorField>
								<EditorField label={t("settings.roles.harness")}>
									<select
										aria-label={t("settings.roles.fieldAria", { field: t("settings.roles.harness"), role: roleID })}
										className="h-control-form w-full rounded-md border border-transparent bg-input/50 px-3 text-sm"
										value={binding.harness}
										onChange={(event) => updateBinding(roleID, { harness: event.target.value })}
									>
										{harnessOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
									</select>
								</EditorField>
								<EditorField label={t("settings.roles.model")}>
									<Input
										aria-label={t("settings.roles.fieldAria", { field: t("settings.roles.model"), role: roleID })}
										placeholder={t("settings.roles.providerDefault")}
										value={binding.model ?? ""}
										onChange={(event) => updateBinding(roleID, optionalStringPatch("model", event.target.value))}
									/>
								</EditorField>
								<EditorField label={t("settings.roles.when")}>
									<Input
										aria-label={t("settings.roles.fieldAria", { field: t("settings.roles.when"), role: roleID })}
										placeholder={t("settings.roles.whenPlaceholder")}
										value={(binding.when ?? []).join(", ")}
										onChange={(event) => updateBinding(roleID, optionalArrayPatch("when", event.target.value))}
									/>
								</EditorField>
							</div>
							<div className="grid gap-2 sm:grid-cols-2">
								<PolicySwitch
									label={t("settings.roles.workspaceWrites")}
									ariaLabel={t("settings.roles.fieldAria", { field: t("settings.roles.workspaceWrites"), role: roleID })}
									checked={binding.permissions.workspaceWrites}
									onCheckedChange={(workspaceWrites) => updateBinding(roleID, {
										permissions: { ...binding.permissions, workspaceWrites },
									})}
								/>
								<PolicySwitch
									label={t("settings.roles.canSpawn")}
									ariaLabel={t("settings.roles.fieldAria", { field: t("settings.roles.canSpawn"), role: roleID })}
									checked={binding.permissions.canSpawn}
									onCheckedChange={(canSpawn) => updateBinding(roleID, {
										permissions: { ...binding.permissions, canSpawn },
									})}
								/>
							</div>

							<div className="flex flex-col gap-2 border-t border-border pt-3">
								<div className="flex items-center justify-between gap-3">
									<div>
										<p className="text-xs font-semibold uppercase tracking-wide text-settings-muted">{t("settings.roles.failover")}</p>
									</div>
									<div className="flex flex-col items-end gap-1">
										<Button
											type="button"
											variant="outline"
											size="sm"
											disabled={!alternativeHarness}
											aria-label={t("settings.roles.addRungAria", { role: roleID })}
											aria-describedby={!alternativeHarness ? `no-alternative-harness-${roleID}` : undefined}
											onClick={() => {
												if (alternativeHarness) addTarget(roleID, targets, { harness: alternativeHarness });
											}}
										>
											<Plus aria-hidden="true" />
											{t("settings.roles.addRung")}
										</Button>
										{!alternativeHarness && (
											<p id={`no-alternative-harness-${roleID}`} className="max-w-56 text-right text-xs text-settings-muted">
												{t("settings.roles.noAlternativeHarness", { role: roleID })}
											</p>
										)}
									</div>
								</div>
								{targets.length === 0 ? (
									<p className="text-xs text-settings-muted">{t("settings.roles.noFailover")}</p>
				) : targets.map((target, index) => (
					<div key={rungKeys[roleID]?.[index] ?? `initial-rung-${roleID}-${index}`} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto] items-center gap-2">
										<select
											aria-label={t("settings.roles.rungHarnessAria", { role: roleID, rung: index + 1 })}
											className="h-control-form min-w-0 rounded-md border border-transparent bg-input/50 px-3 text-sm"
											value={target.harness}
											onChange={(event) => updateTargets(roleID, replaceAt(targets, index, { ...target, harness: event.target.value }))}
										>
											{harnessOptions.map((option) => <option key={option.id} value={option.id}>{option.label}</option>)}
										</select>
										<Input
											aria-label={t("settings.roles.rungModelAria", { role: roleID, rung: index + 1 })}
											placeholder={t("settings.roles.providerDefault")}
											value={target.model ?? ""}
											onChange={(event) => updateTargets(roleID, replaceAt(targets, index, {
												...target,
												...(event.target.value ? { model: event.target.value } : {}),
												...(event.target.value ? {} : { model: undefined }),
											}))}
										/>
										<div className="flex items-center">
											<RungButton rungKey={rungKeys[roleID]?.[index]} action="up" label={t("settings.roles.rungActionAria", { action: t("settings.roles.moveUp"), role: roleID, rung: index + 1 })} disabled={index === 0} onClick={() => moveTarget(roleID, targets, index, index - 1)}><ArrowUp /></RungButton>
											<RungButton rungKey={rungKeys[roleID]?.[index]} action="down" label={t("settings.roles.rungActionAria", { action: t("settings.roles.moveDown"), role: roleID, rung: index + 1 })} disabled={index === targets.length - 1} onClick={() => moveTarget(roleID, targets, index, index + 1)}><ArrowDown /></RungButton>
											<RungButton rungKey={rungKeys[roleID]?.[index]} action="remove" label={t("settings.roles.rungActionAria", { action: t("settings.roles.removeRung"), role: roleID, rung: index + 1 })} onClick={() => removeTarget(roleID, targets, index)}><Trash2 /></RungButton>
										</div>
									</div>
								))}
							</div>
						</fieldset>
					);
				})}
				<div className="flex items-start gap-2 rounded-lg border border-dashed border-border p-3">
					<div className="min-w-0 flex-1">
						<Input
							id="new-role-id"
							aria-label={t("settings.roles.newRoleId")}
							aria-invalid={addError ? true : undefined}
							aria-describedby={addError ? "new-role-id-error" : undefined}
							placeholder={t("settings.roles.newRolePlaceholder")}
							value={newRoleID}
							onChange={(event) => {
								setNewRoleID(event.target.value);
								setAddError(null);
							}}
							onKeyDown={(event) => {
								if (event.key === "Enter") {
									event.preventDefault();
									addRole();
								}
							}}
						/>
						{addError && <p id="new-role-id-error" role="alert" className="mt-1 text-xs text-error">{addError}</p>}
					</div>
					<Button type="button" variant="outline" onClick={addRole}>
						<Plus aria-hidden="true" />
						{t("settings.roles.addRole")}
					</Button>
				</div>
			</SettingsSection>
		</div>
	);
}

function EditorField({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<label className="flex min-w-0 flex-col gap-1 text-xs font-medium text-settings-muted">
			{label}
			{children}
		</label>
	);
}

function PolicySwitch({ label, ariaLabel, checked, onCheckedChange }: { label: string; ariaLabel: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
	return (
		<label className="flex items-center justify-between gap-3 rounded-md bg-input/30 px-3 py-2 text-xs text-foreground">
			{label}
			<Switch aria-label={ariaLabel} checked={checked} onCheckedChange={onCheckedChange} />
		</label>
	);
}

function RungButton({ rungKey, action, label, disabled, onClick, children }: { rungKey?: string; action: "up" | "down" | "remove"; label: string; disabled?: boolean; onClick: () => void; children: React.ReactNode }) {
	return (
		<Button type="button" variant="ghost" size="icon-sm" data-rung-key={rungKey} data-rung-action={action} aria-label={label} title={label} disabled={disabled} onClick={onClick}>
			{children}
		</Button>
	);
}

function optionalStringPatch<K extends "model">(key: K, value: string): Partial<RoleBinding> {
	return value ? { [key]: value } : { [key]: undefined };
}

function optionalArrayPatch<K extends "when">(key: K, value: string): Partial<RoleBinding> {
	const items = value.split(",").map((item) => item.trim()).filter(Boolean);
	return items.length > 0 ? { [key]: items } : { [key]: undefined };
}

function replaceAt<T>(items: T[], index: number, value: T): T[] {
	return items.map((item, itemIndex) => itemIndex === index ? value : item);
}

function move<T>(items: T[], from: number, to: number): T[] {
	if (to < 0 || to >= items.length) return items;
	const next = [...items];
	const [item] = next.splice(from, 1);
	next.splice(to, 0, item);
	return next;
}

function firstAlternativeHarness(current: string, harnesses: RoleHarnessOption[]): string | undefined {
	return harnesses.find((option) => option.id !== current)?.id;
}

function effectiveTargetKey(harness: string, model: string | undefined): string {
	return `${harness.trim()}\u0000${model?.trim() ?? ""}`;
}

function initialRungKeys(roleMap: RoleMap | undefined): Record<string, string[]> {
	return Object.fromEntries(Object.entries(roleMap?.failover?.roles ?? {}).map(([roleID, targets]) => [
		roleID,
		targets.map((_, index) => `initial-rung-${roleID}-${index}`),
	]));
}

function withCurrentHarnesses(harnesses: RoleHarnessOption[], roleMap: RoleMap): RoleHarnessOption[] {
	const byID = new Map(harnesses.map((option) => [option.id, option]));
	for (const binding of Object.values(roleMap.roles ?? {})) {
		if (binding.harness && !byID.has(binding.harness)) byID.set(binding.harness, { id: binding.harness, label: binding.harness });
	}
	for (const targets of Object.values(roleMap.failover?.roles ?? {})) {
		for (const target of targets) {
			if (target.harness && !byID.has(target.harness)) byID.set(target.harness, { id: target.harness, label: target.harness });
		}
	}
	return [...byID.values()];
}
