# Harness capability matrix (mirror of runtime registry)

**Source of truth:** `backend/internal/roles/capabilities` (`capabilities.For`).
This file is documentation only.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| codex | true | **true** | false | **true** | RO sandbox; switch promoted after Phase 2A close-out |
| claude-code | true | **true** | false | **false** | switch promoted after Phase 2A close-out; RO deferred |
| pi | true | false | false | **false** | |
| muse | true | false | false | **false** | spawn proven (argv + developer-prompt env + managed hooks); no write-denial flag |
| other AllHarnesses | true | false | false | false | |
| fake (tests) | true | true | false | true | Test-only |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Basic spawn path |
| `switch_supported` | Worker switch/fresh saga with generation ownership + recovery + input gate; Claude/Codex promoted after Phase 2A dogfood |
| `limit_detection_supported` | Phase 3 |
| `read_only_enforced` | OS/sandbox workspace write denial |

Validated at config-save, launch, restore, and switch.

At config-save, once any production `switch_supported` cell is on, `switch_supported`
is required on **both** sides of a failover ladder:

| Position | Requirement |
|----------|-------------|
| Failover rung | `switch_supported` — it is a switch *target* |
| Primary binding of a role with a **non-empty** ladder | `switch_supported` — it is the switch *source* |
| Primary binding of a role with **no** ladder | spawn/RO only; a switch-incapable harness (Pi) stays valid as spawn-only |

A ladder whose owning primary cannot originate a switch is rejected at config-save
rather than deferring to `ErrSwitchNotSupported` at runtime (DoD invariant 9: no
silent degrade).
