import { expect, test } from "@playwright/test";

// The Playwright web server runs `dev:web` (VITE_NO_ELECTRON=1), so
// useWorkspaceQuery serves the deterministic preview fixtures from
// lib/mock-data.ts instead of hitting a daemon. The tests run in Chromium
// (no window.ao), so the terminal shows its browser-preview surface.

test("renders the orchestrator-first workbench shell", async ({ page }) => {
	await page.goto("/");
	// The global board anchor + the Projects group + a worker row.
	await expect(page.getByRole("button", { name: "Orchestrator board" })).toBeVisible();
	await expect(page.getByText("Projects")).toBeVisible();
	await expect(page.getByRole("button", { name: "Open Build screenshot-ready dashboard data" })).toBeVisible();
	await expect(page.getByRole("region", { name: "Working sessions", exact: true })).toBeVisible();
});

test("deep-links into a worker session", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");
	// Worker view = persistent project sidebar, terminal, and inspector rail.
	await expect(page.getByTestId("session-detail")).toBeVisible();
	await expect(page.getByTestId("session-terminal")).toBeVisible();
	await expect(page.locator("#inspector")).toBeVisible();
});

test("drilling into a worker opens its inspector rail", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "Open Build screenshot-ready dashboard data" }).click();
	await expect(page).toHaveURL(/sessions\/demo-working/);
	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await expect(inspector.getByRole("tab", { name: "Summary" })).toHaveAttribute("aria-selected", "true");
});
