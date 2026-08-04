# Phase 2A dogfood evidence

**Reviewed code SHA (checkpoint):** `2d19ad59d97d00b904e97e6cde85d9293831295e`  
**Branch:** `roles/multi-sub-v1`  
**Remote:** `origin/roles/multi-sub-v1` @ `2d19ad59` (pushed before dogfood)  
**Date (UTC+5:30):** 2026-08-04  

## Production capability stance (historical at this checkpoint)

At `2d19ad59` (manager dogfood):

| Harness | `SwitchSupported` | Evidence |
|---------|-------------------|----------|
| `claude-code` | **false** (then) | `capabilities.For` + `TestDogfood…/0` + `capabilities_test` |
| `codex` | **false** (then) | same |
| Dogfood exercise path | `switchCapsOverride` **only** | never flipped production registry in this run |

**Later:** after live dogfood + close-out accept, Claude/Codex were promoted in a **separate** CL. Re-run dogfood post-promote uses production registry (no override); item 0 asserts `SwitchSupported=true`.

## Scope of this dogfood run

| Layer | Status |
|-------|--------|
| Manager saga (SwitchWorker / Fresh / Recover) | **Executed** via `TestDogfood_Phase2AChecklist` |
| Terminal `AllowTerminalInput` + mux Write suppress | **Executed** (manager fence + `TestServeWriteSuppressedWhenInputGateBlocks`) |
| Lifecycle generation = runtime launch id | **Executed** (ledger + `RuntimeLaunchID` equality) |
| Production refuse without override | **Executed** |
| Live Claude/Codex binaries in desktop | **Recorded later** — see `PHASE2A_LIVE_DOGFOOD.md` (API/CLI landed @ `83f7abfb`+) |

Manager-level dogfood is the recoverable evidence bound to `2d19ad59`. Live agent process dogfood is recorded separately.

## How to re-run

```bash
cd backend
git rev-parse HEAD   # expect dogfood harness commit after 2d19ad59, or 2d19ad59 + local harness
AO_DOGFOOD_EVIDENCE_DIR=/tmp/ao-dogfood-evidence \
  go test ./internal/session_manager/ -run 'TestDogfood_Phase2AChecklist' -count=1 -v
go test ./internal/terminal/ -run 'TestServeWriteSuppressedWhenInputGateBlocks' -count=1
go test ./internal/roles/capabilities/ -count=1
```

## Checklist results

| # | Item | Result | Generation / ledger evidence |
|---|------|--------|------------------------------|
| 0 | Production `switch_supported=false`; SwitchWorker refuses without override | **PASS** | `ErrSwitchNotSupported` with no override |
| 1 | Claude → Codex worker switch | **PASS** | `gen=gen-c2x-1` = `RuntimeLaunchID`; phases `[requested, pre_stop, post_stop, target_ack]`; stable ids `dog-c2x:gen-c2x-1:{phase}`; cross-harness model cleared (`model=""`) |
| 2 | Codex → Claude reverse | **PASS** | `gen=gen-x2c-1`; same phase order; pending cleared; source model not leaked |
| 3 | Same-harness FreshConversation (no handoff stack) | **PASS** | `gen=gen-fresh-2`; `handoff_count=1`; `kind=fresh_conversation`; original task retained |
| 4 | Crash mid-switch (post_stop before launch/ack) → recover | **PASS** | Reused `gen-crash-1` for ledger **and** runtime; `create=1`; phases include `target_ack`; second recover does not double-launch |
| 4b | Crash recovery: live wrong-gen → uncertain | **PASS** | `ErrSwitchUncertain`; `create=0` |
| 5 | Confirmed-alive pre-stop → rollback usable source | **PASS** | Pending cleared; prompt/handle/agent/launch restored; terminal allow on handle |
| 5b | Rollback persist fail → uncertain | **PASS** | `ErrSwitchUncertain`; pending retained |
| 6 | Terminal fence (session id, runtime handle, pending source handle) | **PASS** | `ErrSwitchInProgress` on all three keys; shell terminal allowed |
| 6b | Terminal mux Write suppressed under InputGate | **PASS** | `TestServeWriteSuppressedWhenInputGateBlocks` — PTY write empty; error frame `input blocked` |
| 7 | post_stop append fail blocks launch/ack | **PASS** | `ErrSwitchPostStop`; `create=0`; no `target_ack` |

### Raw evidence dump (manager checklist)

```
=== Phase 2A dogfood checklist (manager-level) ===
note: production SwitchSupported remains false for claude-code and codex
override: switchCapsOverride=testSwitchCaps (dogfood only)
PASS 0: production SwitchSupported=false; SwitchWorker refuses without override
PASS 1: claude→codex gen=gen-c2x-1 runtime=gen-c2x-1 phases=[requested pre_stop post_stop target_ack] ids=[dog-c2x:gen-c2x-1:requested dog-c2x:gen-c2x-1:pre_stop dog-c2x:gen-c2x-1:post_stop dog-c2x:gen-c2x-1:target_ack] model=""
PASS 2: codex→claude gen=gen-x2c-1 phases=[requested pre_stop post_stop target_ack] pending=false
PASS 3: fresh×2 gen=gen-fresh-2 handoff_count=1 kind=fresh_conversation
PASS 4: crash recovery gen=gen-crash-1 runtime=gen-crash-1 create=1 phases=[requested pre_stop post_stop target_ack] no_double_launch
PASS 4b: stale-alive wrong-gen → ErrSwitchUncertain create=0
PASS 5: confirmed-alive → rollback pending; source usable handle=rt-1
PASS 5b: rollback persist fail → ErrSwitchUncertain pending_retained=true
PASS 6: terminal fence session_id + runtime_handle + pending_source_handle; shell allowed
PASS 7: post_stop append fail → ErrSwitchPostStop create=0 no_ack
```

## Generation / ownership invariants verified

1. **Single generation:** ledger `GenerationID` == target `RuntimeLaunchID` (ForceLaunchID).
2. **Stable ledger ids:** `{session}:{gen}:{phase}` (requested / pre_stop / post_stop / target_ack).
3. **Promote after ack only:** harness remains source until durable `target_ack` (covered by existing unit tests + dogfood paths clearing pending only post-ack).
4. **Recovery:** post_stop-present incomplete saga launches once with pending gen; wrong-gen live → `ErrSwitchUncertain` (no second target).
5. **Input ownership:** pending fences terminal by session id, live handle, and pending source handle.

## Explicit non-claims (at this checkpoint)

- Did **not** run interactive Claude Code or Codex CLI agent processes end-to-end in the desktop app in this manager run (live pass recorded later).
- Did **not** promote `switch_supported` in this commit (promotion is a later separate CL).
- Did **not** expose Service/HTTP/CLI switch APIs yet at `2d19ad59` (landed later @ `83f7abfb`).

## Path to promotion (completed after this document)

1. ~~Accept manager-level dogfood results above~~.
2. ~~Land Service/API/CLI with host role-map authorized targets~~ @ `83f7abfb`.
3. ~~Live desktop dogfood~~ — `PHASE2A_LIVE_DOGFOOD.md` (footer + clean crash after lease).
4. ~~Flip `SwitchSupported` for Claude/Codex~~ in dedicated promote CL after close-out accept.
