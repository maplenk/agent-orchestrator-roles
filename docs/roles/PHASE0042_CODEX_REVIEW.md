# Phase 0042 — durable role pin + template CAS (Codex review)

## Status

Phase 1c launch/prompt P1s closed (accepted). This slice lands **migration 0042** and restore-by-artifact wiring.

**Codex rejected durability once** (two CAS fail-closed P1s). Both fixed below — re-review requested.

Verified locally:

```bash
export PATH="/opt/homebrew/opt/go/bin:/opt/homebrew/bin:$PATH"
cd backend && go test ./internal/domain/ ./internal/roles/ ./internal/session_manager/ \
  ./internal/storage/sqlite/... -count=1
```

All packages **ok**.

---

## What landed

### 1. Schema (migration `0042_session_role_fields.sql`)

**`sessions` columns** (empty defaults for legacy rows):

| Column | Domain |
|--------|--------|
| `role_id` | `SessionRoleBinding.RoleID` |
| `role_map_schema_version` | schema version pin |
| `role_map_sha256` | map content hash at spawn |
| `role_config_revision` | optional revision |
| `template_artifact_id` | CAS id (`sha256:<hex>`) |
| `template_sha256` | hex digest |
| `resolved_model` | host-resolved model (empty = provider default) |
| `resolved_workspace_writes` | policy bool |
| `resolved_can_spawn` | policy bool |

**No `resolved_harness` column** — current harness is `sessions.harness`. On read, when `role_id` is set, `Metadata.Role.ResolvedHarness = row.Harness`.

**CAS table `template_artifacts`:**

```text
id (PK), sha256, content BLOB, created_at
```

### 2. sqlc + store

- `queries/sessions.sql` — insert/update/get/list include role columns
- `queries/template_artifacts.sql` — `UpsertTemplateArtifact` (ON CONFLICT DO NOTHING), `GetTemplateArtifact`
- `store/session_store.go` — `Metadata.Role` ↔ columns
- `store/template_artifact_store.go` — `PutTemplateArtifact` / `GetTemplateArtifact`

### 3. Spawn dual-write (fail-closed)

After `applyRoleMap` succeeds with a role applied:

1. `persistRoleTemplateArtifact` requires non-empty artifact id, sha, raw bytes
2. `store.PutTemplateArtifact(...)` — durable SQL CAS
3. Any put failure aborts spawn (no role session without pinned template)

`seedRecord` still writes `Metadata.Role` on `CreateSession` so role columns land with the seed row.

### 4. Restore / relaunch by artifact (not live profiles)

`relaunchSession` → `buildRestoreSystemPrompt`:

1. **Base** standing prompt still from live project (`buildSystemPrompt`) — OK for generic AO instructions
2. **Role body** only from CAS via `GetTemplateArtifact(TemplateArtifactID)`
3. Missing artifact → **fail closed** (no silent re-load from `profiles/*.md`)
4. SHA check vs pinned `TemplateSHA256` when set
5. `composeSystemPromptWithRole` footer path reused
6. Optional live `DelegationContractMarkdown` for orch + strict map (catalog text may refresh; **template body must not**)
7. `restoreAgentConfig` reapplies `ResolvedModel` host-authoritatively (`mergeAgentConfig`)

`lifecycle.mergeMetadata` only merges workspace/runtime/prompt fields — **Role is preserved** across `MarkSpawned`.

---

## Tests (new / relevant)

| Test | Claim |
|------|--------|
| `TestSessionPersistsRoleMetadata` | Role columns round-trip Create/Update/Get |
| `TestTemplateArtifactCAS` | Put/get/idempotent put; missing → ok=false |
| `TestParseTemplate_CASBytesPreserveSystemPrompt` | CAS bytes → stable SystemPrompt |
| `TestBuildRestoreSystemPrompt_UsesPinnedArtifact` | Restore includes pinned role body |
| `TestBuildRestoreSystemPrompt_MissingArtifact` | Fail closed without CAS row |
| `TestRestoreAgentConfig_ReappliesResolvedModel` | Model pin on restore |
| Prior Phase 1c suite | Still green under session_manager/roles/domain |

---

## Invariants claimed for Codex

1. Role pin is **durable** in SQLite (not memory-only).
2. Template bytes are **content-addressed** and survive daemon restart.
3. Spawn of a role **requires** successful CAS put.
4. Restore of a role session **requires** CAS get; never re-reads profile files for the role body.
5. Empty role model still overwrites project model on restore (host-authoritative).
6. `workspaceWrites:false` still fail-closed at spawn (unchanged from 1c).
7. **Partial role pin never restores as legacy** — only fully empty pin is legacy; incomplete → `ErrIncompleteRolePin`.
8. **CAS put verifies stored sha+bytes** after idempotent insert; conflict → `ErrTemplateArtifactConflict`.

---

## P1 fixes after Codex reject

### 1. Incomplete role pin fail-closed (`role_resolve.go`)

Was: `RoleID == "" || TemplateArtifactID == ""` → unapplied (legacy).

Now:

| State | Behavior |
|-------|----------|
| both empty, no other role fields | legacy unapplied |
| both empty, but other role fields set | `ErrIncompleteRolePin` |
| only one of role_id / template_artifact_id | `ErrIncompleteRolePin` |
| both set | CAS path (missing artifact still fail closed) |

Test: `TestBuildRestoreSystemPrompt_IncompleteRolePinFailClosed`

### 2. CAS conflict after ON CONFLICT DO NOTHING (`template_artifact_store.go`)

Was: Put always succeeded even when id already held different content.

Now: after insert, re-read under write lock; require exact sha256 + bytes match; else `ErrTemplateArtifactConflict`. Same content remains idempotent.

Test: `TestTemplateArtifactCAS_RejectsConflictingContent`

### 3. Optional CHECK (accepted suggestion)

`resolved_workspace_writes` / `resolved_can_spawn` columns include `CHECK (... IN (0, 1))` in migration 0042 (unmerged; amended in place).

### 4. Store hydration of partial pins (`session_store.go`) — second reject P1

Was: `rowToRecord` only built `Metadata.Role` when `role_id != ""`, so artifact-only (or other partial) columns were wiped on read → restore saw empty binding → silent legacy.

Now: `roleFromSessionRow` hydrates whenever **any** role-specific column is non-default (`role_id`, artifact id/sha, map sha/version/revision, model, policy flags). `sessions.harness` alone does not trigger hydration (shared with non-role sessions) but is copied into `ResolvedHarness` when a pin is present.

Test: `TestSessionPersistsPartialRolePin_ArtifactOnly`

---

## Still open (not this slice)

| Item | Why open |
|------|----------|
| Adapter **read-only launch mode** | Orch/reviewer roles cannot start until harness can enforce `workspaceWrites:false` |
| **canSpawn** session credentials | Host gate so workers cannot call spawn API |
| Capability matrix at **config-save** | Reject unsupported harness claims early |
| OpenAPI / CLI / DTO drift for role fields on session wire | May lag store until API surface needed |
| Phase 2A switch / handoff saga | Separate workstream |

---

## Files (0042 focus)

```text
backend/internal/storage/sqlite/migrations/0042_session_role_fields.sql
backend/internal/storage/sqlite/queries/sessions.sql
backend/internal/storage/sqlite/queries/template_artifacts.sql
backend/internal/storage/sqlite/gen/{models,sessions,template_artifacts}.sql.go
backend/internal/storage/sqlite/store/session_store.go
backend/internal/storage/sqlite/store/template_artifact_store.go
backend/internal/storage/sqlite/store/store_test.go
backend/internal/session_manager/manager.go          # Store iface, persist, relaunch
backend/internal/session_manager/role_resolve.go     # dual-write + restore helpers
backend/internal/session_manager/role_resolve_test.go
backend/internal/session_manager/manager_test.go     # fake CAS
```

Plus prior Phase 1 role map / resolve / profiles (already accepted).

**Not committed** — waiting on Codex + your accept.

---

## Ask Codex

1. Accept migration 0042 durability after the two CAS fail-closed P1 fixes?
2. Next recommended: **session-scoped canSpawn credentials**, then adapter read-only — confirm before starting.
