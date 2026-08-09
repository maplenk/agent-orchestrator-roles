import { expect, test } from "@playwright/test";

// dev:web (VITE_NO_ELECTRON=1) serves lib/mock-data.ts. The ao-demo workspace
// owns a review-stack session carrying three active PRs plus a merged base —
// the multi-PR-per-session case this suite guards across the inspector rail.

test("the inspector rail stacks every PR a session owns, actionable-first", async ({ page }) => {
	await page.goto("/");
	await page.getByRole("button", { name: "Open Review stacked browser preview flow" }).click();
	await expect(page).toHaveURL(/sessions\/demo-review-stack/);

	const inspector = page.locator("#inspector");
	await expect(inspector).toBeVisible();
	await inspector.getByRole("tab", { name: "Summary" }).click();

	// Plural heading reflects the stack size.
	await expect(inspector.getByText("Pull requests (4)")).toBeVisible();

	// One card per PR, ordered actionable open → draft → merged (the merged
	// base sinks). Scope to the PR section: Activity repeats the PR numbers.
	const prSection = inspector.getByTestId("inspector-section").filter({ hasText: "Pull requests (4)" });
	const cards = prSection.getByRole("link", { name: /Open PR #/ });
	await expect(cards).toHaveText(["PR #319", "PR #320", "PR #321", "PR #317"]);
});
