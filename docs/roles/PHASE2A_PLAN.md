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

## Still open before accept / API / CLI

1. Broader adversarial + dogfood Claude↔Codex
2. Promote `switch_supported` only after dogfood
3. Service/API/CLI with host role-map authorized targets (no free-form harness exposure)
