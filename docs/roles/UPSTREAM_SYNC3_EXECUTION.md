# Upstream Sync 3 execution ledger

**Status:** Phase 0 surveys complete; exact-pin merge pending.

**Fork source:** `roles/multi-sub-v1` at
`37f2db5471d667e225d131babe6555f031229684`.

**Previously accepted upstream baseline:**
`fa799a7a58e2f9ec13d174567aff436ba890ff6a`.

**Immutable Sync 3 pin:**
`6e9dbb1051b0d4dc2b67f6525a0be1aec0c8d445`.

**Integration branch:** `codex/upstream-sync3-integration`.

The source checkout was clean except for the untracked, user-supplied
`UPSTREAM_SYNC3_PLAN.md`. The plan was committed unchanged as `ac7ef32` before
the merge so it remains an auditable input to the execution.

Both remotes were fetched with pruning on 2026-08-09. The canonical pin
contains `01cb67fe11734d725b1a84deb130fe58b40203d6` (PR #3548). The `upstream`
push URL is locally disabled while its fetch URL remains canonical.

## Full-range commit inventory

The range contains 54 commits. The lane is the primary review owner, not a
claim that the commit has no cross-lane effects. Resolution and validation are
filled as the merge gates close.

| SHA | PR / intent | Primary lane | Resolution | Validation |
|---|---|---|---|---|
| `c42ba4d4a` | #3671 confirm only busy interface switches | runtime / frontend | pending | pending |
| `7ecf58970` | #3604 bug-triage skills | distribution | pending | pending |
| `a817939fa` | #3664 merge inspector review sections | frontend / review | pending | pending |
| `3f7b5288e` | #3619 SCM all-check-run guard | backend / SCM | pending | pending |
| `09a87eb17` | #3677 deterministic Muse native-session resume | provider continuity | pending | pending |
| `53ccd9340` | #3675 Muse model catalog | provider / frontend | pending | pending |
| `f17013b53` | #3609 terminal edge spacing | frontend | pending | pending |
| `fb0f85fa4` | #3489 landing analytics and scoped replay | landing / telemetry | pending | pending |
| `8055c9ee0` | #3683 Muse awaiting-input detection | provider / lifecycle | pending | pending |
| `9c475bf71` | #3657 new-task composer defaults | frontend | pending | pending |
| `b168a4e21` | #3680 opaque jump-to-latest button | frontend | pending | pending |
| `bad160129` | #3691 combined composer and attachments | frontend / chat | pending | pending |
| `fc5f4ca02` | #3694 retain current page for Settings | frontend | pending | pending |
| `4909d16f2` | #3672 center-panel corners | frontend | pending | pending |
| `d15fd8277` | #3484 reviewer terminal lifecycle | review / lifecycle | pending | pending |
| `53197448f` | #3701 model-menu scrolling | frontend | pending | pending |
| `49e85355a` | #3696 single-click project board open | frontend | pending | pending |
| `e43989f4f` | #3697 always-expanded Projects section | frontend | pending | pending |
| `72811b755` | #3384 interactive reviewers for all harnesses | review / security | pending | pending |
| `f7e0fe65b` | #3706 sidebar and settings polish | frontend | pending | pending |
| `1ec838b8a` | #3728 README translation move | docs / distribution | pending | pending |
| `2904ef7e8` | #3726 contained activity-row inset | frontend | pending | pending |
| `76a03ca0e` | #3715 unsupported chat agents use TUI | frontend / provider | pending | pending |
| `31d920b5a` | #3724 image attachments in chat messages | chat / frontend | pending | pending |
| `67a02b44a` | #3723 terminal colors follow theme | frontend / terminal | pending | pending |
| `e00123f6a` | #3721 auto-growing chat composer | frontend / chat | pending | pending |
| `237f6a6ab` | #3725 chat horizontal overflow | frontend / chat | pending | pending |
| `ce3b24a99` | #3730 scope font controls to conversation | frontend / chat | pending | pending |
| `b264e7765` | #3727 Codex configured effort default | provider / chat | pending | pending |
| `eff05a0e3` | #3719 stale-idle interface drain | lifecycle / session | pending | pending |
| `a3fbf8850` | #3711 recover chat sessions after controller stop | lifecycle / chat | pending | pending |
| `586d863bb` | #3717 chat links and selection | frontend / chat | pending | pending |
| `66821b3f5` | #3359 automatic browser content and activity badge | browser / frontend | pending | pending |
| `cf3b9f41e` | #3666 large-diff viewer performance | frontend / workspace | pending | pending |
| `61f481d61` | #3731 remove obsolete screenshots | docs | pending | pending |
| `c9e1c676b` | #3473 session-owned browser automation | browser / desktop | pending | pending |
| `6c72814f9` | #3736 Prime Agent harness | provider / review | pending | pending |
| `ec23eaf4f` | #3733 preserve cancel frames after writes | browser / runtime | pending | pending |
| `b6609ae61` | #3742 gate review rows until verdict | review / frontend | pending | pending |
| `fd3f86f77` | #3661 mobile PostHog analytics | mobile / telemetry | pending | pending |
| `22c121720` | #3750 native browser composition and docked DevTools | browser / desktop | pending | pending |
| `6f2eb5dea` | #3663 uniform telemetry client tags | telemetry | pending | pending |
| `67199afa8` | #3704 per-name mobile telemetry cap | mobile / telemetry | pending | pending |
| `0c98a7a8a` | #2649 Kimchi harness | provider / review | pending | pending |
| `d293ea81f` | #3735 Codex interface-switch readiness | provider / lifecycle | pending | pending |
| `f65c48e29` | #3709 review feedback injection toggle | review / API / frontend | pending | pending |
| `6a7cdd231` | #3688 PR fallback uses merge-base | SCM / session | pending | pending |
| `9584c754d` | #3545 iOS photo-library purpose string | mobile / distribution | pending | pending |
| `047ff07e7` | #3625 browser annotation snapshot and prompt | browser / API / frontend | pending | pending |
| `7b76a727b` | #3689 TestFlight download flow | landing / mobile | pending | pending |
| `84f0c26c8` | #3781 remove redundant DevTools control | browser / frontend | pending | pending |
| `1b63debca` | #3705 composer and settings polish | frontend | pending | pending |
| `01cb67fe1` | #3548 durable agent switching | switch engine / storage / API / UI | pending | pending |
| `6e9dbb105` | #3782 constrain and stop Kimi reviewers | review / runtime | pending | pending |

## Pre-merge conflict inventory

`git merge-tree roles/multi-sub-v1 <pin>` reports 49 content conflicts. Generated
OpenAPI, TypeScript API schema, and sqlc outputs are never resolved by choosing
one side; their source conflicts are resolved and the generators are rerun.

| Area | Conflicted files | Required invariant |
|---|---|---|
| Provider adapters | Claude and Codex implementations and tests | retain fork authorization/config rules while adopting native continuity |
| CLI | DTO drift, session, spawn | thin HTTP client; preserve upstream switch compatibility and fork usage errors |
| Daemon wiring | daemon and lifecycle wiring | manager wiring remains orchestrator-owned; no alternate storage/runtime path |
| Domain / ports | session and agent port | retain role facts while adopting switch/native-session contracts |
| HTTP / API | OpenAPI, DTO, session controller/tests | resolve source shapes then regenerate both artifacts |
| Session service | delegation and service implementations/tests | preserve role, pause, failover and orchestrator ownership |
| Session manager | chat spawn, interface transition, manager, provision/tests | manager and exported errors are orchestrator-only integration work |
| SQLite | DB, sessions query/store/tests, burned-version test, generated code | preserve `0085` verbatim and reconcile fork `900x` migrations semantically |
| Terminal | manager and tests | preserve terminal ownership and attachment behavior |
| Desktop build | Forge config, package manifest, main process, daemon discovery | all state remains under `~/.ao`; preserve distribution rules |
| Renderer | project settings, session view, Settings, TaskComposer, ChatWorkspace/tests | retain role/reviewer policy and integrate upstream UX/tests |

## Test inventory

Authoritative pre-merge inventories are being captured from separate detached
worktrees. Inventory output records every `go test -json` `run` event and the
Vitest renderer list JSON; results and reviewed union decisions are added here
before the merge result is accepted.

The backend/domain/storage/API/CLI inventory executed 15 touched packages on
both refs with no package failure:

- fork: 1,517 distinct test and subtest paths;
- upstream: 1,346 distinct test and subtest paths.

The raw JSON streams and parsed inventories are retained under
`/private/tmp/ao-sync3-019fe7a8/backend/inventories/` for the duration of the
integration. The post-merge union comparison is pending.

The runtime/lifecycle inventory executed 26 packages per ref with no test
failure:

- fork: 1,718 test and subtest paths;
- upstream: 1,593 test and subtest paths;
- shared: 1,102 paths;
- reviewed-union candidate: 2,209 paths.

The Vitest 4 renderer list inventory is:

- fork: 2,060 tests in 153 files, SHA-256
  `a5e1087ae2070bedf50fa45e4f564abde5fc0c28a2df6b6b3bc955205350df26`;
- upstream: 2,106 tests in 163 files, SHA-256
  `a9225c10906bb62cbb870bef069b62f71628d75ca0caac85aac760657866c3c6`;
- common: 1,872; fork-only: 188; upstream-only: 234.

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
  adding the pinned agent-browser runtime; package locks are regenerated.

## Migration ledger

- The upstream pin contains `0085_agent_switching.sql` and no fork `900x`
  migration.
- The upstream migration blob must remain byte-for-byte unchanged.
- The pinned `0085` blob is
  `8582b8b9b9e4b0398487fbbca19f91f016016957`; its SHA-256 is
  `b3871aaf81c982886f3385f276aed543abdce029a62057f1a6708a3b5c643bc3`.
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

## Gate results

Pending.
