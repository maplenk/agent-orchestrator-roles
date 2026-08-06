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

## Open finding — NOT fixed, needs a decision

**The runtime reaper terminates a paused session whose runtime died.**

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

Two coherent resolutions, and this needs a decision rather than a late patch:

1. **Make the reaper pause-aware** — skip paused sessions, matching the
   contract. Keeps the documented "active but dead" cell real.
2. **Amend the contract** — accept that paused + dead becomes terminated, and
   specify that the UI reads `terminated + pause != null` as the restart case.

(1) preserves the state model the 3A-2 UI was designed against; (2) is less
code but makes "paused" and "terminated" overlap, which the three-control
contract was written to avoid.

## Not covered by this run

- Terminal-level (tmux/PTY) input fencing during a switch — only the API fence
  was exercised here; the API fence and automatic-send fence are covered by
  `sessionguard` tests, not by this run.
- Post-stop crash recovery **mid-switch** using the original generation: the
  switch sagas above all completed, so no post-stop residue existed to recover.
  The paused-session skip of post-stop recovery *was* observed (§5).
- Live structured limit detection — 3A-2b, deliberately unbuilt.
