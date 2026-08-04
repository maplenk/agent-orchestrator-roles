# Phase 1c+ — launch P1 closure (Codex re-review)

## Fixes since last reject

### 1. `workspaceWrites:false` — fail closed for ALL harnesses

Any role with `workspaceWrites:false` → `ErrReadOnlyUnsupported` before launch.  
No prompt-only / PermissionModeDefault claim (Codex default = full sandbox bypass).

### 1b. Strict orchestrator no longer bypasses role policy

When `strictDelegation` and `KindOrchestrator` with no `RoleID`:

1. Auto-resolve `orchestratorRole`
2. Role requires `workspaceWrites:false` (Validate)
3. `applyRoleMap` → **ErrReadOnlyUnsupported**
4. **No adapter launch** (`launchCalls == 0`)

Tests:
- `TestResolve_StrictOrchestratorAutoBindsRole`
- `TestSpawn_StrictOrchestratorFailsClosedWithoutLaunch`

Non-strict maps: legacy orch without RoleID still allowed.

### 2. Empty role model clears project model — closed

### 3. Role footer after base prompt — closed

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:$PATH"
cd backend && go test ./internal/domain/ ./internal/roles/ ./internal/session_manager/ -count=1
```

## Still open (not this slice)

- Adapter read-only launch mode (required before orch/reviewer roles can start)
- Migration 0042
- canSpawn session credentials

## Ask Codex

Accept Phase 1c launch/prompt P1s closed? Proceed to migration 0042?
