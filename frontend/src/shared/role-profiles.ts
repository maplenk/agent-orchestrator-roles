function joinPath(...segments: string[]): string {
	return segments.map((segment) => segment.replace(/[/\\]+$/, "")).join("/");
}

/**
 * Where the daemon should look for shipped role templates.
 *
 * The daemon's own `profileRoots` fallback is `cwd/profiles` plus a few
 * data-dir-relative paths. Neither works for a real installation: the packaged
 * daemon runs with cwd `~/.ao`, and a clean install has nothing under the data
 * dir — so a strict, role-pinned orchestrator could not launch at all until
 * someone copied `profiles/` in by hand. Passing the root explicitly is what
 * makes the shipped templates actually reachable.
 *
 * Resolution order:
 *  - an explicit `AO_ROLE_PROFILES_DIR` always wins, so an operator can point at
 *    their own approved templates;
 *  - packaged: the bundled resource staged by `scripts/stage-profiles.mjs` and
 *    listed in `forge.config.ts` `extraResource`;
 *  - dev: the repository's `profiles/`, deliberately the source tree rather than
 *    the staged copy, so editing a template takes effect without repackaging.
 */
export function resolveRoleProfilesDir(
	env: Record<string, string | undefined>,
	isPackaged: boolean,
	resourcesPath: string,
	appPath: string,
): string {
	const configured = env.AO_ROLE_PROFILES_DIR?.trim();
	if (configured) return configured;
	if (isPackaged) return joinPath(resourcesPath, "profiles");
	return joinPath(appPath, "..", "profiles");
}
