# Phase 2A live dogfood evidence (Step 2)

**Reviewed production code SHA:** `83f7abfb2b4a32b614c2a9df14197e5cbea1d289`  
**Branch:** `roles/multi-sub-v1` (aligned with origin)  
**Date (UTC):** 2026-08-04  

## Production capability stance (unchanged)

| Harness | `SwitchSupported` in `capabilities.For` |
|---------|----------------------------------------|
| `claude-code` | **false** (verified `TestFor_Phase1Cells` after dogfood) |
| `codex` | **false** |

**No production promotion in this run.** Capability enablement was **local-only and non-committable**.

## Local-only enablement (not committed)

| Field | Value |
|-------|--------|
| Mechanism | Uncommitted patch to `session_manager.switchCaps` |
| Env gate | `AO_DOGFOOD_SWITCH=1` forces `SwitchSupported=true` for Claude/Codex/Fake **only in the dogfood build** |
| Patch sha256 | `212f72d7eb4bf6a412f9dacc13b79ea0b37624fb22dbe96a681f00dd4a4f8c29` |
| Patch artifact | `/tmp/ao-dogfood-2a-83f7abfb/evidence/local-dogfood-enable.patch` |
| Tree after run | `git checkout -- backend/internal/session_manager/switch.go` → clean at `83f7abfb` |

`capabilities.For` was **never** modified. Without `AO_DOGFOOD_SWITCH=1`, production registry stays false.

## Isolated environment

| Setting | Value |
|---------|--------|
| `AO_DATA_DIR` | `/tmp/ao-dogfood-2a-83f7abfb/data` |
| `AO_RUN_FILE` | `/tmp/ao-dogfood-2a-83f7abfb/run/running.json` |
| `AO_PORT` | `3015` |
| `AO_ROLE_PROFILES_DIR` | repo `profiles/` |
| Workspace | `/tmp/ao-dogfood-2a-83f7abfb/workspace` (local git, `main`) |
| Project | `dogfood` with roleMap: implementor→claude-code, failover codex + claude-code |
| Binaries | `/tmp/ao-dogfood-2a-83f7abfb/bin/{ao,ao-daemon}` built from base SHA + local patch |

## Live checklist results

| # | Item | Result | Evidence |
|---|------|--------|----------|
| 0 | Production caps false at base SHA | **PASS** | `TestFor_Phase1Cells`; tree restored clean |
| 1 | Claude → Codex live switch | **PASS** | gen `5ec94137-eb1a-48fc-95a2-cc8fefb58623` = `runtime_launch_id`; harness→codex; role pin `implementor` retained |
| 2 | Codex → Claude reverse | **PASS** | gen `22ee55dc-a754-4576-ab1b-d2df1dc716ea`; harness→claude-code |
| 3 | Fresh conversation ×2 (no handoff stack) | **PASS** | gens `17e8cc1b-…`, `6a758b7c-…`; SQLite handoff_count=**1** |
| 4 | Crash recovery (post_stop incomplete → reboot) | **PASS** | Installed pending+post_stop gen `crash-recover-gen-1`, SIGKILL daemon, restart with same env; after Reconcile: harness=codex, `runtime_launch_id=crash-recover-gen-1`, pending cleared, `target_ack` present |
| 5 | SQLite ledger order | **PASS** | Each gen: `requested → pre_stop → post_stop → target_ack` (stable ids `{session}:{gen}:{phase}`) |
| 6 | Terminal / input fence | **PASS** | With artificial pending JSON: send → `SWITCH_IN_PROGRESS`; after clear pending: send → 200 ok |

### Generation / ledger excerpts (session `dogfood-1`)

**Claude → Codex**

```
dogfood-1:5ec94137-…:requested  requested  claude-code → codex
dogfood-1:5ec94137-…:pre_stop   pre_stop
dogfood-1:5ec94137-…:post_stop  post_stop
dogfood-1:5ec94137-…:target_ack target_ack
```

Session after ack: `harness=codex`, `runtime_launch_id=5ec94137-eb1a-48fc-95a2-cc8fefb58623`, `role_id=implementor`, pending empty.

**Codex → Claude**

```
dogfood-1:22ee55dc-…:requested … codex → claude-code
… pre_stop / post_stop / target_ack
```

**Fresh ×2**

```
dogfood-1:17e8cc1b-…:… claude-code → claude-code (fresh_conversation)
dogfood-1:6a758b7c-…:… claude-code → claude-code (fresh_conversation)
handoff_count=1
```

**Crash recovery**

```
pre-reboot: pending gen=crash-recover-gen-1, handle cleared, harness still claude-code
post-reboot: harness=codex, runtime_launch_id=crash-recover-gen-1, pending length=0
ledger: post_stop + target_ack for crash-recover-gen-1
note: a transient failed phase was also recorded before successful target_ack (launch noise); final state is ack + live codex
```

**Input fence**

```json
// send while switch_pending_json set:
{"error":"conflict","code":"SWITCH_IN_PROGRESS","message":"A worker switch is already in progress for this session"}
// send after clear:
{"ok":true,"sessionId":"dogfood-1","message":"fence cleared"}
```

## Artifacts (local host paths)

All under `/tmp/ao-dogfood-2a-83f7abfb/evidence/`:

- `META.txt`, `local-dogfood-enable.patch`
- `switch_c2x.json`, `switch_x2c.json`, `fresh1.json`, `fresh2.json`
- `ledger_after_c2x.txt`, `ledger_after_x2c.txt`, `ledger_full.txt`, `ledger_crash_recover.txt`
- `send_during_pending.json`, `send_after_clear.json`
- `daemon.log`, `daemon_recover.log`

SQLite DB: `/tmp/ao-dogfood-2a-83f7abfb/data/ao.db`

## Explicit non-claims

- Did **not** promote `SwitchSupported` in production source.
- Did **not** commit the dogfood enablement patch.
- Desktop Electron UI was not used; dogfood was CLI + isolated daemon on port 3015.
- Crash recovery was **post_stop-incomplete** inject + hard kill + Reconcile (not a random mid-instruction kill during live destroy).

## Gate for Step 4 (promotion)

Only after this live log is accepted, promote `SwitchSupported` for Claude/Codex in a **separate** final commit with:

1. Registry flip in `capabilities.For`
2. Failover `switch_supported` config-save enforcement activates via `switchSupportedPromoted()`
3. Remove any reliance on `AO_DOGFOOD_SWITCH`
