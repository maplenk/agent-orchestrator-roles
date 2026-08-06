# Upstream sync — fresh-data dogfood

**Gate, not polish.** The renumber can reintroduce exactly the class 2B-1 hit
(schema present in Go, rejected by SQLite), and the merge touched the boot
chain, the ownership resolver and the session row shape.

| | |
|---|---|
| SHA under test | `7e388bf2` (merge `3215a364` + the defect fix below) |
| Data dir | `~/.ao/sync-dogfood/data` (created empty) |
| Daemon port | `127.0.0.1:3011` |
| Project | `syncproj` — an isolated clone, **strict delegation** |
| Preserved | `~/.ao/dev/data/ao.db` untouched, md5 `3240349d…`; its tmux sessions left running |

## 1. Migration history

```
goose versions: 53 rows, 1 … 60
tail:           52 53 54 55 56 57 58 59 60
```

Upstream through `0052` then roles `0053–0060`, in order. Both sides' columns
landed on `sessions`:

```
upstream: reviewer_harness TEXT   is_pinned BOOLEAN   pinned_at DATETIME
roles:    role_id TEXT   spawn_capability_hash TEXT   switch_pending_json TEXT   pause_json TEXT
upstream tables: agent_model_catalog, usage_sources, usage_bindings, model_usage_events
```

Boot ERROR lines: **0**.

## 2. Strict role-pinned worker switch, both directions

`implementor` is pinned to `claude-code` with a `codex` failover rung.

| | harness | launch id | spawn capability | role |
|---|---|---|---|---|
| start | `claude-code` | `a49332bf` | `e18f6bfc` | implementor |
| → codex | `codex` | `9c13056b` | `602dc1db` | implementor |
| → claude | `claude-code` | `533f4811` | `3e38904c` | implementor |

Role pin preserved across both; launch id **and** spawn capability rotate every
time, so a killed generation's token never survives.

```
switch / requested  / 9c13056b  claude-code->codex
switch / pre_stop   / 9c13056b
switch / post_stop  / 9c13056b
switch / target_ack / 9c13056b
switch / requested  / 533f4811  codex->claude-code
switch / pre_stop   / 533f4811
switch / post_stop  / 533f4811
switch / target_ack / 533f4811
switch_pending = empty      failed phases = 0
```

## 3. Orchestrator fresh conversation

Stable across the refresh: session id `syncproj-1`, `role_id=orchestrator`,
`template_artifact_id=sha256:65a9829d…`, branch `ao/syncproj-orchestrator`,
workspace path. Rotated: launch id `ba86795b → f06d1674`, spawn capability
`522e9f78 → 9a00f04d`.

Run twice. The prompt does **not** accumulate:

```
after refresh #1:  handoff sections: 1   fleet sections: 1   1555 bytes
after refresh #2:  handoff sections: 1   fleet sections: 1   1555 bytes
```

Fleet is host-derived, not recalled:

```
### Observed fleet (host; authoritative over any recollection of workers)
- Project: `syncproj`
- Workers: 1 live, 0 terminated
```

## 4. Pause authorization and the incident contract

```
operator pause                 -> 200   incident=dogfood-inc-1 reason=operator detectedBy=operator
agent principal + capability   -> 403   PAUSE_AGENT_FORBIDDEN
headerless                     -> 403   PAUSE_AUTH_REQUIRED
resume, STALE incident         -> 409   PAUSE_INCIDENT_MISMATCH   (pin still held)
resume, correct incident       -> 200   pin CLEARED
ledger: pause / dogfood-inc-1  then  resume / dogfood-inc-1
```

## 5. Daemon restart while paused — zero automatic restart

The paused worker's runtime was killed to make boot **prove** it dead, then the
daemon was restarted.

```
before:  paused=dogfood-inc-1  activity=exited  launch=7548dc4a  tmux alive
kill tmux; restart daemon
after:   paused=dogfood-inc-1  launch=7548dc4a  (unchanged)
         restore markers: 0        tmux: still gone — no automatic restart
         boot ERROR lines: 0
         "skipping automatic action for a paused session" x2:
             action="post_stop switch recovery"
             action="save-and-teardown of a dead runtime"
```

Nothing was relaunched, nothing was sent, the pin survived, and no restore
marker was minted. **MASTER_PLAN §7 rule 2 holds.**

## 6. Upstream fields survive the round trip

```
POST /sessions/{id}/pin -> 200
db:  is_pinned=1  pinned_at=2026-08-06 12:22:38  reviewer_harness=(unset)
api: isPinned=true  pause=null  activity=idle
```

`sqlc.embed(sessions)` carries upstream's three new columns with no adapter
changes — the point of deleting the hand-written adapters.

## 7. Usage telemetry stays distinct from a limit signal

Upstream's usage tables exist and are wired; nothing in that path pauses
anything, and `LimitDetectionSupported` is still **false** for every harness.
`paused_sessions=0` after the resume.

## 8. Residues clean

```
reap_queue = 0    replacement_intents = 0    sessions with pending switch = 0
```

---

## Defect found, fixed, and re-run from clean state

**Pause/resume returned manager sentinels unmapped** (`7e388bf2`). A
stale-incident resume correctly refused to lift the pin but answered
`500 INTERNAL_ERROR` instead of `409 PAUSE_INCIDENT_MISMATCH` — the safety
property held while the answer was useless. Both service methods returned the
manager's error raw, never through `toAPIError`.

The suite could not catch it: one test exercised `toAPIError` with no service,
another exercised the service with a fake that never errored. Neither joined
the two. The regression now drives the service with a manager returning each
sentinel wrapped as the manager wraps it. Fixed, rebuilt, data dir wiped, and
§4 above re-run from scratch.

---

## Finding — CLOSED at `84582db6`

**The runtime reaper terminated a paused session whose runtime died.**

`observe/reaper.Tick` probes every non-terminated session and applies a dead
observation, which terminates the row. It has no pause awareness, so after the
§5 restart the worker read:

```
paused=dogfood-inc-1   activity=exited   is_terminated=1   restore markers=0
```

This is **not** a rule-2 violation — nothing was relaunched or sent, verified
above — and the session was not stuck: an explicit `POST /sessions/{id}/restore`
returned 200 and brought it back, after which the correct-incident resume
cleared the pin.

But it contradicts `PHASE3A_PAUSE_CONTRACT.md` §1/§2, which states a paused
session whose agent dies **stays active with a dead runtime** so the UI can
offer "Restart agent". In reality the row is terminated, and because
`reconcileLive` deliberately skips writing a restore marker for paused
sessions, it is terminated *without* one.

**Resolved (1): the lifecycle sink is now pause-aware**, in
`ApplyRuntimeObservation` rather than `reaper.Tick` — the sink re-reads under
the mutation fence, so a resume landing between the usage-finalize pass and the
decision pass is handled instead of being decided from a stale snapshot. A
confirmed-dead paused session keeps `IsTerminated=false` and its incident,
records `ActivityExited`, releases tool-flight state, has usage finalized and
containers reaped, and writes no restore marker.

The orchestrator case is why it had to be (1): terminating a paused
orchestrator releases migration 0057's partial-unique active slot, so a
replacement could be spawned while the paused owner is still restartable — two
claimants on one canonical worktree.

### Re-run from clean state, both kinds paused (`84582db6`)

```
BEFORE   syncproj-1 orchestrator paused=inc-orch  activity=idle    terminated=0 launch=93733c3b
         syncproj-2 worker       paused=inc-worker activity=exited terminated=0 launch=450b64df
         (both runtimes killed while paused, daemon restarted)

AFTER    syncproj-1 orchestrator paused=inc-orch  activity=exited  terminated=0 launch=93733c3b
         syncproj-2 worker       paused=inc-worker activity=exited terminated=0 launch=450b64df
         markers=0  reap_queue=0  intents=0  tmux relaunched=0  boot ERRORs=0
```

Both stay **active** with the pin retained and the death **recorded**; the
orchestrator keeps its ownership slot; nothing relaunched.

## Post-stop recovery — original generation reaches target_ack, no dual launch

A durable post-stop window was injected (pending pinned to generation
`postop-gen-1`, `requested`/`pre_stop`/`post_stop` ledgered, source handle
cleared) and the source runtime destroyed, then the daemon restarted.

```
ledger after boot:   requested / pre_stop / post_stop / target_ack   all gen=postop-gen-1
session:             harness=codex  runtime_launch_id=postop-gen-1  pending=CLEARED  terminated=0
distinct generations reaching target_ack: 1
tmux sessions for the worker:             1
boot ERRORs:                              0
```

Recovery completed **the original generation** rather than minting a new one,
and exactly one runtime exists — no dual launch.

## Final residues

```
reap_queue=0  replacement_intents=0  pending=0  paused=0  failed_phases=0
preserved ~/.ao/dev/data/ao.db: md5 unchanged
```

## Still not covered — one blocker remains open

**Terminal/tmux keystroke suppression during a held switch was NOT exercised
live.** `AllowTerminalInput` is the gate (wired at `daemon.go` via
`termMgr.SetInputGate`), and it resolves a terminal three ways — session id,
live runtime handle, and `json_extract` on the pending pin's
`sourceRuntimeHandleId`, which is the case that matters after the source is
destroyed. But terminal input arrives over a **websocket**, not an HTTP route,
so exercising it end-to-end needs a websocket client this run did not build.
The gate has unit coverage; what is missing is a live keystroke against a
daemon holding a real pending pin.

This is the one remaining blocker on the complete gate. It should be closed
before `roles/upstream-sync` merges back.

Also unexercised, and deliberately so: live structured limit detection (3A-2b,
unbuilt).
