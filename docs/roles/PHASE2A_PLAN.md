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

## Still open before accept / API / CLI

1. Broader adversarial + dogfood Claude↔Codex
2. Promote `switch_supported` only after dogfood
3. Service/API/CLI with host role-map authorized targets (no free-form harness exposure)
4. Pre_stop uncertain recovery paths beyond post_stop (partial)
