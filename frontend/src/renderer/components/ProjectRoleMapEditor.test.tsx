import { describe, expect, it } from "vitest";
import { appI18n } from "../i18n";
import { validateRoleMapDraft, type RoleMap } from "./ProjectRoleMapEditor";

describe("role-map editor validation", () => {
	it("returns validation copy through the active locale catalog", () => {
		const strictOrchestratorWithoutSpawn: RoleMap = {
			role_map_schema_version: 1,
			strictDelegation: true,
			orchestratorRole: "orchestrator",
			roles: {
				orchestrator: {
					template: "orchestrator",
					harness: "claude-code",
					permissions: { workspaceWrites: true, canSpawn: false },
				},
			},
		};

		expect(validateRoleMapDraft(strictOrchestratorWithoutSpawn, appI18n.getFixedT("de"))).toBe(
			"Strikte Delegation erfordert für die Rolle „orchestrator“ die Berechtigung zum Starten von Agenten.",
		);
	});

	it("rejects a failover rung that repeats the current target", () => {
		const repeatedTarget: RoleMap = {
			role_map_schema_version: 1,
			orchestratorRole: "orchestrator",
			roles: {
				orchestrator: {
					template: "orchestrator",
					harness: "claude-code",
					permissions: { workspaceWrites: true, canSpawn: true },
				},
			},
			failover: { mode: "manual", roles: { orchestrator: [{ harness: "claude-code" }] } },
		};

		expect(validateRoleMapDraft(repeatedTarget, appI18n.getFixedT("en"))).toBe(
			"Failover rung 1 for “orchestrator” must differ from the current target or any earlier rung.",
		);
	});

	it("accepts strict orchestrator-only maps and same-harness model alternatives", () => {
		const valid: RoleMap = {
			role_map_schema_version: 1,
			strictDelegation: true,
			orchestratorRole: "orchestrator",
			roles: {
				orchestrator: {
					template: "orchestrator",
					harness: "claude-code",
					permissions: { workspaceWrites: true, canSpawn: true },
				},
			},
			failover: {
				mode: "manual",
				roles: { orchestrator: [{ harness: "claude-code", model: "opus" }] },
			},
		};

		expect(validateRoleMapDraft(valid, appI18n.getFixedT("en"))).toBeNull();
	});

	it("blocks a legacy automatic map until the user explicitly converts it", () => {
		const automatic: RoleMap = {
			role_map_schema_version: 1,
			orchestratorRole: "orchestrator",
			roles: {
				orchestrator: {
					template: "orchestrator",
					harness: "claude-code",
					permissions: { workspaceWrites: true, canSpawn: true },
				},
			},
			failover: { mode: "automatic", roles: {} },
		};

		expect(validateRoleMapDraft(automatic, appI18n.getFixedT("en"))).toBe(
			"Convert automatic failover to manual before saving. Reviewed structured limit detection is not promoted for any harness.",
		);
	});
});
