# Template authority (v1 = Option A)

**Decision:** Phase 1 uses **Option A** from `REMAINING_PLAN.md` §1-E.

## Contract

1. **New sessions** load role templates only from **host-approved profile roots**:
   - `AO_ROLE_PROFILES_DIR` (explicit operator approval surface)
   - Daemon `cwd/profiles` and data-dir-relative `profiles/` walks (host process context — not the project worktree)
2. **Repo / branch templates are unsupported.** The loader never reads `.ao/roles/*` from a worktree as system authority.
3. **Deployment or setting `AO_ROLE_PROFILES_DIR` constitutes approval** of the files under that root. Changing those host files changes policy for **new** sessions without a separate re-pin UI (v1).
4. **Existing sessions** restore exclusively from the **pinned SQL template artifact** (CAS). Live profile drift does not rewrite restored prompts.

## Not Option B (yet)

Option B (approved template hashes stored in role config + explicit re-pin on drift) is deferred. Session CAS already covers restore; new-session host-root approval is the v1 bar.

## Tests / enforcement

- Path containment: template ids cannot escape roots (`..`, separators).
- Negative: no code path loads worktree `.ao/roles`.
- Restore tests: pinned artifact only.

See `backend/internal/roles/templates.go` and `session_manager/role_resolve.go` (`profileRoots`, `restoreRoleApplyResult`).
