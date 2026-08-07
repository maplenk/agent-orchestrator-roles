# Sync 2 — Step 5 live records

Date: 2026-08-07. Branch `roles/upstream-sync-2` (code checkpoint `baa0599c`),
isolated daemon on `AO_DATA_DIR=~/.ao/sync2/data`, port 3201, tmux server
`-L ao-e3502562da04`. The real desktop app on 3001 was never touched.

These are the two records Step 5 was missing. The switch and input-fencing
halves were captured earlier; see `UPSTREAM_SYNC2_PLAN.md`.

## Record 1 — orchestrator fresh conversation (2B-1) on the merged tree

`s2-7` spawned into the strict role map and auto-bound the orchestrator role:
`role=orchestrator, workspaceWrites=0, canSpawn=1`, template artifact pinned.

`POST /sessions/s2-7/fresh-conversation` returned
`kind=orchestrator_fresh_conversation` — the ledger kind migration **9006**
exists to admit, which is worth stating because that migration is one of the
eight the renumber moved.

```
gen=18b95105  orchestrator_fresh_conversation  requested
gen=18b95105  orchestrator_fresh_conversation  pre_stop
gen=18b95105  orchestrator_fresh_conversation  post_stop
gen=18b95105  orchestrator_fresh_conversation  target_ack
```

Afterwards: same session id, same harness (`codex`), `switch_pending` cleared,
not terminated, and the role pin intact — `role=orchestrator`,
`workspaceWrites=0`, `canSpawn=1`, same template artifact. In place, as 2B-1
requires.

## Record 2 — a genuine `post_stop` without `target_ack`, then recovery

The specimen had to be manufactured, because a failed switch is not one. The
earlier candidate `s2-4` failed at `pre_stop`: its source was never stopped, so
there is nothing to re-drive and `RecoverSwitchFromPostStop` correctly reports
`ErrSwitchNothingToRecover`. **A `failed` ledger row is not recovery evidence.**

What produces a real one is a target launch that fails *after* the source
stops. Project `agentRules` was set to 18 KiB, which inflates every worker
system prompt; both harnesses inline that prompt into argv, and the tmux
launch-command preflight then refuses the TARGET launch — inside
`finishSwitchTarget`, which runs after `ensurePostStopLedger`.

`POST /sessions/s2-5/switch {targetHarness: codex}` returned
`409 SWITCH_POST_STOP` — "Source stopped but target switch did not complete;
handoff retained for recovery".

State before recovery, which is what a valid specimen must show:

| Fact | Value |
|---|---|
| Ledger for gen `99e7780d` | `requested → pre_stop → post_stop → failed` |
| `target_ack` for that generation | **absent** |
| `switch_pending.generationId` | `99e7780d…` — matches the ledger generation |
| Durable payload | present (`payloadJson`) |
| `sourceRuntimeHandleId` | `s2-5` (retained) |
| Session `runtime_handle_id` | cleared — the source really stopped |
| `is_terminated` | false |

The 18 KiB rule was then removed and the daemon restarted. Recovery is
boot-driven; there is no HTTP route.

Result:

- It **reused generation `99e7780d`** rather than minting a new one — still
  two distinct generations on this session, not three.
- Exactly **one** `target_ack` row appended, for that generation.
- Exactly **one** target runtime on the isolated tmux server (`s2-5`).
- `switch_pending` cleared; harness is now `codex`; activity idle.

## Also observed

Boot recovery logged `post_stop recovery failed, skipping` for `plain-1` and
`s2-3` with `switch is not supported for chat sessions`. That is the Step 5
chat refusal firing on the recovery path, on rows that predate it — the
refusal reached from `RecoverSwitchFromPostStop`, not just the interactive
entry points, and it skipped rather than wedging the boot.
