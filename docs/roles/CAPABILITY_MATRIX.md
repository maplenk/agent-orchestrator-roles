# Harness capability matrix (mirror of runtime registry)

**Source of truth:** `backend/internal/roles/capabilities` (`capabilities.For`).
This file is documentation only.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| codex | true | **false** | false | **true** | RO sandbox; switch after Phase 2A dogfood |
| claude-code | true | **false** | false | **false** | switch after dogfood; RO deferred |
| pi | true | false | false | **false** | |
| other AllHarnesses | true | false | false | false | |
| fake (tests) | true | true | false | true | Test-only |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Basic spawn path |
| `switch_supported` | Worker switch/fresh saga with generation ownership + recovery + input gate; promote only after dogfood |
| `limit_detection_supported` | Phase 3 |
| `read_only_enforced` | OS/sandbox workspace write denial |

Validated at config-save (spawn/RO), launch, restore, and switch.
