# Read-only workspace contract (executable)

**Status:** Phase 1 binding contract for `workspaceWrites:false` roles.
**Code:** `backend/internal/roles/capabilities`, `roles/readonly`, adapter launch paths.
**Non-goal:** Prompt-only “please don’t write” policy. That is never sufficient for `read_only_enforced`.

---

## 1. Protected paths (must not be mutated)

Under the session **workspace / worktree root**:

| Class | Examples |
|-------|----------|
| Worktree contents | Tracked and untracked project files |
| Git directory | `.git/**`, index, refs, objects, HEAD |
| Repository metadata | `.gitmodules`, git worktree links that alter repo state |

A read-only launch must fail closed if the agent can create, edit, delete, or rename under these paths, or mutate Git state for this worktree.

## 2. Permitted writes (host / AO state)

| Class | Examples |
|-------|----------|
| AO data dir | Session rows, runfiles, template CAS under `AO_DATA_DIR` / daemon data |
| Logs | Agent/daemon logs outside the worktree |
| Temporary storage | OS temp, AO-managed temp **outside** the protected worktree |
| Credentialed host APIs | `ao spawn` / session APIs via managed session capability (not a general shell) |

The agent may still **read** the worktree freely.

## 3. Enforcement mechanisms (v1)

| Harness | Enforcement | `read_only_enforced` |
|---------|-------------|----------------------|
| **codex** | `--sandbox read-only` and **never** `--dangerously-bypass-approvals-and-sandbox` on launch **and** restore | **true** |
| **claude-code** | *Not claimed.* Claude `auto` is classifier-based auto-approval, not deny-by-default. Tool allow/deny under `auto` leaves unlisted write-capable Bash eligible. | **false** until `dontAsk` + no write-capable Bash + non-shell spawn (or OS sandbox) |
| **pi** (incl. Zai/Kimi models) | No permission/sandbox flags | **false** |
| Other harnesses | Unimplemented | **false** |

### Claude path to re-enable (future)

All of the following must land before `read_only_enforced=true` for Claude:

1. Permission mode **`dontAsk`** (deny tools unless pre-approved via allow rules) — not `auto`.
2. **No write-capable Bash** on the allowlist (no `printf`, no broad `ao …` prefixes that can mutate host state).
3. Spawn for `canSpawn` roles via a **dedicated non-shell** tool/capability (not Bash argument patterns).
4. Tests: unlisted Bash denied on launch and restore; allowed spawn path cannot redirect or invoke other AO mutations.
5. Then promote the registry cell.

## 4. Negative runtime tests (required)

| Harness | Required proof |
|---------|----------------|
| **Codex** | Launch/restore argv carries `--sandbox read-only` and never full bypass; capability gates at config/launch/restore |
| **Claude** | Config + launch reject `workspaceWrites:false` while `read_only_enforced=false` |
| **Pi / others** | Same reject |

Full agent binary FS/Git write negation under Codex sandbox is best-effort outside CI when binary present.

## 5. Orchestrator + `ao spawn` without write-capable shell

A strict orchestrator has `workspaceWrites:false` and `canSpawn:true`.

| Allowed | How |
|---------|-----|
| Invoke `ao spawn` / session APIs | Session-scoped spawn capability — **application-level** |
| Worktree writes | **Denied** by Codex OS sandbox |
| General write shell | **Denied** by sandbox (Codex) |

Phase 1 **RO orchestrator must use Codex** (or any future harness with true enforcement). Claude is rejected for `workspaceWrites:false`.

## 6. When validated

| Gate | Check |
|------|--------|
| Config save | RoleMap roles with `workspaceWrites:false` require `read_only_enforced` |
| Launch (`applyRoleMap`) | Same capability check before spawn |
| Restore | Same capability check when pinned policy has `workspaceWrites:false`; RO launch config re-applied for enforced harnesses |

## 7. Honesty residuals

- Same-UID residual risk remains (agent clearing markers and reading runfiles).
- **Codex:** OS sandbox residual depends on binary version/platform; network/side effects outside FS are out of scope.
- **Claude:** deliberately **not** RO-enforced in v1 registry.
- Pi remains not RO-capable until an external sandbox is wired.
- `CAPABILITY_MATRIX.md` is documentation; the **runtime registry** is source of truth.
