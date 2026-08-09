# Upstream Sync 3 execution ledger

**Status:** Phase 0 complete at merge commit
`c7889415fe0b757a2158ebf830771fa33fd8dc08`; Phase 1 lanes are in progress.

**Fork source:** `roles/multi-sub-v1` at
`37f2db5471d667e225d131babe6555f031229684`.

**Previously accepted upstream baseline:**
`fa799a7a58e2f9ec13d174567aff436ba890ff6a`.

**Immutable Sync 3 pin:**
`6e9dbb1051b0d4dc2b67f6525a0be1aec0c8d445`.

**Integration branch:** `codex/upstream-sync3-integration`.

**Integration merge:** `c7889415fe0b757a2158ebf830771fa33fd8dc08`
with parents `ecf45bccd015575be5d90ee3241adf746f42826f` and the immutable
upstream pin `6e9dbb1051b0d4dc2b67f6525a0be1aec0c8d445`.

The source checkout was clean except for the untracked, user-supplied
`UPSTREAM_SYNC3_PLAN.md`. The plan was committed unchanged as `ac7ef32` before
the merge so it remains an auditable input to the execution.

Both remotes were fetched with pruning on 2026-08-09. The canonical pin
contains `01cb67fe11734d725b1a84deb130fe58b40203d6` (PR #3548). The `upstream`
push URL is locally disabled while its fetch URL remains canonical.

Ancestry was verified directly: the fork tip, upstream pin, prior upstream
baseline, and PR #3548 commit are all ancestors of `c7889415`. The merge base
of the fork tip and upstream pin is exactly
`fa799a7a58e2f9ec13d174567aff436ba890ff6a`; the exclusive upstream range has
54 commits. `ac7ef32aa30a924417fd17283cc095d6bf193b7e` records the immutable
plan and `ecf45bccd015575be5d90ee3241adf746f42826f` records the pre-merge
baseline.

## Full-range commit inventory

The range contains 54 commits. “Auto-merged” below means the commit did not
touch one of the 49 textual-conflict paths; it does not waive later behavioral
acceptance. “Semantic union” means the conflict was resolved in `c7889415` by
retaining the fork boundary and adopting the upstream feature. Exact per-file
decisions follow in the next section.

| SHA | Exact upstream subject / PR | Conflict classification | Phase 0 resolution status |
|---|---|---|---|
| `c42ba4d4a` | fix(frontend): only confirm busy interface switches (#3671) | renderer session-view semantic union | resolved in `c7889415` |
| `7ecf58970` | chore: add bug triage skills (#3604) | auto-merged | merged in `c7889415` |
| `a817939fa` | Merge the inspector's two review sections into one list (#3664) | auto-merged | merged in `c7889415` |
| `3f7b5288e` | fix(scm): condition commit check guard on all check runs, not just one (#3619) | auto-merged | merged in `c7889415` |
| `09a87eb17` | fix(muse): resume native sessions deterministically (#3677) | auto-merged | merged in `c7889415` |
| `53ccd9340` | feat(agent): add muse model catalog (#3675) | auto-merged | merged in `c7889415` |
| `f17013b53` | fix(frontend): remove extra terminal edge spacing (#3609) | auto-merged | merged in `c7889415` |
| `fb0f85fa4` | feat(landing): record marketing analytics, with replay scoped to the design-partner page (#3489) | auto-merged | merged in `c7889415` |
| `8055c9ee0` | fix(harness): detect Muse awaiting-input activity state (#3683) | agent-port capability union | resolved in `c7889415` |
| `9c475bf71` | feat(new-task): redesign the composer with resolved agent and model defaults (#3657) | daemon/API/service/renderer policy union | resolved in `c7889415` |
| `b168a4e21` | fix: make jump-to-latest button opaque (#3680) | ChatWorkspace semantic union | resolved in `c7889415` |
| `bad160129` | feat(new-task): combine continuous composer and attachments (#3691) | API/generated/service/composer union | resolved; API artifacts regenerated |
| `fc5f4ca02` | fix: keep current page when opening Settings modal (#3694) | auto-merged | merged in `c7889415` |
| `4909d16f2` | fix(frontend): restore rounded center-panel corners on Windows/Linux (#3672) | auto-merged | merged in `c7889415` |
| `d15fd8277` | fix: align reviewer terminal lifecycle (#3484) | provider/daemon/API/storage/session-view union | resolved; generated artifacts regenerated |
| `53197448f` | fix: restore model menu scrolling (#3701) | auto-merged | merged in `c7889415` |
| `49e85355a` | fix: open board from project name without double-click (#3696) | auto-merged | merged in `c7889415` |
| `e43989f4f` | fix: keep sidebar Projects section always expanded (#3697) | auto-merged | merged in `c7889415` |
| `72811b755` | feat(review): add interactive reviewers for all agent harnesses (#3384) | review/API/storage/project-settings union | resolved; fork trust policy retained |
| `f7e0fe65b` | fix(frontend): polish sidebar and settings presentation (#3706) | auto-merged | merged in `c7889415` |
| `1ec838b8a` | docs: move README translations into translations/ (#3728) | auto-merged | merged in `c7889415` |
| `2904ef7e8` | fix(frontend): inset contained activity rows (#3726) | auto-merged | merged in `c7889415` |
| `76a03ca0e` | fix(frontend): route unsupported chat agents to TUI (#3715) | TaskComposer semantic union | resolved in `c7889415` |
| `31d920b5a` | fix(chat): render image attachments in messages (#3724) | ChatWorkspace semantic union | resolved in `c7889415` |
| `67a02b44a` | fix(frontend): sync terminal colors with selected theme (#3723) | auto-merged | merged in `c7889415` |
| `e00123f6a` | feat: auto-grow chat composer (#3721) | auto-merged | merged in `c7889415` |
| `237f6a6ab` | fix(frontend): prevent chat pane horizontal overflow (#3725) | SessionView semantic union | resolved in `c7889415` |
| `ce3b24a99` | fix(frontend): scope chat font controls to conversation (#3730) | ChatWorkspace semantic union | resolved in `c7889415` |
| `b264e7765` | fix(chat): honor Codex configured effort default (#3727) | auto-merged | merged in `c7889415` |
| `eff05a0e3` | fix(session): prevent stale-idle interface drain hangs (#3719) | interface-transition/manager union | resolved in `c7889415` |
| `a3fbf8850` | fix: recover Chat sessions after controller stops (#3711) | lifecycle/chat/manager union | resolved in `c7889415` |
| `586d863bb` | fix(frontend): enable chat links and text selection (#3717) | ChatWorkspace semantic union | resolved in `c7889415` |
| `66821b3f5` | Open first browser content automatically and badge background activity (#3359) | session-view/browser union | resolved in `c7889415` |
| `cf3b9f41e` | fix: stop the session Files diff viewer from freezing on large diffs (#3666) | CLI/API/service/package union | resolved in `c7889415` |
| `61f481d61` | docs: remove obsolete PR screenshots (#3731) | auto-merged | merged in `c7889415` |
| `c9e1c676b` | Add session owned browser automation(Agent-browser), DevTools, and stable preview controls (#3473) | daemon/domain/storage/desktop/distribution union | resolved; browser resource added alongside fork resources |
| `6c72814f9` | feat: add Prime Agent harness (#3736) | CLI/API/generated/storage union | resolved; generated artifacts regenerated |
| `ec23eaf4f` | fix(browser): preserve cancel frames after delivered writes (#3733) | auto-merged | merged in `c7889415` |
| `b6609ae61` | fix: gate review rows until verdict (#3742) | auto-merged | merged in `c7889415` |
| `fd3f86f77` | feat(mobile): PostHog product analytics for the mobile app (#3661) | auto-merged | merged in `c7889415` |
| `22c121720` | feat(browser): use native composition and docked devtools (#3750) | Electron main-process union | resolved in `c7889415` |
| `6f2eb5dea` | feat(telemetry): classify every surface with a uniform client tag (#3663) | auto-merged | merged in `c7889415` |
| `67199afa8` | feat(mobile): per-name rate cap on mobile telemetry events (#3704) | auto-merged | merged in `c7889415` |
| `0c98a7a8a` | feat: add Kimchi agent harness with full feature parity (#2649) | provider/CLI/API/storage/settings union | resolved; generated artifacts regenerated |
| `d293ea81f` | fix: recover Codex interface switch readiness (#3735) | Codex/interface-transition test union | resolved in `c7889415` |
| `f65c48e29` | feat: add review feedback injection toggle (#3709) | API/session/storage/generated union | resolved; generated artifacts regenerated |
| `6a7cdd231` | fix(session): resolve PR fallback compare base to merge-base (#3688) | service-test semantic union | resolved in `c7889415` |
| `9584c754d` | fix(mobile): add iOS photo library purpose string (#3545) | auto-merged | merged in `c7889415` |
| `047ff07e7` | feat(browser-annotations): attach a snapshot and tighten the prompt (#3625) | daemon/API/service/manager/generated union | resolved; generated artifacts regenerated |
| `7b76a727b` | feat(landing): add mobile TestFlight download flow (#3689) | auto-merged | merged in `c7889415` |
| `84f0c26c8` | fix(browser): remove redundant DevTools dock control (#3781) | auto-merged | merged in `c7889415` |
| `1b63debca` | Polish the New Task composer and settings modals (#3705) | project-settings/dialog/composer union | resolved; Roles retained and Developer removed |
| `01cb67fe1` | feat: add durable agent switching (#3548) | broad switch-engine/API/storage/terminal union | resolved for Phase 0 compilation; convergence remains Phase 3 work |
| `6e9dbb105` | fix(kimi): constrain and stop reviewer sessions (#3782) | auto-merged | merged verbatim; `--plan --auto` and double interrupt retained |

## Pre-merge conflict inventory

`git merge-tree --write-tree ecf45bccd 6e9dbb105` reports exactly 49 textual
conflicts. The table is exhaustive. “Union” means both intended behaviors were
retained; it is not a claim that the later full behavioral gate has passed.
Generated OpenAPI, TypeScript API schema, and sqlc outputs were resolved from
their sources and regenerated rather than choosing a stage blob.

| # | Conflicted file | Semantic resolution category | Phase 0 resolution |
|---:|---|---|---|
| 1 | `backend/internal/adapters/agent/claudecode/claudecode.go` | provider continuity + fork config/authorization | union resolved |
| 2 | `backend/internal/adapters/agent/claudecode/claudecode_test.go` | provider behavior test union | both test families retained |
| 3 | `backend/internal/adapters/agent/codex/codex.go` | provider continuity + fork config/hooks | union resolved |
| 4 | `backend/internal/adapters/agent/codex/codex_test.go` | provider behavior test union | both test families retained |
| 5 | `backend/internal/cli/dto_drift_e2e_test.go` | thin-client DTO/API compatibility | legacy and upstream route shapes retained |
| 6 | `backend/internal/cli/session.go` | thin-client command surface | fork session commands and upstream switch support unioned |
| 7 | `backend/internal/cli/spawn.go` | role-aware spawn + upstream harness/attachment fields | union resolved; CLI remains HTTP-only |
| 8 | `backend/internal/daemon/daemon.go` | daemon authority + browser/reviewer lifecycle | union resolved; no alternate persistence/runtime path |
| 9 | `backend/internal/daemon/lifecycle_wiring.go` | shared manager wiring | union resolved by integration owner |
| 10 | `backend/internal/domain/session.go` | durable role/pause/legacy-switch facts + upstream session fields | union resolved |
| 11 | `backend/internal/httpd/apispec/openapi.yaml` | generated API artifact | regenerated by `npm run api` |
| 12 | `backend/internal/httpd/controllers/dto.go` | API source DTO union | fork role/policy and upstream review/switch/browser fields retained |
| 13 | `backend/internal/httpd/controllers/sessions.go` | controller route/validation union | legacy and durable-switch endpoints retained |
| 14 | `backend/internal/httpd/controllers/sessions_test.go` | controller behavior test union | conflict resolved; full suite not yet claimed |
| 15 | `backend/internal/ports/agent.go` | provider capability contract | fork activity/config facts and upstream continuation capabilities retained |
| 16 | `backend/internal/service/session/delegation.go` | strict role delegation + upstream task attachments/defaults | union resolved |
| 17 | `backend/internal/service/session/delegation_test.go` | delegation policy test union | both test families retained |
| 18 | `backend/internal/service/session/service.go` | session policy + upstream service surface | union resolved |
| 19 | `backend/internal/service/session/service_test.go` | service policy/API-error tests | conflict resolved; the Phase 1 baseline is not yet green |
| 20 | `backend/internal/session_manager/chat_spawn.go` | pause/role authority + upstream chat lifecycle | union resolved |
| 21 | `backend/internal/session_manager/interface_transition.go` | switch/interface mutual exclusion + upstream transition fixes | union resolved |
| 22 | `backend/internal/session_manager/interface_transition_test.go` | interface-transition regression union | both test families retained |
| 23 | `backend/internal/session_manager/manager.go` | shared manager wiring and error surface | resolved by integration owner; later convergence still pending |
| 24 | `backend/internal/session_manager/manager_test.go` | shared test fixtures + upstream engine coverage | conflict resolved; full suite not yet green |
| 25 | `backend/internal/session_manager/provision_test.go` | role-aware provision + browser/native metadata | test union resolved |
| 26 | `backend/internal/sessionguard/guard.go` | pause/legacy/durable-switch input fences | union resolved |
| 27 | `backend/internal/storage/sqlite/db.go` | migration repair ordering + upstream migrations | semantic union; `0085` preserved verbatim |
| 28 | `backend/internal/storage/sqlite/gen/models.go` | generated sqlc model artifact | regenerated by `npm run sqlc` |
| 29 | `backend/internal/storage/sqlite/gen/sessions.sql.go` | generated sqlc query artifact | regenerated by `npm run sqlc` |
| 30 | `backend/internal/storage/sqlite/migrate_burned_versions_test.go` | fork `900x` and upstream burned-version repair | semantic test union, never stage-selected wholesale |
| 31 | `backend/internal/storage/sqlite/queries/sessions.sql` | source SQL union | role/pause fields and upstream review/browser/switch fields retained |
| 32 | `backend/internal/storage/sqlite/store/session_store.go` | durable session serialization union | role/pause/legacy pending and upstream fields retained |
| 33 | `backend/internal/storage/sqlite/store/store_test.go` | storage fixture/schema union | conflict resolved; database gates remain pending |
| 34 | `backend/internal/telemetrymeta/cli.go` | telemetry metadata + expanded client surface | union resolved |
| 35 | `backend/internal/terminal/manager.go` | terminal ownership/attachments + switch input behavior | union resolved |
| 36 | `backend/internal/terminal/manager_test.go` | terminal behavior test union | both test families retained |
| 37 | `frontend/forge.config.ts` | desktop distribution resources | fork daemon/profiles/ACP plus upstream `agent-browser` retained |
| 38 | `frontend/package.json` | build lifecycle/dependency union | profile staging and browser-runtime preparation retained; `npm ci` accepted lock |
| 39 | `frontend/src/api/schema.ts` | generated TypeScript API artifact | regenerated by `npm run api` |
| 40 | `frontend/src/main.ts` | Electron native composition + daemon/AO-data authority | union resolved; browser secrets remain private-main-process state |
| 41 | `frontend/src/renderer/components/ProjectSettingsForm.test.tsx` | role-map CAS/degraded tests + reviewer/save-state tests | test union resolved |
| 42 | `frontend/src/renderer/components/ProjectSettingsForm.tsx` | Roles editor/CAS + upstream settings/reviewer UX | union resolved; orphan footer removed |
| 43 | `frontend/src/renderer/components/SessionView.test.tsx` | orchestrator switch/pause tests + reviewer-terminal tests | test union resolved |
| 44 | `frontend/src/renderer/components/SessionView.tsx` | orchestrator switch + reviewer-terminal state | union resolved; orchestrators excluded from worker engine |
| 45 | `frontend/src/renderer/components/SettingsDialog.tsx` | upstream sidebar save + fork Roles navigation | union resolved; obsolete Developer section removed |
| 46 | `frontend/src/renderer/components/TaskComposer.tsx` | strict role-aware delegation + upstream composer/attachments | union resolved; strict payload omits free-form agent/model |
| 47 | `frontend/src/renderer/components/chat/ChatWorkspace.test.tsx` | pause/input fence + selectable text/link routing | test union resolved |
| 48 | `frontend/src/renderer/components/chat/ChatWorkspace.tsx` | input fence + upstream link provider | union resolved |
| 49 | `frontend/src/shared/daemon-discovery.ts` | runfile identity/address + operator token privacy | union resolved; removed browser token not restored |

## Test inventory

Six files are the authoritative pre-merge test-name inventories. The backend
and runtime inventories record every `go test -json` `run` event, including
subtests. The renderer inventories are Vitest 4 `--list` JSON. They were
captured from isolated worktrees at the exact fork and upstream refs.

| Surface / ref | Authoritative inventory | Count | SHA-256 | Collection result |
|---|---|---:|---|---|
| backend fork `37f2db547` | `/private/tmp/ao-sync3-019fe7a8/backend/inventories/fork-backend-lane-tests.json` | 1,517 test/subtest paths across 15 packages | `0569197534e5056fd40cdf6d193269485b14543776bca6d5eb3588b4c150f234` | all 15 packages passed |
| backend upstream `6e9dbb105` | `/private/tmp/ao-sync3-019fe7a8/backend/inventories/upstream-backend-lane-tests.json` | 1,346 test/subtest paths across 15 packages | `b5bf933ffec56be27f772a308ef3bb8317ef2c06a86243c18ac3f434391ab7ad` | all 15 packages passed |
| runtime fork `37f2db547` | `/private/tmp/ao-sync3-019fe7a8/runtime/inventories/fork/all-run-events.sorted.tsv` | 1,718 test/subtest paths across 26 surveyed package paths | `2d3eb99ab317ff4a970fe66ef31c609ff31d9e8ca028b6198a5c392e7fd3b9a8` | every package present on the fork passed; four upstream-only package paths were absent |
| runtime upstream `6e9dbb105` | `/private/tmp/ao-sync3-019fe7a8/runtime/inventories/upstream/all-run-events.sorted.tsv` | 1,593 test/subtest paths across 26 package paths | `c716a2aa4f61d62746399e4dd9089dd3e035974ab7946d75cf099aa89f8f7076` | all 26 package paths passed |
| renderer fork `37f2db547` | `/private/tmp/ao-sync3-019fe7a8/frontend/inventories/fork-renderer-vitest4.json` | 2,060 tests in 153 files | `a5e1087ae2070bedf50fa45e4f564abde5fc0c28a2df6b6b3bc955205350df26` | Vitest 4 list succeeded |
| renderer upstream `6e9dbb105` | `/private/tmp/ao-sync3-019fe7a8/frontend/inventories/upstream-renderer-vitest4.json` | 2,106 tests in 163 files | `a9225c10906bb62cbb870bef069b62f71628d75ca0caac85aac760657866c3c6` | Vitest 4 list succeeded after installing the isolated frontend and landing dependencies |

The backend raw streams remain beside the parsed inventories as
`fork-backend-lane-go-test.jsonl` and `upstream-backend-lane-go-test.jsonl`.
The runtime comparison contains 1,102 shared paths, 616 fork-only paths, 491
upstream-only paths, and a 2,209-path reviewed-union candidate. The renderer
comparison contains 1,872 common names, 188 fork-only names, and 234
upstream-only names.

Upstream intentionally deletes `detect-urls.test.ts` with the browser-owned
content change and `ui-store.test.ts` with the settings/UI-store rewrite. Both
retirements require named replacement coverage before the union is accepted.

No newly added focused or unconditional skip marker was found. Nine new Go
conditional skips are platform/environment guards and remain ledgered for
post-merge review. The inventories were captured on Darwin; ConPTY behavior
still requires Windows CI.

## Reviewer and desktop conflict decisions

- Kimi reviewer invocation remains exactly `kimi --plan --auto`; task launches
  add a fresh empty `--skills-dir`.
- Kimi cancellation remains exactly two interrupt sequences, and its adapter
  and registry-wide cancellation tests are both retained.
- Upstream's experimental reviewer catalog does not itself grant fork
  authorization. Reviewer trust/capability policy remains separately
  authoritative and unknown-auth reviewers are not enabled accidentally.
- The worker switch UI remains unavailable until the engine and policy gates
  pass. Its eventual targets come from authorized role-map harness/model pairs,
  never the upstream hard-coded set.
- Orchestrator switching remains outside the worker engine and hidden in Chat.
- Browser runtime and native-window composition must retain daemon authority,
  the opt-in authenticated LAN listener, updater behavior, and all state below
  `~/.ao`.
- Forge/package conflicts preserve the fork daemon and ACP resources while
  adding the pinned agent-browser runtime. `npm ci` accepted the merged lock;
  a post-integration zero-diff lock audit remains pending.

## Migration ledger

- The upstream pin contains `0085_agent_switching.sql` and no fork `900x`
  migration.
- The upstream migration blob must remain byte-for-byte unchanged.
- The pinned `0085` blob is
  `8582b8b9b9e4b0398487fbbca19f91f016016957`; its SHA-256 is
  `b3871aaf81c982886f3385f276aed543abdce029a62057f1a6708a3b5c643bc3`.
- The file in integration merge `c7889415` retains those exact Git-blob and
  SHA-256 identities.
- Fresh-database, copied-fork-through-`9008`, and second-boot idempotency tests
  are mandatory.
- `migrate_burned_versions_test.go` is a semantic merge conflict and may not be
  resolved wholesale.
- Fork migration-ledger repair must run before upstream chat and agent-switch
  renumber repair, followed by burned-schema and review/browser compatibility
  preparation, `goose.Up` with allow-missing, and post-migration
  reconciliation.
- Any fork-specific switch extension uses a new `9009+` migration; `0085` and
  the already-merged `9008` are immutable.

## Phase 0 gate evidence

The following commands/results were actually observed on integration commit
`c7889415`; they are deliberately narrower than the final acceptance gate.

| Evidence | Result | Scope / caveat |
|---|---|---|
| `git diff --check --cached` before the merge commit | passed | whitespace/conflict-resolution hygiene only |
| `cd backend && go test ./... -run '^$'` | passed for every backend package | compile-only; it did not execute tests |
| `npm run sqlc` | succeeded | regenerated sqlc outputs during conflict resolution; a later zero-diff rerun was not performed |
| `npm run api` | succeeded | regenerated OpenAPI and `frontend/src/api/schema.ts`; a later zero-diff rerun was not performed |
| `npm --prefix frontend ci` | succeeded | lock/install coherence; npm reported 36 existing vulnerabilities and no audit fix was run |
| `npm run frontend:typecheck` | succeeded | frontend TypeScript check on the merged sources |

Phase 0 did **not** run the full backend test suite, full renderer tests, final
generator zero-diff checks, frontend production build/package, migration
matrix, browser end-to-end tests, Windows ConPTY CI, real-desktop validation,
or signed artifact verification. Those remain later gates.

## Phase 1 policy characterization evidence

The isolated policy lane added test-only commit
`a93714b4aed6f239557841b85b5ff87335a2668b` (`test: characterize switch
policy boundaries`) on `codex/upstream-sync3-policy`. It adds:

- `backend/internal/domain/policy_characterization_test.go`;
- `backend/internal/service/session/policy_characterization_test.go`;
- `backend/internal/session_manager/policy_characterization_test.go`.

The tests freeze exact role/model authorization before manager mutation,
pause and interface-transition boundaries, `ActiveFailoverAttempt` post-stop
adoption without spending another rung, legacy `SwitchPending` recovery
ownership, automatic failover, and worker/orchestrator route separation across
the fork and upstream durable entry points. No production file changed in that
commit.

Executed evidence:

| Command/scope | Result |
|---|---|
| `go test ./internal/domain ./internal/service/session ./internal/session_manager -run '^TestPolicyCharacterization_' -count=1` | 10 passed across 3 packages |
| focused authorization, pause, interface-transition, legacy recovery, ownership and routing selection | 40 passed: domain 12, service 11, session manager 17 |
| selected pause/failover characterization set | 5 passed, 2 failed |
| `go test ./internal/domain ./internal/service/session ./internal/session_manager` | 1,278 passed, 17 failed test/subtest events; this was a three-package baseline, not a full-backend run |

The two selected failover failures are
`TestContinueAutomaticFailover_DuplicateAfterPreStopFailureDoesNotSpendSecondRung`
and `TestReconcile_AutomaticTerminalFailureNeverAdvancesAnotherRung`. Both
fixtures expected a confirmed-alive pre-stop rollback, while the merged
runtime/test-fixture behavior completed and acknowledged the switch. Other
three-package baseline failures cluster in shared chat cleanup, dogfood
rollback, orchestrator reap, pause re-nudge, legacy orchestrator rollback,
switch rollback error identity, and the service mapping for
`manager_switch_in_progress`. They are unresolved integration evidence, not
failures introduced by `a93714b4`.

## Unresolved acceptance items

- Run `npm run sqlc` and `npm run api` again after all Phase 1-3 integration
  and require zero diff.
- Run the fresh database, copied fork-through-`9008`, second-boot idempotency,
  burned-version, and migration reconciliation matrix.
- Resolve the 17 observed domain/service/session-manager baseline failures and
  run the complete backend test suite, including race coverage where required.
- Produce the post-merge test-name inventory and reconcile it against the
  2,209-path runtime and renderer reviewed unions.
- Name and verify replacement coverage for the intentionally retired
  `detect-urls.test.ts` and `ui-store.test.ts` suites.
- Run full renderer tests, frontend build/package, browser/Electron end-to-end,
  real-desktop isolated-`AO_DATA_DIR`, Windows ConPTY, and macOS signed ZIP/DMG
  artifact verification.
- Keep the worker switch UI unavailable until policy/engine convergence and
  the later acceptance gates are complete.
