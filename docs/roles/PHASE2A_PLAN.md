# Phase 2A — worker switch (execution notes)

**Status:** first slice landed (domain + compiler + migration 0044 ledger).  
**Not done:** saga orchestration, API, promoting `switch_supported`.

## Landed (2A.0)

| Item | Path |
|------|------|
| SemanticHandoffV1 / ObservedWorkspaceV1 / CompiledHandoff | `domain/handoff.go` |
| Lifecycle ledger kinds/phases | `domain/lifecycle_ledger.go` |
| Compiler (observed overrides semantic) | `handoff/compile.go` |
| Migration 0044 append-only table | `migrations/0044_lifecycle_ledger.sql` |
| Store append + list | `store/lifecycle_ledger_store.go` |

## Next slices

1. **2A.1** — `session_manager` switch fence (`ErrSwitchInProgress`) + durable saga phases writing ledger  
2. **2A.2** — Observe workspace (git) into `ObservedWorkspaceV1`  
3. **2A.3** — Worker switch Claude↔Codex path using `relaunchSession` (not orch RetireForReplacement)  
4. **2A.4** — Same-harness fresh conversation (`kind=fresh_conversation`)  
5. **2A.5** — Promote `switch_supported` for Claude/Codex after dogfood  
6. **2A.6** — API/CLI + review pack

## DoD (MASTER_PLAN)

- Pre-stop failure → source usable  
- Post-stop failure → handoff retained, target retry  
- One generation owns input at boundary  
- Ledger records every switch/fresh  

## Non-goals

- Orchestrator ownership transfer (2B)  
- Synara ports  
- Full chat ledger  
