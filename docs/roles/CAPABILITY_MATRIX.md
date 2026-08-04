# Harness capability matrix (mirror of runtime registry)

**Source of truth:** `backend/internal/roles/capabilities` (`capabilities.For`).
This file is documentation only — config/launch/restore must not parse this markdown.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| codex | true | **false** | **false** | **true** | `--sandbox read-only` on launch+restore |
| claude-code | true | false | false | **false** | `auto` is not deny-by-default; dontAsk path deferred |
| pi | true | false | false | **false** | No permission/sandbox flags |
| pi (zai/kimi) | true | false | false | **false** | Same as pi |
| other AllHarnesses | true | false | false | false | See registry |
| fake (tests) | true | false | false | true | Test-only |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Auth, model id, system prompt inject, basic cancel |
| `switch_supported` | Promote in Phase 2 after tests |
| `limit_detection_supported` | Promote in Phase 3 after tests |
| `read_only_enforced` | Host/adapter **actually** denies workspace writes (OS sandbox or fail-closed tool mode) |

## Role binding rules

- Every role requires `spawn_supported`.
- `workspaceWrites: false` requires `read_only_enforced` (config-save + launch + restore).
- Phase 1 RO roles (orch/reviewer) must bind **codex** (or another true-RO harness when added).

Validated at: **config-save**, **launch** (`applyRoleMap`), **restore** (pinned WW=false).
