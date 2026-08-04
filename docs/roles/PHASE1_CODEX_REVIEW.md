# Phase 1 (partial) — Codex review pack

**Branch:** `roles/multi-sub-v1`  
**Tree:** `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`  
**Baseline:** `742c77bcb5d3591c540ef0e8eb71947a7c4a85f8`  
**Date:** 2026-08-04  

This is **Phase 1 foundation**, not full Phase 1 DoD. Review for architecture correctness and landmines before we continue (sqlc migration, canSpawn credentials, OpenAPI regen, end-to-end tests).

---

## Summary of what landed

### Domain

| File | Purpose |
|------|---------|
| `backend/internal/domain/rolemap.go` | `RoleMap`, `RoleBinding`, `RoleExecutionPolicy`, `FailoverConfig`, `SessionRoleBinding`, Validate, SHA256 |
| `backend/internal/domain/rolemap_test.go` | Unit tests (strict orch write/spawn, default model ban, failover role existence) |
| `backend/internal/domain/projectconfig.go` | `RoleMap` field + Validate hook |
| `backend/internal/domain/session.go` | `SessionMetadata.Role` (in-memory/metadata; **not SQL columns yet**) |
| `backend/internal/ports/session.go` | `SpawnConfig.RoleID`, `RoleBinding` |

### Roles package

| File | Purpose |
|------|---------|
| `backend/internal/roles/templates.go` | Frontmatter parse, CAS `ArtifactStore`, `Loader` with path containment |
| `backend/internal/roles/resolve.go` | Strict require role; forbid harness override; pin binding; `DelegationContractMarkdown` |
| `backend/internal/roles/resolve_test.go` | Resolve + contract tests |

### Runtime wiring

| File | Change |
|------|--------|
| `session_manager/role_resolve.go` | `applyRoleMap`, template roots (`AO_ROLE_PROFILES_DIR`) |
| `session_manager/manager.go` | Call `applyRoleMap` before `effectiveHarness`; seed metadata role; inject role sections into system prompt |
| `session_manager/prompt.go` | Orch spawn docs teach `--role` over `--agent` |
| `cli/spawn.go` | `--role` flag; mutually exclusive with harness; `roleId` on wire |
| `httpd/controllers/dto.go` | `SpawnSessionRequest.roleId` |
| `httpd/controllers/sessions.go` | Pass `RoleID` into `SpawnConfig` |

### Profiles / docs

| Path | Purpose |
|------|---------|
| `profiles/orchestrator.md`, `implementor.md`, `ui-implementor.md`, `reviewer.md` | Intent-style templates |
| `docs/roles/examples/role-map.strict.example.json` | Example project config fragment |
| `docs/roles/MASTER_PLAN.md`, `FORK.md`, `CAPABILITY_MATRIX.md` | Plan + pin |

---

## Explicitly **not** done (Phase 1 remaining)

1. **SQL migration 0042** — durable columns + template_artifacts CAS table; sqlc regen; store mapping  
2. **Session-scoped spawn credential** — `canSpawn` not enforced at API (only policy fields exist)  
3. **Capability matrix at Validate** — `ValidateWithCapabilities` not wired; Pi read-only not rejected at config save  
4. **OpenAPI / `npm run api` / frontend schema**  
5. **CLI projectConfig mirror** for roleMap via set-config  
6. **Restore path** loads pinned artifact (still recomputes system prompt from live project)  
7. **DTO drift e2e test** update for `roleId`  
8. **service/session toAPIError** codes for role errors  
9. **Strict: reject worker spawn with only --harness when project.strictDelegation** if RoleID empty — done in Resolve; need **integration test** with real Manager.Spawn  
10. **go test** — Go toolchain was being installed; verify green suite  

---

## Suggested Codex review focus

### A. Host authority

- [ ] Is `applyRoleMap` early enough that free-form harness cannot win on strict projects?  
- [ ] CLI omits harness when `--role` set — does any client still force harness after?  
- [ ] Should strict mode reject **empty RoleID** even when client sends default worker harness from legacy UI?

### B. Template / CAS

- [ ] In-memory `ArtifactStore` is process-local — restore across daemon restart will fail until SQL CAS. Accept for this slice?  
- [ ] Loader roots (`Getwd()/profiles`) fragile for installed app binaries — prefer embed or install-time path?  
- [ ] Frontmatter parse edge cases (`---\r\n`, empty body)

### C. Prompt composition

- [ ] Role template prepended then full orch/worker prompt — order OK vs “recency footer”?  
- [ ] Double `applyRoleMap` in Spawn + buildSpawnTexts — side effects / harness mutation twice?  
- [ ] Strict orch still has AO “confirm then edit” language elsewhere in `orchestratorSystemPrompt`? (plan requires single no-edit rule — **not fully done**)

### D. Config model

- [ ] `RoleMap` nested under `ProjectConfig.roleMap` vs top-level document fields in MASTER_PLAN example — JSON shape for set-config  
- [ ] Failover rungs not capability-validated yet  

### E. Correctness risks

- [ ] `seedRecord` puts role in `Metadata.Role` but store may not persist Metadata.Role fields — **durability gap**  
- [ ] `globalTemplateLoaderOnce` never refreshes roots if `AO_ROLE_PROFILES_DIR` set after first call  

---

## How to exercise (once `go` available)

```bash
cd /Users/tagtaste/Documents/QBApps/agent-orchestrator-roles/backend
export PATH="/opt/homebrew/bin:$PATH"
go test ./internal/domain/ ./internal/roles/ -count=1

# Later, with daemon + project roleMap set via set-config JSON:
export AO_ROLE_PROFILES_DIR=/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles/profiles
ao spawn --project <id> --role implementor --name "demo-impl" --prompt "…"
# Strict project without --role should 400 ROLE_REQUIRED
```

Example role map: `docs/roles/examples/role-map.strict.example.json` — nest under project config as:

```json
{ "roleMap": { ...contents of example... } }
```

(Exact set-config merge TBD when CLI mirror lands.)

---

## Files changed (review this diff)

```
backend/internal/domain/rolemap.go
backend/internal/domain/rolemap_test.go
backend/internal/domain/projectconfig.go
backend/internal/domain/session.go
backend/internal/ports/session.go
backend/internal/roles/*
backend/internal/session_manager/role_resolve.go
backend/internal/session_manager/manager.go
backend/internal/session_manager/prompt.go
backend/internal/cli/spawn.go
backend/internal/httpd/controllers/dto.go
backend/internal/httpd/controllers/sessions.go
profiles/*
docs/roles/*
```

---

## Ask Codex for

1. Approve / reject this foundation slice.  
2. Ordered fix list before Phase 1 “complete”.  
3. Whether in-memory CAS + Metadata-only role pin is acceptable interim or must block on migration 0042.  
4. Any security issue with trusting RoleID from unauthenticated loopback spawn without canSpawn yet.  
