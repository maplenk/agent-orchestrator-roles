# Harness capability matrix (mirror of runtime registry)

**Source of truth:** `backend/internal/roles/capabilities` (`capabilities.For`).
This file is documentation only — config/launch/restore must not parse this markdown.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| codex | true | **true** | **false** | **true** | RO sandbox; switch pair with claude-code |
| claude-code | true | **true** | false | **false** | switch pair with codex; RO deferred |
| pi | true | false | false | **false** | No permission/sandbox flags |
| pi (zai/kimi) | true | false | false | **false** | Same as pi |
| other AllHarnesses | true | false | false | false | See registry |
| fake (tests) | true | true | false | true | Test-only |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Auth, model id, system prompt inject, basic cancel |
| `switch_supported` | Worker switch / fresh conversation saga (Claude↔Codex initial matrix) |
| `limit_detection_supported` | Promote in Phase 3 after tests |
| `read_only_enforced` | Host/adapter **actually** denies workspace writes |

## Role binding rules

- Every role requires `spawn_supported`.
- `workspaceWrites: false` requires `read_only_enforced` (Codex only in Phase 1).
- Switch/fresh targets require `switch_supported` on source and target.

Validated at: **config-save** (spawn/RO), **launch**, **restore**, **switch** (switch_supported).
