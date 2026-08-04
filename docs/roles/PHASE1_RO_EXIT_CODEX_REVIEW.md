# Phase 1 remainder — review pack (RO + registry + roleMap + template A)

**Branch:** `roles/multi-sub-v1`
**Scope:** REMAINING_PLAN 1-A…1-E (not Phase 2)
**Ask:** Re-review after P1 Claude honesty fix. Do not start Phase 2A until accept.

## Summary

| Slice | Deliverable |
|-------|-------------|
| **1-A** | `READ_ONLY_CONTRACT.md` — paths, permitted writes, gates, Claude re-enable bar |
| **1-B** | **Codex only** claims `read_only_enforced`: `--sandbox read-only` + never full bypass (launch+restore). **Claude `read_only_enforced=false`** (`auto` is not deny-by-default) |
| **1-C** | Runtime registry; switch/limit default false; config-save + launch + restore |
| **1-D** | CLI `roleMap` mirror (no silent drop) |
| **1-E** | Template Option A (host roots only) |

## P1 close (Claude)

Review finding: Claude `PermissionModeAuto` + tool lists is **not** fail-closed; allowlist Bash/`printf`/`ao …` could mutate.

**Resolution (conservative option from review):**

- `capabilities.For(claude-code).ReadOnlyEnforced = false`
- Config + launch + restore reject Claude `workspaceWrites:false`
- `readonly.ApplyLaunch/Restore` for Claude does **not** set `ReadOnly` or tool lists
- Removed write-capable Claude allowlist entirely
- Strict example role map: **Codex** orch/reviewer
- Future re-enable bar documented: `dontAsk` + no write Bash + non-shell spawn + tests

## Accepted from prior review (unchanged)

- Codex sandbox RO
- Capability gates (config/launch/restore)
- CLI roleMap
- Template Option A
- switch/limit out of scope

## Tests

```text
go test ./internal/roles/... ./internal/session_manager/ \
  ./internal/service/project/ ./internal/cli/ \
  ./internal/adapters/agent/codex/ -count=1
# all ok
```

## Accept criteria (updated)

- [ ] Claude `read_only_enforced=false` honest; WW=false rejected for Claude
- [ ] Codex RO launch/restore only production true cell
- [ ] Registry non-circular
- [ ] CLI roleMap + Option A
- [ ] Docs (contract, matrix, remaining plan, example map) consistent
