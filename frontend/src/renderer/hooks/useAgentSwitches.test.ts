import { describe, expect, it } from "vitest";
import type { AgentSwitch } from "./useAgentSwitches";
import {
	agentSwitchNeedsManualDelivery,
	agentSwitchNeedsRecovery,
	agentSwitchesRefetchInterval,
} from "./useAgentSwitches";

function switchRecord(overrides: Partial<AgentSwitch> = {}): AgentSwitch {
	return {
		agentHandoffStatus: "not_attempted",
		fromHarness: "claude-code",
		id: "switch-1",
		requestedAt: "2026-06-10T00:00:00Z",
		semanticHandoffIncluded: true,
		sessionId: "session-1",
		state: "starting_target",
		targetHarness: "codex",
		updatedAt: "2026-06-10T00:00:01Z",
		...overrides,
	};
}

describe("agentSwitchesRefetchInterval", () => {
	it.each([
		["polls an ordinary active switch", {}, 1_000],
		["stops eager polling when a durable recovery marker is present", { errorCode: "target_start_unconfirmed" }, false],
		["does not poll terminal history", { state: "completed" }, false],
	] as const)("%s", (_name, overrides, expected) => {
		expect(agentSwitchesRefetchInterval([switchRecord(overrides)])).toBe(expected);
	});
});

describe("agent switch operator actions", () => {
	it("requires safe recovery only for the nonterminal ambiguous-owner marker", () => {
		expect(agentSwitchNeedsRecovery(switchRecord({ errorCode: "target_start_unconfirmed" }))).toBe(true);
		expect(
			agentSwitchNeedsRecovery(
				switchRecord({ errorCode: "delivery_unconfirmed", state: "failed" }),
			),
		).toBe(false);
	});

	it("routes ambiguous delivery to manual guidance instead of recovery", () => {
		expect(
			agentSwitchNeedsManualDelivery(
				switchRecord({ errorCode: "delivery_unconfirmed", state: "failed" }),
			),
		).toBe(true);
		expect(
			agentSwitchNeedsManualDelivery(switchRecord({ errorCode: "target_start_unconfirmed" })),
		).toBe(false);
	});
});
