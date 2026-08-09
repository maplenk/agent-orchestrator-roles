import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. Reviews now live in the
// inspector Summary pane, and preview mode supplies its own deterministic
// review/config data instead of issuing daemon requests.

test("the Summary pane renders the reviewer panel for a session that owns PRs", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-ready");
	await expect(page).toHaveURL(/sessions\/demo-ready/);

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();

	// The reviewer card surfaces the harness, its approved verdict, and the rerun
	// action — never the empty state, since this session owns a PR.
	await expect(inspector.getByText("No pull request opened yet.")).toHaveCount(0);
	await expect(inspector.getByRole("button", { name: "Select reviewer agent" })).toContainText("codex");
	await expect(inspector.getByText("Approved")).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Re-run review" })).toBeVisible();
	await expect(inspector.getByRole("button", { name: "Open terminal" })).toHaveCount(0);
});

test("the Summary pane shows the empty state for a session with no PRs", async ({ page }) => {
	await page.goto("/#/projects/ao-demo/sessions/demo-working");
	await expect(page).toHaveURL(/sessions\/demo-working/);

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();

	await expect(inspector.getByText("No pull request opened yet.")).toBeVisible();
	await expect(inspector.getByText("Run review", { exact: true })).toHaveCount(0);
});
