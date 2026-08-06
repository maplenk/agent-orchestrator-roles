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
	it("lists profiles as an extraResource", () => {
		// A clean desktop install with no profiles resource cannot launch a strict
		// role-pinned orchestrator at all. This is the packaging half of that
		// guarantee; resolveRoleProfilesDir is the runtime half, and both are
		// required.
		const forge = readFileSync(join(frontendRoot, "forge.config.ts"), "utf8");
		const match = forge.match(/extraResource:\s*\[([^\]]*)\]/);
		expect(match, "forge.config.ts has no extraResource array").toBeTruthy();
		expect(match?.[1]).toContain('"profiles"');
	});

	it("stages profiles before dev and before packaging", () => {
		// Staging only on one of the two hooks would leave dev and packaged
		// disagreeing about whether templates exist.
		const pkg = JSON.parse(readFileSync(join(frontendRoot, "package.json"), "utf8"));
		expect(pkg.scripts["stage:profiles"]).toBeTruthy();
		expect(pkg.scripts.predev).toContain("stage:profiles");
		expect(pkg.scripts.prepackage).toContain("stage:profiles");
	});

	it("has role templates in the repository to stage", () => {
		// The staging script fails closed on an empty source, but that only helps
		// if the source is expected to exist at all.
		expect(existsSync(join(repoRoot, "profiles", "orchestrator.md"))).toBe(true);
	});
});
