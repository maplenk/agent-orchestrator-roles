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
| switch_supported premature | Claude/Codex back to **false**; tests use `switchCapsOverride` |
| Handoff stacking | Always `stripCompiledHandoff` before compose; store `OriginalTask` |
| Stable ledger ids | `{session}:{gen}:{phase}` with skip-if-exists |
| Input gate | sessionguard suppresses when `SwitchPending` set |

## API (manager only — not production-capable until switch_supported)

```go
SwitchWorker / FreshConversation / RecoverSwitchFromPostStop
```

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

## Dogfood follow-ups (landed in code; switch_supported still false)

| Item | Status |
|------|--------|
| Indexed terminal gate (`GetSessionByRuntimeHandleID` / `GetSessionByPendingSourceHandle`) | **Done** — no `ListAllSessions` scan |
| Rollback fail → `ErrSwitchUncertain` | **Done** + unit test |
| Adversarial: confirmed-alive rollback fail | **Done** (`TestSwitchWorker_RollbackFailWrapsErrSwitchUncertain`) |
| Adversarial: post_stop append fail blocks launch/ack | **Done** (switch + recover paths) |
| Adversarial: terminal mux Write suppressed under InputGate | **Done** (`TestServeWriteSuppressedWhenInputGateBlocks`) |
| Production `switch_supported` | **Still false** until manual dogfood |

### Manual dogfood checklist (before promoting switch_supported)

**Checkpoint SHA:** `2d19ad59` (pushed). Full results: [`PHASE2A_DOGFOOD.md`](./PHASE2A_DOGFOOD.md).

| # | Item | Manager dogfood @ 2d19ad59 |
|---|------|----------------------------|
| 0 | Production caps false / refuse without override | **PASS** |
| 1 | Claude → Codex | **PASS** (gen=runtime; ledger order+ids) |
| 2 | Codex → Claude | **PASS** |
| 3 | FreshConversation no stack | **PASS** |
| 4 | Crash recovery post_stop | **PASS** (reuse gen; no double-launch) |
| 4b | Stale wrong-gen → uncertain | **PASS** |
| 5 | Confirmed-alive rollback | **PASS** |
| 5b | Rollback persist fail → uncertain | **PASS** |
| 6 | Terminal fence + mux write suppress | **PASS** |
| 7 | post_stop fail blocks launch | **PASS** |

Harness: `TestDogfood_Phase2AChecklist` + terminal mux test. Override only; **production `switch_supported` remains false**.

**Still deferred:** live Claude/Codex desktop processes (needs Service/API/CLI).

## Service / API / CLI (landed; caps still false)

| Surface | Detail |
|---------|--------|
| Service | `SwitchWorker` / `FreshConversation` authorize via `roleMap` binding + `failover.roles` (`domain.SwitchTargetAuthorized`) |
| HTTP | `POST /api/v1/sessions/{id}/switch`, `POST /api/v1/sessions/{id}/fresh-conversation` |
| CLI | `ao session switch --session … --harness …`, `ao session fresh --session …` |
| Errors | `SWITCH_TARGET_UNAUTHORIZED` (403), `SWITCH_NOT_SUPPORTED` (409 while caps false), post_stop/uncertain/in-progress |
| Free-form harness | **Rejected** unless on host role-map authorized set |

## Still open before promotion

1. ~~Manager-level dogfood checklist~~ — **recorded in PHASE2A_DOGFOOD.md**
2. ~~Service/API/CLI role-map targets~~ — **landed** (caps still false)
3. Live desktop Claude↔Codex + crash/restart dogfood (controlled local-only cap enablement)
4. Promote `switch_supported` in a **separate** final change
