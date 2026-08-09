import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";

vi.mock("./ProjectSettingsForm", () => ({
	ProjectSettingsForm: ({ section }: { section: string }) => <div data-testid="project-settings-section">{section}</div>,
}));

vi.mock("./GlobalSettingsForm", () => ({
	GlobalSettingsForm: () => <div>global settings</div>,
}));

import { SettingsDialog } from "./SettingsDialog";

afterEach(() => {
	useUiStore.setState({ settingsModal: null });
});

describe("SettingsDialog project navigation", () => {
	it("exposes the role-map editor through the project settings sidebar", async () => {
		useUiStore.setState({ settingsModal: { scope: "project", projectId: "proj-1" } });
		render(<SettingsDialog />);

		const roles = await screen.findByRole("button", { name: "Roles" });
		await userEvent.click(roles);

		expect(roles).toHaveAttribute("aria-current", "page");
		expect(screen.getByTestId("project-settings-section")).toHaveTextContent("roles");
	});
});
