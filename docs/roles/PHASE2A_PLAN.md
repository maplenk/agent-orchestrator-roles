# Phase 2A — worker switch (status after review fixes)

## Closed against P1 review (this pass)

| Finding | Fix |
|---------|-----|
| Ledger gen ≠ runtime gen | Single `ForceLaunchID` through `superviseAgentProcess` / relaunch; assert equality |
| Promote harness before ack | Durable `SwitchPending` (0045 `switch_pending_json`); promote only after durable `target_ack` |
| Recovery double-launch | Live wrong-gen → `ErrSwitchUncertain`; matching live gen → ack only |
| Mutex deadlock | Single `ownershipMu` for switch+resume |
| Destroy error ≠ source usable | Probe `IsAlive` after Destroy; dead → post_stop even if Destroy erred |
| Source model leak | Cross-harness empty TargetModel → clear (provider default) |
| switch_supported premature | Held **false** through dogfood; promoted only in separate final CL after close-out |
| Handoff stacking | Always `stripCompiledHandoff` before compose; store `OriginalTask` |
| Stable ledger ids | `{session}:{gen}:{phase}` with skip-if-exists |
| Input gate | sessionguard suppresses when `SwitchPending` set |

## API (manager + Service/API/CLI)

```go
SwitchWorker / FreshConversation / RecoverSwitchFromPostStop
```

Claude/Codex `switch_supported` **promoted** after Phase 2A close-out accept.

## Follow-up P1 close (after 7fdbf1aa)

| Finding | Fix |
|---------|-----|
| Pending after destroy | Persist `SwitchPending` (+ full payload) **before** destroy |
| Handoff lost without post_stop | `PayloadJSON` on pending; recover pre_stop payload fallback |
| Send reports success on suppress | `SuppressedSwitchPending` → `ErrSwitchInProgress` |
| After-start blocked | `DeliverHost` for host injection only |
| Terminal bypass | `InputGate` + daemon `SetInputGate(sessMgr)` |
| Recovery RO skip | `RequireReadOnly(toHarness)` on recover |
| Observed gen wrong | Attribute observe to **source** RuntimeLaunchID |
| Corrupt pending fail-open | `decodeSwitchPending` errors; GetSession fails closed |

## Follow-up P1 close (d511dfa2+)

| Finding | Fix |
|---------|-----|
| Confirmed-alive source unusable | Rollback pending + restore pre-switch metadata on probe-alive |
| Terminal handle ≠ session id | Resolve by RuntimeHandleID / SourceRuntimeHandleID |
| Recovery skips post_stop | `ensurePostStopLedger` before launch and ack |
| Optional gate wiring | `AllowTerminalInput` on `sessionLifecycle` (compile-time) |

## Dogfood follow-ups (landed; Claude/Codex switch promoted)

| Item | Status |
|------|--------|
| Indexed terminal gate (`GetSessionByRuntimeHandleID` / `GetSessionByPendingSourceHandle`) | **Done** — no `ListAllSessions` scan |
| Rollback fail → `ErrSwitchUncertain` | **Done** + unit test |
| Adversarial: confirmed-alive rollback fail | **Done** (`TestSwitchWorker_RollbackFailWrapsErrSwitchUncertain`) |
| Adversarial: post_stop append fail blocks launch/ack | **Done** (switch + recover paths) |
| Adversarial: terminal mux Write suppressed under InputGate | **Done** (`TestServeWriteSuppressedWhenInputGateBlocks`) |
| Production `switch_supported` (Claude/Codex) | **Promoted** after close-out accept |

### Manual dogfood checklist (pre-promotion evidence)

**Checkpoint SHA:** `2d19ad59` (pushed). Full results: [`PHASE2A_DOGFOOD.md`](./PHASE2A_DOGFOOD.md).

| # | Item | Manager dogfood @ 2d19ad59 |
|---|------|----------------------------|
| 0 | Production caps false / refuse without override | **PASS** (pre-promotion; item flipped at promote) |
| 1 | Claude → Codex | **PASS** (gen=runtime; ledger order+ids) |
| 2 | Codex → Claude | **PASS** |
| 3 | FreshConversation no stack | **PASS** |
| 4 | Crash recovery post_stop | **PASS** (reuse gen; no double-launch) |
| 4b | Stale wrong-gen → uncertain | **PASS** |
| 5 | Confirmed-alive rollback | **PASS** |
| 5b | Rollback persist fail → uncertain | **PASS** |
| 6 | Terminal fence + mux write suppress | **PASS** |
| 7 | post_stop fail blocks launch | **PASS** |

Harness: `TestDogfood_Phase2AChecklist` + terminal mux test. Live evidence: [`PHASE2A_LIVE_DOGFOOD.md`](./PHASE2A_LIVE_DOGFOOD.md).

## Service / API / CLI (landed; Claude/Codex switch live)

| Surface | Detail |
|---------|--------|
| Service | Exact `(harness, model)` via `ResolveAuthorizedSwitchModel`; ambiguous omitted model → `TARGET_MODEL_REQUIRED` |
| HTTP | `POST …/switch`, `…/fresh-conversation` — operator/LAN global; session principals need role pin + canSpawn + **same project** |
| CLI | `ao session switch|fresh` attach `spawnCallerHeaders()` (managed-session no-upgrade) |
| Desktop | `applyOperatorSpawnHeaders` covers switch/fresh paths |
| Errors | `SWITCH_AUTH_REQUIRED`, `SWITCH_TARGET_UNAUTHORIZED`, `TARGET_MODEL_REQUIRED`, `SWITCH_NOT_SUPPORTED` |
| Failover config-save | `ValidateRoleMap` checks failover rungs for spawn + inherited RO + **switch_supported**, and requires **switch_supported on the primary binding of any role with a non-empty ladder** (the switch source). Both active after promotion. |

## Phase 2A promotion checklist — all closed

1. ~~Manager-level dogfood checklist~~ — **recorded in PHASE2A_DOGFOOD.md**
2. ~~Service/API/CLI role-map targets + auth~~ — **accepted** @ `83f7abfb`
3. ~~Target-authoritative switch prompt~~ — **done** @ `a3bc32be` (live footer `Harness: codex`)
4. ~~Live Claude↔Codex + crash protocol~~ — **evidence** in PHASE2A_LIVE_DOGFOOD.md
5. ~~Concurrent daemon ownership~~ — **`datadirlock`** before store/reconcile; clean crash ledger re-dogfood without `failed`
6. ~~Promote `switch_supported`~~ — **done** (Claude/Codex in `capabilities.For`; failover switch validation active)

Living plan status: `REMAINING_PLAN.md` (keep in sync on each phase land). Next critical path: **Phase 2B**.
