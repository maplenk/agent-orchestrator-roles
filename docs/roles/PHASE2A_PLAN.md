# Phase 2A — worker switch (execution notes)

## Landed

| Slice | Status | Detail |
|-------|--------|--------|
| **2A.0** | Done | SemanticHandoff / ObservedWorkspace / Compile / migration 0044 ledger |
| **2A.1** | Done | Switch fence (`ErrSwitchInProgress`), durable phases → ledger |
| **2A.2** | Done | `handoff.ObserveWorkspace` (git branch/HEAD/porcelain) |
| **2A.3** | Done | `SwitchWorker` Claude↔Codex (destroy runtime, keep worktree, relaunch) |
| **2A.4** | Done | `FreshConversation` same-harness via switch saga |
| Caps | Done | Claude + Codex `switch_supported=true` (limit still false) |
| **2A.5** | Done | Crash recovery from `post_stop` + boot `Reconcile` integration |

## API surface (session_manager)

```go
SwitchWorker(ctx, SwitchRequest) (SwitchResult, error)
FreshConversation(ctx, sessionID, SemanticHandoffV1) (SwitchResult, error)
RecoverSwitchFromPostStop(ctx, sessionID) (SwitchResult, error)
```

Saga phases written to `lifecycle_ledger`:
`requested` → `pre_stop` → `post_stop` → `target_ack` (or `failed`)

| Failure mode | Behavior |
|--------------|----------|
| Pre-stop (destroy fails) | Source usable; phase `failed` |
| Post-stop (relaunch fails) | Handoff in ledger; `ErrSwitchPostStop`; recover via `RecoverSwitchFromPostStop` |
| Daemon crash after post_stop | Boot `Reconcile` re-drives before live teardown pass |

### Recovery rules

- Newest `post_stop` generation without `target_ack` is recoverable (a later `failed` does not clear it).
- Payload JSON supplies compiled handoff; target harness/model come from the ledger row.
- `reconcileLive` refuses to terminate sessions with incomplete post_stop (defense in depth).
- Idempotent: second recover returns `ErrSwitchNothingToRecover`.

## Still open

1. HTTP/CLI surface for switch + fresh + recover  
2. Input ownership fence in sessionguard/lifecycle during switch  
3. Service-layer wiring + API errors (409/400)  
4. Real dogfood + review pack  
5. Phase 2B orchestrator ownership transfer  

## Non-goals

- Orchestrator switch (2B)  
- Synara ports  
- Full chat ledger  
