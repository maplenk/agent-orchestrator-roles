# Phase 1b corrective slice — Codex re-review pack

**Branch:** `roles/multi-sub-v1`  
**Tree:** `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`  
**Responds to:** Codex reject of foundation slice (P1 launch/prompt/durability)  

---

## Verdict context

Previous slice was **rejected** for landing as a functional Phase 1 foundation. This slice addresses the **P1 correctness** items Codex said not to block on migration 0042, while keeping 0042 / canSpawn credentials as **still open**.

---

## What this slice fixed

### 1. Resolved model applied to launch

`Manager.Spawn` now:

```text
agentConfig := mergeAgentConfig(effectiveAgentConfig(...), roleResult.AgentConfigPatch, roleResult.Policy, roleResult.Applied)
```

Role `ResolvedModel` is copied into `AgentConfigPatch.Model` in `applyRoleMap` and reaches `LaunchConfig.Config.Model`.

### 2. workspaceWrites policy partially enforced

- Fail closed if `workspaceWrites:false` and harness is **Pi** (`ErrReadOnlyUnsupported`).
- Claude/Codex allowed as interim (`harnessEnforcesReadOnly`).
- Injects **HARD HOST POLICY: workspaceWrites=false** system section.
- Strips `bypass-permissions` when role applies `workspaceWrites:false`.
- Zero-value policy does **not** strip bypass when no role applied (`policyApplied` flag).

**Honest gap:** adapter-level FS sandbox still incomplete; policy is host gate + prompt + permission mode, not full OS sandbox.

### 3. Single role resolution; fail closed on prompt

- Removed second `applyRoleMap` in `buildSpawnTexts`.
- `roleApplyResult` carried from Spawn → `buildSpawnTexts(ctx, cfg, roleResult)`.
- Empty ExtraSystem when `Applied` → `ErrRolePromptRequired` (spawn fails).
- Empty template body → fail at `applyRoleMap`.

### 4. Reject all harness overrides when `--role` set

Even matching harness → `ErrHarnessOverrideForbidden` (`roles/resolve.go`).

### 5. Orchestrator prompt: one no-edit rule

Removed “confirm then edit” exception. Unconditional NEVER edit + spawn worker with `--role`. Tests assert banned strings absent.

---

## Tests

```bash
export PATH="/opt/homebrew/opt/go/bin:$PATH"
cd backend && go test ./internal/domain/ ./internal/roles/ ./internal/session_manager/ -count=1
```

**Expected:** all three packages `ok` (includes new `role_resolve_test.go`).

---

## Still open (ordered, from prior Codex list)

| # | Item | Status |
|---|------|--------|
| 3 | Migration 0042 + sqlc + CAS table + restore-by-artifact | **Next milestone** |
| 4 | Session-scoped spawn credentials + canSpawn at daemon | Open — do not claim security boundary |
| 5 | Capability matrix at config-save | Open |
| 6 | Installation-safe template roots / cache invalidation | Partial (`AO_ROLE_PROFILES_DIR`, test override) |
| 7 | Reject all overrides | **Done** |
| 8 | Full Manager integration tests (persist, restore, drift) | Partial unit tests only |
| 9 | API error codes, OpenAPI, CLI set-config mirror, DTO drift | Open |

---

## Ask Codex

1. Accept this corrective slice as unblocking **launch + prompt** P1s, or still reject?  
2. Is interim read-only (Claude/Codex allow + Pi reject + system policy) acceptable until reviewer subsystem / matrix?  
3. Confirm: next work should be **migration 0042 only**, with canSpawn credential in the same PR or after?  
4. Any remaining silent-success paths on role spawn?

---

## Key files touched this slice

```
backend/internal/session_manager/role_resolve.go   (rewrite)
backend/internal/session_manager/role_resolve_test.go
backend/internal/session_manager/manager.go
backend/internal/session_manager/prompt.go
backend/internal/session_manager/prompt_test.go
backend/internal/session_manager/manager_test.go
backend/internal/roles/resolve.go
backend/internal/roles/resolve_test.go
```
