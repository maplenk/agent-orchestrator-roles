import { existsSync, readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { resolveRoleProfilesDir } from "./role-profiles";

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "..", "..");
const repoRoot = resolve(frontendRoot, "..");

describe("resolveRoleProfilesDir", () => {
	it("ships the bundled resource for a packaged app", () => {
		// The packaged daemon's cwd is ~/.ao and a clean install has nothing under
		// the data dir, so its own fallback roots resolve to nothing. This value is
		// the ONLY thing that makes shipped templates reachable on a fresh install.
		expect(resolveRoleProfilesDir({}, true, "/Applications/AO.app/Contents/Resources", "/app")).toBe(
			"/Applications/AO.app/Contents/Resources/profiles",
		);
	});

	it("uses the repository profiles in dev, not the staged copy", () => {
		// Source tree, so editing a template takes effect without repackaging.
		expect(resolveRoleProfilesDir({}, false, "/unused", "/repo/frontend")).toBe("/repo/frontend/../profiles");
	});

	it("lets an explicit override win in both modes", () => {
		const env = { AO_ROLE_PROFILES_DIR: "/approved/templates" };
		expect(resolveRoleProfilesDir(env, true, "/res", "/app")).toBe("/approved/templates");
		expect(resolveRoleProfilesDir(env, false, "/res", "/app")).toBe("/approved/templates");
	});

	it("ignores a blank override rather than passing an empty root", () => {
		expect(resolveRoleProfilesDir({ AO_ROLE_PROFILES_DIR: "   " }, true, "/res", "/app")).toBe("/res/profiles");
	});
});

describe("desktop packaging", () => {
	const pkg = JSON.parse(readFileSync(join(frontendRoot, "package.json"), "utf8"));
	const forge = readFileSync(join(frontendRoot, "forge.config.ts"), "utf8");

	// The body of Forge's prePackage hook, which runs for package, make and
	// publish alike — unlike npm's `prepackage`, which fires only for
	// `npm run package`.
	const prePackage = forge.match(/prePackage:\s*async\s*\([^)]*\)\s*=>\s*\{([\s\S]*?)\n\t\t\},/)?.[1] ?? "";

	// Does `npm run <entry>` end up staging profiles? npm runs `pre<entry>` then
	// `<entry>`; Forge's hook additionally covers any command that runs Forge.
	function stagesProfiles(entry: string): boolean {
		const chain = [pkg.scripts[`pre${entry}`], pkg.scripts[entry]].filter(Boolean).join(" && ");
		if (chain.includes("stage:profiles")) return true;
		return chain.includes("electron-forge") && prePackage.includes("stage-profiles.mjs");
	}

	it("lists profiles as an extraResource", () => {
		// A clean desktop install with no profiles resource cannot launch a strict
		// role-pinned orchestrator at all. This is the packaging half of that
		// guarantee; resolveRoleProfilesDir is the runtime half, and both are
		// required.
		const match = forge.match(/extraResource:\s*\[([^\]]*)\]/);
		expect(match, "forge.config.ts has no extraResource array").toBeTruthy();
		expect(match?.[1]).toContain('"profiles"');
	});

	// The real release entrypoints, not the convenient one. CI publishes with
	// `npm run publish` (frontend-release.yml, feature-release.yml) and builds
	// artifacts with `npm run make` (build-artifacts.yml, desktop-testing.yml,
	// testing-build.yml). No workflow runs `npm run package`, so staging wired
	// only into `prepackage` shipped a template-less bundle from every release
	// path — masked locally by a left-over, gitignored frontend/profiles.
	it.each(["package", "make", "publish"])("stages profiles for `npm run %s`", (entry) => {
		expect(pkg.scripts[entry], `no ${entry} script to release with`).toBeTruthy();
		expect(stagesProfiles(entry)).toBe(true);
	});

	it("has role templates in the repository to stage", () => {
		// The staging script fails closed on an empty source, but that only helps
		// if the source is expected to exist at all.
		expect(existsSync(join(repoRoot, "profiles", "orchestrator.md"))).toBe(true);
	});
});
