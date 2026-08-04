# Harness capability matrix (Phase 0)

Fill by dogfood against this pin. **Unsupported → config reject**, never silent degrade.

| Harness | spawn_supported | switch_supported | limit_detection_supported | read_only_enforced | Notes |
|---------|-----------------|------------------|---------------------------|--------------------|-------|
| claude-code | TBD | TBD | TBD | TBD | First switch pair with codex |
| codex | TBD | TBD | TBD | TBD | First switch pair with claude-code |
| grok | TBD | TBD | TBD | TBD | |
| pi | TBD | TBD | TBD | TBD | Zai / Kimi test **separately** |
| pi (zai model) | TBD | TBD | TBD | TBD | |
| pi (kimi model) | TBD | TBD | TBD | TBD | |

## Gate definitions

| Capability | Meaning |
|------------|---------|
| `spawn_supported` | Auth, model id, system prompt inject, basic cancel |
| `switch_supported` | Native id / successor safe, ack, restore path |
| `limit_detection_supported` | Structured or reviewed envelope (not free-text) |
| `read_only_enforced` | Host/adapter can deny workspace writes |

## Role binding rules

- Every role requires `spawn_supported`.
- `workspaceWrites: false` requires `read_only_enforced`.
- Failover / switch targets require `switch_supported` when used on switch path.
- Auto-failover requires `limit_detection_supported`.
