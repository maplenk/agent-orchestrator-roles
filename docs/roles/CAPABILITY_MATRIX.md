# Harness capability matrix (mirror of runtime registry)

**Source of truth:** `backend/internal/roles/capabilities` (`capabilities.For`).
This file is documentation only.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| codex | true | **true** | false | **true** | RO sandbox; switch promoted after Phase 2A close-out |
| claude-code | true | **true** | false | **false** | switch promoted after Phase 2A close-out; RO deferred |
| pi | true | false | false | **false** | |
| other AllHarnesses | true | false | false | false | |
| fake (tests) | true | true | false | true | Test-only |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Basic spawn path |
| `switch_supported` | Worker switch/fresh saga with generation ownership + recovery + input gate; Claude/Codex promoted after Phase 2A dogfood |
| `limit_detection_supported` | Phase 3 |
| `read_only_enforced` | OS/sandbox workspace write denial |

Validated at config-save (spawn/RO; failover rungs also require `switch_supported` once any production cell is on), launch, restore, and switch.
