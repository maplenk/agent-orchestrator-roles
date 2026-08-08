# Phase 3B manual failover — live dogfood, attempt 1

**SHA under test:** `fc0be219` (code identical to `7c018683`; the later commit is
docs only)
**Daemon:** real `ao daemon`, isolated `AO_DATA_DIR=~/.ao/dogfood-3b`, port 3001
**Project:** `mer3b`, scratch git repo, non-strict role map
**Ladder:** `implementor` primary `claude-code` → rung `[codex]`, `mode: manual`
**Worker:** `mer3b-1`, spawned `ao spawn --project mer3b --role implementor`
(real claude-code launch, 9985-byte role system prompt)

Non-strict deliberately: a strict map requires the orchestrator role to be
`workspaceWrites:false`, which is still blocked on Claude read-only (Phase 1-B).
The failover path does not depend on strictness — only on a durable role pin.

---

## Status: 5 of 12 captured. **Not accepted.** One live blocker, one live finding.

| # | Record | Result |
|---|---|---|
| 1 | Paused-live → Continue → next rung | **BLOCKED** — see the blocker below |
| 2 | Paused-dead → Continue | **BLOCKED** — same cause |
| 3 | `role_id` + template artifact unchanged | **PASS** — byte-identical across a failed continuation |
| 4 | Pre-stop failure → `failed`, pause retained | not reached |
| 5 | Post-stop failure → `post_stop` (not `failed`), pause retained | **PASS** — observed exactly |
| 6 | Post-stop crash → restart leaves paused/pending, no dup runtime, no ack | not reached |
| 7 | Next explicit Continue recovers same generation → one runtime, one ack | **PARTIAL** — adoption proven, completion blocked |
| 8 | `requested` crash re-driven on same generation | not reached |
| 9 | Duplicate Continue → no second attempt/rung/runtime | **PASS** |
| 10 | Ladder exhausted → `FAILOVER_NO_TARGET` | not reached (a live `post_stop` is adopted first, correctly) |
| 11 | No automatic failover anywhere | **PASS** — nothing advanced unattended across ~10 min |
| 12 | `limit_detection_supported` false everywhere | **PASS** — no harness sets it true |

## What was proven live

**The read model works end to end.** Before pause: `available:false`,
`reason:"not_paused"`. After pause:

```json
"failover": { "available": true, "roleId": "implementor",
  "nextTarget": {"harness":"codex","model":""}, "nextRungIndex": 0,
  "attemptsUsed": 0, "maxAttempts": 8, "incidentId": "inc-dogfood-1", "reason": "" }
```

That is contract §9 against a real daemon, and it settles the one runtime risk
the compile-time assertion could not: **`FailoverPreview` is genuinely wired on
the production service.** A desktop would render "Continue with codex" from it.

**Failure never lifted the pause, and the role pin never drifted.** After a
failed continuation the pin was intact and
`harness|role_id|template_artifact_id|template_sha256|resolved_model` was
byte-identical to the pre-Continue baseline (§6 rules 3 and 7).

**Post-stop classification is right, and Agent A's judgement call proved itself.**
The attempt was written `post_stop`, not `failed`, because `switch_pending_json`
was set — `failoverFailureState` classifying by the durable fence rather than the
error string is exactly what made that correct here, since the saga reported a
*pre-stop* error while leaving a live fence behind.

**Idempotence holds against real state.** A second Continue produced no second
attempt row, no second rung, no second runtime, and no new `requested` ledger
row — it adopted seq 1 on generation `d1fdde64`.

## The blocker — and it is not in this MVP's code

Both Continues failed with `SWITCH_UNCERTAIN`:

```
switch mer3b-1: pre-stop: session: switch runtime state uncertain:
probe after destroy: tmux runtime: probe session mer3b-1:
runtime: infrastructure unavailable: no server running on /private/tmp/tmux-501/ao-d001b90ab03e
```

The worker was given a trivial prompt, finished it, and exited. Its tmux server
went down with it, leaving a dead socket. `destroyRuntimeProbed`
(`session_manager/switch.go:786`) turns **any** probe error into
`ErrSwitchUncertain`:

```go
alive, probeErr := m.runtime.IsAlive(ctx, handle)
if probeErr != nil {
    return false, fmt.Errorf("%w: probe after destroy: %w", ErrSwitchUncertain, probeErr)
}
```

So the saga refused to assume death — which is the repo's hard rule working
("do not treat failed/unknown runtime probes as proof a session is dead"), and
in isolation it is the correct refusal.

### The live finding worth escalating

The probe path does not distinguish **"the probe failed"** from **"the server
that would host this pane does not exist"**. Those are different facts: a
transient probe error is genuinely unknown, but `no server running` means no
pane can be alive on that socket at all, because tmux panes live inside the
server process. Treating the second as unknown makes a session whose agent
exited **permanently un-switchable** — every Continue and every recovery returns
`SWITCH_UNCERTAIN` forever, with the pause correctly retained and no way
forward except Resume.

> **Correction (2026-08-08).** An earlier revision of this file said AO uses a
> *per-session* tmux socket. That is wrong. `SocketForDataDir` hashes the DATA
> DIRECTORY, so one namespaced server is shared by every session of a daemon,
> and the default data dir deliberately uses the default server. That changes
> the blast radius of any fix here and is why the fix was scoped to namespaced
> sockets only — see `PROBE_CLASSIFICATION.md`.

That matters for 3B specifically because **paused-dead is a first-class MVP
state** (record 2) and this is the ordinary way a session becomes paused-dead.

Scope: `destroyRuntimeProbed` is **Phase 2A code**, unchanged by this MVP —
`git log` shows no 3B commit touching it. This is a pre-existing gap that 3B is
the first feature to walk into, not a regression introduced here. It is
deliberately **not** fixed in this pass: narrowing a liveness rule is exactly the
kind of change that deserves its own review rather than an unreviewed edit at the
end of a dogfood.

## Re-run plan

To reach records 1, 2, 4, 6, 7, 8 and 10, the source agent must still be alive
when Continue is issued — a long-running prompt, or `remain-on-exit`, so the
tmux server survives the agent. Records 6 and 8 additionally need the daemon
killed at precise points (after `post_stop`, and between the attempt write and
the saga), which is scriptable once the probe problem is out of the way.

## Cleanup

Isolated daemon stopped; `~/.ao/dogfood-3b` and `/tmp/ao-3b-proj` are the only
state created. The real `~/.ao` install was never touched.
