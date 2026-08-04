# Phase 2A live dogfood evidence

## Prompt fix + clean crash rerun (this revision)

**Production code SHA (prompt fix):** `a3bc32be6aabcb10334d8dd15579a144fbd811fe`  
**Branch:** `roles/multi-sub-v1`  
**Date (UTC):** 2026-08-04  

### P1 fix: target-authoritative switch prompt

**Commit:** `a3bc32be` — `relaunchSession` builds an **ephemeral** role copy with `ResolvedHarness` / `ResolvedModel` set to the pending target for system prompt + agent config only. Durable session identity stays on the source until `target_ack`.

**Unit tests (always-on):**

- `TestSwitchWorker_SystemPromptTargetHarnessFooter` (Claude → Codex)
- `TestSwitchWorker_SystemPromptTargetHarnessFooter_CodexToClaude`
- `TestRecover_SystemPromptTargetHarnessFooter`

### Production caps (at time of live dogfood)

Were **false** in `capabilities.For` during the live runs below. Enablement used non-committable local patch + `AO_DOGFOOD_SWITCH=1` only. **Promoted** in a separate CL after this close-out was accepted (see Promotion gate).

| Field | Value |
|-------|--------|
| Local patch sha256 | `3625fbf3658b6d1f4b39c1d9f03c1f51b8389bb90bb288b03390dd5defb2d7ab` |
| Tree after dogfood | `git checkout -- switch.go` → clean at `a3bc32be` |
| Isolated env | `/tmp/ao-dogfood-2a-a3bc32be` port `3016` |

### Live ordinary switch (footer fix)

| Check | Result |
|-------|--------|
| API | Claude → Codex `generationId=b1324388-7189-4251-a5b3-bea0dfbf5663` |
| Process executable | `/Users/tagtaste/.local/bin/codex` |
| Launch id in process | `b1324388-7189-4251-a5b3-bea0dfbf5663` |
| Authoritative footer | **`Active role: implementor. Harness: codex.`** |

### Clean crash recovery protocol (offline inject)

Designed to remove the prior “live handle at inject” ambiguity:

1. Live Claude source; **tmux kill** → **confirmed DEAD**
2. **Daemon SIGKILL** → port free (**daemon offline**)
3. Inject only while offline: pending + ledger phases `requested`, `pre_stop`, `post_stop` for gen `offline-crash-gen-1` (no failed/ack)
4. **Single** daemon start → one Reconcile recovery attempt
5. Inspect process + footer + ledger

| Check | Result |
|-------|--------|
| Source dead before inject | **Yes** |
| Inject while daemon offline | **Yes** |
| Restart count | **1** |
| Session after recover | `harness=codex`, `runtime_launch_id=offline-crash-gen-1`, pending length **0** |
| Process executable | **codex** |
| Launch id | **offline-crash-gen-1** |
| Authoritative footer | **`Active role: implementor. Harness: codex.`** |
| Daemon log `recovery failed` | **None** |

### Residual `failed`→`target_ack` — root cause and fix

**Root cause (review):** concurrent daemons could both pass the runfile check; one bound the configured port, the other bound an **ephemeral** port (`server.go` fallback), and **both reconciled the same SQLite store**. One launch collides (`failed`); the other succeeds (`target_ack`). Reproduced with two processes both reaching `daemon listening` on different ports against one data dir.

**Fix:** `datadirlock` exclusive lease on `AO_DATA_DIR/daemon.lock`, acquired in `daemon.Run` **before** store open / reconcile. Second start exits with `ErrLocked`. Ephemeral port fallback remains only for the lease holder when a non-AO process owns the configured port.

**Re-dogfood after lease (gen `lease-crash-gen-1`):**

```text
requested → pre_stop → post_stop → target_ack
```

- **No `failed` row**
- Session: `harness=codex`, `runtime_launch_id=lease-crash-gen-1`, pending cleared
- Dual-start smoke: one process acquires lease; peer exits `data directory already owned`

**Close-out accepted.** The lease re-dogfood closes the unexplained `failed`→`target_ack` provenance condition. Production `SwitchSupported` for Claude/Codex is flipped in a **separate** promotion commit (this land).

---

## Earlier manager-level + first live pass (historical)

**Manager dogfood:** `PHASE2A_DOGFOOD.md` @ `2d19ad59`  
**First live pass (accepted portions only):** base `83f7abfb`, docs commit `b5fe6b15`

Accepted from first live pass (still valid): docs-only evidence commit shape, patch-hash discipline, forward/reverse/fresh API, stable ledger IDs, input fence. (Production caps were false during that pass; now promoted.)

**Superseded for promotion gate:** stale source footer on target; crash inject with live source handle; dual-daemon failed→ack provenance (fixed by `datadirlock`).

## Promotion gate — **closed / accepted**

Conditions required before promoting `SwitchSupported` — all met:

1. Target-authoritative prompt fix accepted (**landed** `a3bc32be` + live footer evidence).
2. Crash-recovery ledger provenance accepted **without** unexplained `failed`→`target_ack` (**closed** — `lease-crash-gen-1` clean sequence after `datadirlock`).
3. Explicit accept of Phase 2A close-out (lease + concurrent ownership + clean crash) — **accepted**.

**Promotion:** Claude/Codex `SwitchSupported=true` in `capabilities.For` (this commit). `limit_detection_supported` remains false.
