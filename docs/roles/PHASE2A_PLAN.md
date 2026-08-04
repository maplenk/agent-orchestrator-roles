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

## API surface (session_manager)

```go
SwitchWorker(ctx, SwitchRequest) (SwitchResult, error)
FreshConversation(ctx, sessionID, SemanticHandoffV1) (SwitchResult, error)
```

Saga phases written to `lifecycle_ledger`:
`requested` → `pre_stop` → `post_stop` → `target_ack` (or `failed`)

| Failure mode | Behavior |
|--------------|----------|
| Pre-stop (destroy fails) | Source usable; phase `failed` |
| Post-stop (relaunch fails) | Handoff in ledger; `ErrSwitchPostStop` |

## Still open

1. HTTP/CLI surface for switch + fresh  
2. Input ownership fence in sessionguard/lifecycle during switch  
3. Service-layer wiring + API errors (409/400)  
4. Crash recovery re-drive from post_stop ledger  
5. Real dogfood + review pack  
6. Phase 2B orchestrator ownership transfer  

## Non-goals

- Orchestrator switch (2B)  
- Synara ports  
- Full chat ledger  
