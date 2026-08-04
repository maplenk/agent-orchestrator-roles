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

### Production caps

Still **false** in `capabilities.For`. Live enablement used non-committable local patch + `AO_DOGFOOD_SWITCH=1` only.

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

### Residual open: ledger still shows `failed` before `target_ack`

Even with offline inject + single restart, SQLite still records:

```text
requested → pre_stop → post_stop → failed → target_ack
```

for `offline-crash-gen-1`, with **~5 ms** between Go-written `failed` and `target_ack`, and **no** `reconcile: post_stop recovery failed` log line.

Honest status:

- Final runtime state is **correct** (one codex process, matching gen, target footer, pending clear).
- The intermediate **`failed` row provenance is not fully explained** by this run: it is **not** claimed as intentional “fail then retry”, and it is **not** dismissed as “launch noise”.
- Likely needs a code-path audit of who appends `LifecyclePhaseFailed` during a Recover that ultimately returns success (or a silent double-entry race), **before** treating crash-ledger provenance as closed for promotion.

Artifacts: `/tmp/ao-dogfood-2a-a3bc32be/evidence/` (`v2_offline_ledger.txt`, `crash_E_footer_exact.txt`, `SUMMARY.txt`, `daemon_offline_recover.log`).

---

## Earlier manager-level + first live pass (historical)

**Manager dogfood:** `PHASE2A_DOGFOOD.md` @ `2d19ad59`  
**First live pass (accepted portions only):** base `83f7abfb`, docs commit `b5fe6b15`

Accepted from first live pass (still valid): docs-only evidence commit shape, patch-hash discipline, production caps false, forward/reverse/fresh API, stable ledger IDs, input fence.

**Superseded for promotion gate:** stale source footer on target; crash inject with live source handle; incomplete failed→ack provenance.

## Promotion gate (unchanged)

Do **not** promote `SwitchSupported` until:

1. Target-authoritative prompt fix is accepted (**landed** `a3bc32be` + live footer evidence).
2. Crash-recovery ledger provenance is accepted **without** unexplained `failed`→`target_ack` (still open).
