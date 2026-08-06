# Phase 2B-1 — live dogfood evidence

**Gate, not polish.** 2B-1 shipped with a real-store `CHECK` failure that made the
feature impossible to run while the entire unit suite was green (see
`b3ce92a6`). Two further defects — an unreachable service gate and a corrupted
role pin — were found only by running it. Unit tests cannot close this class,
because every switch fixture seeds a role-mapped project and the in-memory store
accepts any ledger kind. **This evidence must be refreshed whenever the switch
saga, the ledger schema, or the role-pin path changes.**

Run: 2026-08-06, isolated dev data (`~/.ao/dev/data`), daemon `127.0.0.1:3002`,
product API with operator credential (the same path the desktop UI uses).

Project `agent-orchestrator-roles`, **strict delegation**, `orchestratorRole:
orchestrator` bound to **codex** (`workspaceWrites:false`, `canSpawn:true`) —
i.e. the only orchestrator binding strict delegation permits, since
`workspaceWrites:false` requires `read_only_enforced` and only Codex has it.

## 1. Migration 0048 on the upgraded dev database

Upgraded in place from an existing populated database, not a fresh one.

```
goose version = 48
ledger CHECK admits orchestrator_fresh_conversation: YES
orchestrator_replacement_intent (0047): present
```

## 2. Codex orchestrator fresh conversation through the product API

`POST /api/v1/sessions/agent-orchestrator-roles-2/fresh-conversation` → **200**

```json
{"ok": true,
 "sessionId": "agent-orchestrator-roles-2",
 "generationId": "7bd9dfc6-7fd5-4bf2-aba1-bc2d88740f25",
 "kind": "orchestrator_fresh_conversation",
 "session": {"kind": "orchestrator", "harness": "codex"}}
```

Run three times in total (two consecutive, then one with a worker present); all
returned 200.

## 3. Identity stable, credentials rotate

| Field | Before | After | Required |
|---|---|---|---|
| session id | `agent-orchestrator-roles-2` | `agent-orchestrator-roles-2` | **stable** |
| `role_id` | `orchestrator` | `orchestrator` | **stable** |
| `template_artifact_id` | `sha256:a09f7d18…` | `sha256:a09f7d18…` | **stable** |
| branch | `ao/dev/agent-orches-orchestrator` | unchanged | **stable** |
| workspace | `…/orchestrator/agent-orches-orchestrator` | unchanged | **stable** |
| `runtime_launch_id` | `6998939a…` | `7bd9dfc6…` | **rotated** |
| `spawn_capability_hash` | `b40ea1f9…` | `41140892…` | **rotated** |

The new `runtime_launch_id` equals the response `generationId`, so the ledger
generation and the runtime generation agree. Credential rotation matters
because a killed session's prior token must not survive the relaunch.

## 4. Ledger records the lifecycle in exact order

```
orchestrator_fresh_conversation / requested  / 7bd9dfc6
orchestrator_fresh_conversation / pre_stop   / 7bd9dfc6
orchestrator_fresh_conversation / post_stop  / 7bd9dfc6
orchestrator_fresh_conversation / target_ack / 7bd9dfc6
orchestrator_fresh_conversation / requested  / 93a8efc0
orchestrator_fresh_conversation / pre_stop   / 93a8efc0
orchestrator_fresh_conversation / post_stop  / 93a8efc0
orchestrator_fresh_conversation / target_ack / 93a8efc0
```

One generation per cycle, no interleaving, **no `failed` phases**.

## 5. Handoff appears once, carries the roster, does not stack

After two consecutive fresh conversations the stored prompt contains:

```
handoff sections: 1
fleet sections:   1
prompt bytes:     1529
```

Stacking would have been the natural failure — each refresh injects a compiled
handoff into a prompt that already contains one. It does not accumulate.

With a live worker present, the roster is host-derived from the session table:

```
### Observed fleet (host; authoritative over any recollection of workers)
- Project: `agent-orchestrator-roles`
- Workers: 1 live, 0 terminated
  - `agent-orchestrator-roles-3` — idle, role `implementor`, harness `claude-code`,
    branch `ao/dev/agent-orchestrator-roles-3/root`
```

## 6. Input fenced until target acknowledgment

Probed concurrently with the saga, catching the durable pending window:

```
fence window observed
POST /sessions/{id}/send  ->  409  SWITCH_IN_PROGRESS
                              "A worker switch is already in progress for this session"
```

That message is quoted as observed. Being told a **worker** switch was in
progress while refreshing an *orchestrator* is exactly the worker-centric
wording review flagged: orchestrator fresh reuses the same saga and fences, so
`SWITCH_IN_PROGRESS`, `SWITCH_NOT_SUPPORTED` and `NOT_A_WORKER` are now
kind-neutral. The same probe today reads "A switch or fresh conversation is
already in progress for this session"; the code and fence behaviour are
unchanged.

After `target_ack` the same call is no longer fenced — it reaches ordinary
message validation (`400 MESSAGE_REQUIRED` for an empty body), which is the
distinguishing evidence that the gate lifted rather than the endpoint being
broken.

## 7. Restart / recovery

Full daemon restart after the runs:

```
ERROR lines on boot:            0
active orchestrators:           1
sessions with pending switch:   0
replacement intents:            0
reap queue:                     0
unexplained ledger failures:    0
```

## Defects this dogfood found

| # | Defect | Fix |
|---|---|---|
| 1 | Service required a role pin + role map for **any** switch, making fresh conversation unreachable for un-pinned sessions — including every orchestrator on a non-strict project | Checks moved to the cross-harness path, where the map actually authorizes a target |
| 2 | `relaunchSession` stamped an ephemeral `ResolvedHarness` onto an **empty** role binding, manufacturing a partial pin that restore correctly refused — *after* the source had stopped, so it failed identically on every boot | Stamp only applies when a pin exists |
| 3 | `EnsureOrchestrator`'s non-`clean` path never discharged the replacement intent, so a project rescued by an ordinary spawn kept a durable record claiming it was still owed one | Both non-clean branches discharge |
| 4 | Desktop bundles shipped **no role templates at all**, so a clean install could not launch a strict role-pinned orchestrator | Profiles staged and shipped as an `extraResource`; `AO_ROLE_PROFILES_DIR` passed explicitly. Re-dogfooded from an empty data dir |
| 4b | The staging fix ran from `prepackage`, which **no release workflow invokes** — CI builds with `make`/`publish`, so bundles stayed template-less | Staged from Forge's shared `prePackage` hook; tests pin the real entrypoints |
| 5 | The five role-resolution sentinels were unmapped, so configuration errors surfaced as **opaque 500s** | Stable actionable 400s with wrapped-error coverage |

### Follow-ups from review — both closed

**P1 — profiles were not shipped at all.** Recorded here first as a
"dev-launch path issue", which understated it: `forge.config.ts`
`extraResource` did not include `profiles`, so a **clean desktop install
shipped no role templates**, and `profileRoots` only searches
`AO_ROLE_PROFILES_DIR`, `cwd/profiles` and data-dir-relative paths — none of
which exist for a packaged daemon running with cwd `~/.ao`. A fresh install
therefore could not launch a strict role-pinned orchestrator at all.

Fixed by staging `profiles/` into the bundle (`scripts/stage-profiles.mjs`,
failing closed on a missing or empty source), shipping it via `extraResource`,
and passing `AO_ROLE_PROFILES_DIR` explicitly on every platform in both dev and
packaged launches (`resolveRoleProfilesDir`).

**The first fix wired staging to the wrong entrypoint** — caught by review. npm
runs only the pre-hook matching the script you invoked, so `prepackage` fires
for `npm run package` alone, and **no workflow uses it**: releases publish with
`npm run publish` (`frontend-release.yml`, `feature-release.yml`) and build
artifacts with `npm run make` (`build-artifacts.yml`, `desktop-testing.yml`,
`testing-build.yml`). A left-over, gitignored `frontend/profiles` hid this on
developer machines; a clean CI checkout has none, so every release path would
still have shipped a template-less bundle. Staging now runs from Forge's
`prePackage` hook — the one step `package`, `make` and `publish` all share.

Proven through the real CI entrypoint, starting from a **deleted**
`frontend/profiles`:

```
$ rm -rf frontend/profiles && npm run make -- --targets @electron-forge/maker-zip
  Running prePackage hook from forgeConfig
  Staged 4 role template(s) into …/frontend/profiles
  Making a zip distributable for darwin/arm64

out/make/zip/darwin/arm64/Agent Orchestrator-darwin-arm64-0.10.3.zip
  Agent Orchestrator.app/Contents/Resources/profiles/orchestrator.md    1464
  Agent Orchestrator.app/Contents/Resources/profiles/reviewer.md         802
  Agent Orchestrator.app/Contents/Resources/profiles/implementor.md     1171
  Agent Orchestrator.app/Contents/Resources/profiles/ui-implementor.md   884
```

`diff -r` against the repository `profiles/` reports the bundled copies
identical. Still a **non-claim**: no signed, installed `.app` was launched — the
packaged *runtime* resolution (`resourcesPath/profiles`) is covered by unit test
and by construction, not by an installed-app run.

**Clean-install re-dogfood**, with `~/.ao/dev` moved aside and nothing copied by
hand:

```
daemon env: AO_ROLE_PROFILES_DIR=<repo>/profiles
profiles under the data dir: NONE
POST /projects                     -> 201
PUT  /projects/{id}/config (strict) -> 200
POST /orchestrators                -> 201  codex, role_id=orchestrator,
                                            template_artifact_id=sha256:a09f7d18…,
                                            workspaceWrites=0, canSpawn=1
POST /sessions/{id}/fresh-conversation -> 200 orchestrator_fresh_conversation
ledger: requested -> pre_stop -> post_stop -> target_ack
```

The `template_artifact_id` is byte-identical to the earlier run, so the pin
resolved from the shipped templates rather than from anything left behind.

**P2 — the role-error family is mapped.** All five sentinels now return stable
actionable 400s instead of opaque 500s, with wrapped-error coverage (the service
sees `spawn mer-1: spawn: <sentinel>`, so testing bare sentinels would have
passed while the real path still 500'd):

| Sentinel | Code |
|---|---|
| `ErrRoleRequired` | `ROLE_REQUIRED` |
| `ErrRoleUnknown` | `ROLE_UNKNOWN` |
| `ErrHarnessOverrideForbidden` | `HARNESS_OVERRIDE_FORBIDDEN` |
| `ErrRolePromptRequired` | `ROLE_TEMPLATE_UNAVAILABLE` |
| `ErrReadOnlyUnsupported` | `READ_ONLY_UNSUPPORTED` |

Live, on the clean install:

```
POST /sessions roleId=nonexistent-role -> 400 ROLE_UNKNOWN
POST /sessions (strict, no roleId)     -> 400 ROLE_REQUIRED
```

No extra server logging was added: these are expected configuration errors, and
the response now carries the diagnosis.

**D2a was also observed live and unplanned** during setup: a spawn failure landed
*after* the predecessor was retired, leaving zero orchestrators, and 2B-2's
replacement intent was correctly present, retained, and later discharged.
