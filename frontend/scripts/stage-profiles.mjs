// Stage the repository's shipped role templates into frontend/profiles so
// electron-forge can ship them as an extraResource.
//
// Without this the desktop bundle contains no role templates at all: the
// packaged daemon runs with cwd ~/.ao and a clean install has nothing under the
// data dir, so a strict role-pinned orchestrator fails to launch until someone
// copies profiles/ in by hand. Mirrors scripts/build-daemon.mjs, which stages
// the daemon binary the same way and runs from the same predev/prepackage hooks.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(scriptsDir, "..");
const repoRoot = resolve(frontendRoot, "..");
const sourceDir = join(repoRoot, "profiles");
const outDir = join(frontendRoot, "profiles");

if (!existsSync(sourceDir)) {
	console.error(`Role profiles not found at ${sourceDir}. The desktop bundle would ship no role templates.`);
	process.exit(1);
}

const templates = readdirSync(sourceDir).filter((name) => name.endsWith(".md"));
if (templates.length === 0) {
	// Staging an empty directory would package successfully and fail at runtime,
	// which is exactly the failure mode this script exists to prevent.
	console.error(`No .md role templates in ${sourceDir}; refusing to stage an empty profiles resource.`);
	process.exit(1);
}

rmSync(outDir, { recursive: true, force: true });
mkdirSync(outDir, { recursive: true });
cpSync(sourceDir, outDir, { recursive: true });

console.log(`Staged ${templates.length} role template(s) into ${outDir}`);
