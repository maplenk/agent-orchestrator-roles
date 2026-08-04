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

Run against a real desktop build with `switchCapsOverride` / temporary test caps **or** a private dogfood binary that sets switch_supported — do **not** merge production true until all pass:

1. **Claude → Codex worker switch** on an implementor session with live workspace
   - Source stops; target launches with host-compiled handoff
   - Terminal input blocked during pending; resumes after ack
   - Ledger: requested → pre_stop → post_stop → target_ack (stable ids)
2. **Codex → Claude** reverse path (same role pin)
3. **Same-harness FreshConversation** (no stacking of `## Host-compiled handoff`)
4. **Crash mid-switch**: kill daemon after source stop / before ack; on boot `RecoverSwitchFromPostStop` completes or reports uncertain without double-launch
5. **Confirmed-alive pre-stop**: if destroy fails to kill, source remains usable (pending rolled back) or returns `ErrSwitchUncertain` if rollback cannot persist
6. **Terminal fence**: during pending, client PTY writes get `input blocked: switch in progress` (mux + session id / sanitized handle)

## Still open before accept / API / CLI

1. ~~Broader adversarial unit coverage~~ (see table above) — **manual dogfood still required**
2. Promote `switch_supported` only after dogfood checklist
3. Service/API/CLI with host role-map authorized targets (no free-form harness exposure)
