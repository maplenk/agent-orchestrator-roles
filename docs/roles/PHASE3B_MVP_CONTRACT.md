# Phase 3B MVP — the frozen Continue contract

**Status:** frozen 2026-08-07 by the orchestrator, before any worker agent was
spawned. Nothing below may be changed by an implementing agent. If an agent
believes a clause is wrong, it stops and reports; it does not edit this file.

**Scope:** manual failover only. Detection is *out* — no vendor fixture exists,
so `limit_detection_supported` stays `false` everywhere and no detector is
registered. The MVP rides the operator pause that 3A-2a already ships.

---

## 1. What the MVP delivers

A paused, role-pinned worker can:

1. Show the next authorized failover target (read model, host-resolved).
2. **Continue** on that target through the existing switch saga.
3. Keep `role_id`, template artifact and permissions across the move.
4. Survive a crash mid-continue without a duplicate runtime.
5. Stay paused if the continuation fails.

Explicitly **not** in this MVP: automatic failover, Claude read-only,
cross-harness orchestrator switching, a desktop role-map editor, any detector,
timers, or automatic retries.

---

## 2. The three operations stay distinct

| Control | Meaning | Endpoint |
|---|---|---|
| **Resume** | lift the pin; AO may write again. Starts nothing. | `POST /sessions/{id}/resume` |
| **Restart agent** | relaunch a process for a session whose agent is gone, **same harness** | existing restore / resume-agent |
| **Continue with `<target>`** | move to the **next failover rung** and lift the pin once the target acks | `POST /sessions/{id}/continue` |

Continue is the only one of the three that changes harness/model. It never
changes `role_id`.

**Distinct does not mean always rendered.** The requirement is that the three
operations never blur into one another — not that all three appear in every
state. The control set is state-appropriate:

| Cell | Controls |
|---|---|
| **paused-live** (agent running) | Resume + Continue |
| **paused-dead** (agent gone) | Resume + Restart agent + Continue |

**Restart must remain absent while the process is alive.** 3A already landed and
asserted this — `SessionInspector.test.tsx`'s paused-live case requires Restart
to be *absent*, "not merely worded differently" — and offering to restart a
process that is running is an invitation to a second runtime, which is the
failure this whole phase is organised against. Continue is offered in **both**
cells, because a live-but-limited agent is exactly the case failover exists for.

---

## 3. Endpoint

```
POST /api/v1/sessions/{sessionId}/continue
  { "incidentId": "<the incident being answered, required>" }

200 -> {
  "ok": true,
  "sessionId": "…",
  "incidentId": "…",
  "generationId": "…",
  "target": { "harness": "codex", "model": "" },
  "rungIndex": 0,
  "attemptSeq": 1,
  "reused": false,
  "session": { …SessionView… }
}
```

### Hard rules on the request

- The body carries **`incidentId` and nothing else**. There is no
  `targetHarness`, no `targetModel`, no `roleId`. A free-form target must be
  structurally impossible, not merely rejected.
- The host selects the target: the next **unused** rung of
  `roleMap.failover.roles[<session role id>]`.
- Body capped at 4 KiB (same reader as pause/resume).
- `incidentId` obeys `domain.ValidateIncidentID` (≤128 bytes, `[A-Za-z0-9._-]`).

### Authorization

Identical to pause/resume: **`authorizeOperatorPause`**, reused verbatim.
LAN-authenticated or a valid operator credential; a caller presenting session
capability headers is refused with `PAUSE_AGENT_FORBIDDEN`. Continue answers a
human's pause, so it inherits the human's authority — not the agent's spawn
capability. Do not write a second auth helper with the same semantics.

---

## 4. Error codes

Reused from the pause contract (same meaning, same status):

| Code | Status | Meaning |
|---|---|---|
| `PAUSE_INCIDENT_REQUIRED` | 400 | no incident id supplied |
| `PAUSE_INCIDENT_INVALID` | 400 | id over 128 bytes or outside `[A-Za-z0-9._-]` |
| `PAUSE_INCIDENT_MISMATCH` | 409 | a different incident holds the pin; response names which |
| `SESSION_NOT_PAUSED` | 409 | nothing to continue |
| `SESSION_TERMINATED` | 409 | |
| `PAUSE_AUTH_REQUIRED` / `PAUSE_AGENT_FORBIDDEN` / `OPERATOR_CREDENTIAL_INVALID` | 403 | |
| `SWITCH_NOT_SUPPORTED` | 409 | source or target harness `switch_supported=false` |
| `SWITCH_CHAT_UNSUPPORTED` | 409 | chat sessions do not enter the saga |
| `SWITCH_IN_PROGRESS` | 409 | another switch/fresh saga holds the fence |
| `NOT_A_WORKER` | 400 | orchestrators do not failover in this MVP |
| `ROLE_MAP_REQUIRED` / `ROLE_NOT_IN_MAP` | 400 | |

New, introduced by this MVP:

| Code | Status | Meaning |
|---|---|---|
| `FAILOVER_NO_TARGET` | 409 | role has no ladder, or every rung is already used for this incident |
| `FAILOVER_ROLE_REQUIRED` | 400 | session carries no durable role pin |
| `FAILOVER_LIMIT_REACHED` | 409 | `MaxFailoversPerIncident` reached; the session stays paused |
| `FAILOVER_RECOVERY_REQUIRED` | 409 | an incomplete `post_stop` belongs to a *different* generation; recover that first |

`FAILOVER_NO_TARGET` and `FAILOVER_LIMIT_REACHED` are **not** failures of the
pause: the pin survives untouched, which is the whole point of the cap.

**`ErrFailoverNotWired` deliberately has no public code.** It means the daemon
was assembled without the failover surface — a wiring bug, not a condition any
caller can act on — so it surfaces as a 500 like every other internal fault.
This is the existing precedent, not a new one: `ErrSwitchNotWired` is returned
raw by both `switch.go` and `pause.go` for exactly the same reason. Inventing a
fifth public code here would tell an operator to react to a build defect.

---

## 5. Durability — migration `9008`

Reserved: `backend/internal/storage/sqlite/migrations/9008_session_failover_attempts.sql`.
No other migration number may be used by this MVP.

The lifecycle ledger needs **no** migration: `failover` is already in the `kind`
CHECK constraint (9002, re-stated in 9006). Use `domain.LifecycleKindFailover`
with the existing phases `requested → pre_stop → post_stop → target_ack`, or
`failed`.

`9008` adds the durable attempt record. One row per continuation attempt:

- primary key `<sessionId>:<incidentId>:<seq>` — the same composition rule the
  ledger key uses, which is why `ValidateIncidentID` excludes `:`
- `session_id`, `project_id`, `incident_id`, `seq`
- `role_id`, `from_harness`, `from_model`, `to_harness`, `to_model`, `rung_index`
- `generation_id` — the switch saga's `ForceLaunchID`. **Never empty.** See §6a.
- `state` — `requested` | `post_stop` | `acked` | `failed`, CHECK-constrained
- `created_at`, `updated_at`
- `UNIQUE(session_id, incident_id, seq)`; index on `(session_id, incident_id)`
  and on `(session_id, generation_id)` for recovery matching

Agent A owns the exact DDL, the sqlc queries and the store methods. The columns
above are the contract; adding a column is allowed, removing one is not.

---

## 6. Ordering guarantees (non-negotiable)

1. **Ledger and attempt are ONE write.** The `failover`/`requested` ledger row
   and the attempt row are inserted in a **single SQLite write transaction**,
   via the existing `Store.inTx` helper (`storage/sqlite/store/store.go:70`),
   which hands the callback a `*gen.Queries` that already carries
   `InsertLifecycleLedger`. There is therefore no "between" for a crash to land
   in, and no orphan-repair logic to get wrong. Both rows are durable before the
   switch saga touches the runtime — the 3A "ledger before effect" rule,
   strengthened to atomicity because here it is two rows rather than one.
   This also means Agent A needs no edit to `lifecycle_ledger_store.go`, which
   it does not own. A test must assert atomicity by failing the second insert
   and proving neither row exists.
2. **Pause clears only after `target_ack`.** The pin stays through
   `requested`, `pre_stop` and `post_stop`. It is cleared under a CAS on the
   *same* `incidentId` — a newer pin must never be cleared by an older
   continuation.
3. **Failure never lifts the pause.** In every failure mode the pin is
   untouched and the session stays paused. What the *attempt* records depends on
   how far the saga got, and that distinction is §6a — it is not "any failure is
   terminal".
4. **One relaunch path.** Continue calls the existing switch saga
   (`switchUnderOwnership` via the worker entry point). It must not open a
   second launch path.
5. **Idempotence adopts any non-terminal attempt.** A duplicate Continue for an
   incident whose latest attempt is `requested` **or** `post_stop` returns that
   attempt (`reused: true`) — same `generationId`, same `attemptSeq`, no second
   attempt row, no second runtime, no second rung spent.
6. **Crash recovery completes the same incident, and only an explicit Continue
   drives it.** An incomplete `post_stop` whose generation matches the
   incident's latest non-terminal attempt is finished through
   `RecoverSwitchFromPostStop` — one runtime, one `target_ack`. An incomplete
   `post_stop` matching nothing is `FAILOVER_RECOVERY_REQUIRED`.

   **Boot is passive for paused failovers.** Boot's `pausedSkip` already
   excludes paused sessions from automatic post_stop recovery and this MVP does
   not change that, so a restart alone never reaches `target_ack` — it leaves
   the session paused, pending, with no duplicate runtime, waiting for a human.
   That is the smallest coherent design: the ladder is only ever advanced by a
   person, and so is the completion of a rung.

   The consequence is load-bearing rather than incidental, because it means a
   `requested` attempt whose saga never started has **nothing** that would ever
   drive it. `ContinueFailover` therefore re-drives such an attempt on its own
   stored target and generation rather than reporting it as an in-flight
   duplicate. The discriminator is the in-memory `beginSwitch` fence: a live
   saga holds it and answers `ErrSwitchInProgress`, which is the only true
   duplicate; after a crash it is free. No durable fact distinguishes the two.
7. **Role identity is invariant.** `role_id`, `template_artifact_id`,
   `template_sha256` and `resolved_permissions` are byte-identical before and
   after. Only `resolved_harness` / `resolved_model` move.

## 6a. The attempt state machine — terminal vs recoverable

The first draft of this contract said every launch failure is `failed` while
idempotence adopted only `requested`. Those two clauses contradict: a target
launch that fails *after* the source stopped is recoverable by the switch saga
that already exists, and boot's `Reconcile` will recover it. That produced three
reachable defects — a `failed` attempt reaching `target_ack`, an attempt left
`failed` after a successful recovery, and a Continue that starts a *second*
attempt over an unrecovered switch (two runtimes, and a ladder advance nobody
asked for, which is the automatic failover this MVP refuses to build).

Terminal-vs-recoverable is decided by **whether the source was stopped** —
exactly the line the existing saga already draws between a rolled-back pre-stop
failure and `ErrSwitchPostStop`.

| State | Meaning | Terminal | Rung spent | Continue does |
|---|---|---|---|---|
| `requested` | durable; source **not** stopped. Saga running (fenced by `beginSwitch`) or crashed pre-stop. | no | yes | adopt — safe to re-drive, nothing was destroyed |
| `post_stop` | source stopped, handoff retained, target has not acked | **no — recoverable** | yes | adopt and drive `RecoverSwitchFromPostStop` on the **same** generation |
| `acked` | target owns input; pin cleared for this incident | yes | yes | nothing; the incident is closed |
| `failed` | **pre-stop failure only** — source confirmed alive, nothing destroyed | yes | yes | nothing; a new Continue starts a new attempt on the next rung |

Load-bearing consequences:

- A post-stop failure **never** writes `failed`. It writes `post_stop` and stops.
  `failed` is reachable only when the source survived.
- `post_stop → acked` is performed **only by an explicit operator Continue** that
  adopts the attempt and recovers its generation. Boot does not do it: this MVP
  keeps boot passive for paused sessions (rule 6), so a restart leaves the
  session paused and pending rather than completing anything. An earlier draft
  of this section named boot `Reconcile` as a second completer, which
  contradicted that decision and would have made acceptance #5 pass without a
  human in the loop.
- **`acked` does not by itself license clearing the pin.** The switch saga makes
  its `target_ack` ledger row durable *before* it promotes the session, so an
  attempt can read `acked` while `SwitchPending` is still set and the row still
  names the source harness. Promotion must be proven on every axis — no pending
  pin, session harness and model equal to the attempt's target, runtime
  generation equal to the attempt's — before the pause is lifted. An `acked`
  attempt that is not promoted is finished on the *same* generation through the
  existing recovery path, or handed to a human as
  `FAILOVER_RECOVERY_REQUIRED`; it is never re-launched (a durable ack means a
  target runtime may already exist), and it never falls through to a new rung.
- **Neither is an automatic retry.** Both complete the *same rung* on the *same
  generation*; neither selects a new rung, and only a human's Continue ever
  advances the ladder. Automatic failover would be choosing a new rung with no
  human in the loop, and nothing here does that.
- `ErrFailoverRecoveryRequired` therefore means "an incomplete `post_stop`
  exists that this incident's latest non-terminal attempt does not account for"
  — a genuine ambiguity a human must resolve, not a routine state.

## 6b. The generation is minted by the caller

`SwitchRequest` gains one field, **frozen here and owned by Agent A**:

```go
// ForceGenerationID pins the saga's generation. Empty keeps today's behaviour
// (the saga mints its own), so every existing caller is unaffected.
ForceGenerationID string
```

Continue mints the generation (a uuid, as `newSwitchGeneration` does) *before*
the §6 rule 1 transaction and passes it in, so the attempt row is durable **with
its generation from the very first write**.

Without this, the generation is minted inside the saga and the attempt row must
be written empty and stamped afterwards — a third durable write, and a crash in
that window leaves an attempt that can only be matched to its runtime by
guessing from `(to_harness, to_model, role_id)` and ledger ordering. Recovery
matching then decides whether to adopt or to launch again, so a wrong guess is a
second runtime. Replacing a heuristic with an identity is worth a three-line,
behaviour-preserving field on a struct.

This is the one edit to `switch.go` Agent A is authorized to make. It must be
exactly this: an optional field, honoured where `newSwitchGeneration()` is
called, no other change. A test must prove the empty case is byte-identical to
today's behaviour.

---

## 7. Target selection

```go
domain.NextFailoverRung(m RoleMap, roleID string, current FailoverTarget, used []FailoverTarget) (FailoverTarget, int, error)
```

Frozen and implemented in `backend/internal/domain/failover_contract.go` because
three agents depend on it (A to execute, B to expose it in the read model, C to
label the button). Rules:

- Candidates are `m.Failover.Roles[roleID]`, in declared order. The primary
  binding is **not** a rung — a first continuation must not re-select the
  current target.
- A rung equal to `current` (harness **and** model, empty model = provider
  default on both sides) is skipped.
- A rung already in `used` is skipped. `used` is every prior attempt for **this
  incident**, in any state — a rung that failed is spent, not retried.
- First survivor wins. None → `ErrFailoverNoTarget`.
- Comparison is exact: an empty configured model means provider default, never
  a wildcard. This mirrors `ResolveAuthorizedSwitchModel`.

Every selected rung is by construction inside
`domain.RoleAuthorizedSwitchTargets`, so capability validation (source **and**
target `switch_supported`) applies unchanged.

## 8. `MaxFailoversPerIncident`

Frozen as the **constant** `domain.MaxFailoversPerIncident = 8`, not a role-map
field.

Adding a field to `FailoverConfig` would change `RoleMap.SHA256()`'s hash
document, and that hash is durably pinned on every existing session as
`role_map_sha256`. Introducing schema drift across every live session to
configure a bound that the ladder length already dominates is a bad trade for
an MVP. The effective bound is `min(len(ladder), 8)` because rungs are
single-use per incident. Making it configurable is a post-MVP change that must
come with a migration story for the pinned hash.

Reaching the bound is `FAILOVER_LIMIT_REACHED` and the session **stays paused**.

## 9. Read model

`SessionView` gains one nullable block, computed at read time — never stored
(the "no derived state" rule stands):

```
failover: {
  available: boolean,
  roleId: string,
  nextTarget: { harness, model } | null,
  nextRungIndex: number,
  attemptsUsed: number,
  maxAttempts: number,
  incidentId: string,
  reason: "" | "no_role_pin" | "no_ladder" | "ladder_exhausted" | "limit_reached" | "not_paused" | "switch_unsupported" | "unavailable"
} | null
```

`available: false` carries a machine-readable `reason` so the desktop can
disable the control and say *why* without inventing prose. The block is `null`
for sessions that are not paused **and** have no ladder — the ordinary case.

**The null rule is a derivation the read surface must perform**, on the pair
`(pause == nil && reason == no_ladder)`. The manager cannot express null in a
value type, so a surface that omits this derivation ships a `no_ladder` block on
every ordinary worker. Comparing the preview against its zero value is **not**
the rule and does not work: the manager always sets `nextRungIndex` and
`maxAttempts`, so the zero value is unreachable on any success path — a guard
written that way is dead code that never fires.

**`unavailable` means the preview could not be COMPUTED** — a store or project
read failed — and is never a verdict about the ladder. The manager never returns
it; the read surface substitutes it when the manager returns an error, so one
unreadable row degrades that row rather than failing the whole response. This
matters because the preview is computed for **every worker in a list**:
propagating turned a single unreadable project into a 500 across the entire
fleet, on the endpoint the desktop polls most.

It is a distinct value rather than a reuse of `null` because `null` already
means "ordinary session, nothing to offer". Collapsing "we know there is
nothing" into "we could not find out" is a silent degrade — a paused session
would simply stop offering Continue and say nothing about why. Degrading toward
`available` is forbidden in every case: an unreadable store can never produce an
offer to continue.

The service obtains this from the manager via `FailoverPreview`; it does not
re-derive the ladder itself, and React never resolves a harness.

## 10. CLI

```bash
ao session continue --session <id> --incident <id>
```

Prints the backend-resolved target. The client never resolves the ladder,
never accepts `--harness`/`--model`, and mirrors the DTO by hand as every other
CLI command does. Usage errors exit 2; daemon failures exit 1.

## 11. File ownership

Overlapping edits are the failure mode this section exists to prevent. An agent
that needs a file it does not own stops and reports to the orchestrator.

| Owner | Files |
|---|---|
| **Orchestrator (frozen — nobody edits)** | `docs/roles/PHASE3B_MVP_CONTRACT.md`, `backend/internal/domain/failover_contract.go`, `backend/internal/session_manager/failover_contract.go` |
| **Agent A** | `backend/internal/domain/failover.go` + tests, `backend/internal/session_manager/failover.go` + tests, `storage/sqlite/migrations/9008_*.sql`, `storage/sqlite/queries/session_failover*.sql`, `storage/sqlite/store/session_failover_store*.go`, regenerated `storage/sqlite/gen/*` |
| **Agent B** | `backend/internal/service/session/failover.go` + tests, the `toAPIError` failover arm in `service/session/service.go`, `httpd/controllers/sessions.go` route + handler, `httpd/controllers/dto.go`, `httpd/apispec/specgen/build.go`, generated `openapi.yaml` + `frontend/src/api/schema.ts`, `backend/internal/cli/session*.go` |
| **Agent C** | `frontend/src/renderer/components/SessionPausePanel.tsx` + test, `SessionInspector.tsx` + test, `renderer/hooks/*`, `renderer/lib/api-client.ts`, `renderer/i18n/*.json`, `renderer/types/*` |
| **Agent D** | the existing relaunch/restore delivery path in `session_manager/manager.go` (restore/resume-agent region only) and its new tests. **Must not** create or edit any `failover*` file. |

Agent A must not edit the relaunch/restore path. Agent D must not edit the
switch saga. Where they meet — a paused-dead session that gets Restarted — the
orchestrator resolves the seam during integration.

## 12. Acceptance (live, before anything is promoted)

1. Manual pause → Continue → next authorized rung, on a **paused-live** source.
2. Same on a **paused-dead** source.
3. `role_id` and template artifact byte-identical after the move.
4. **Pre-stop** target failure (source confirmed alive) → attempt `failed`, pause
   retained, no retry.
5. **Post-stop** target failure → attempt `post_stop` and **not** `failed`, pause
   retained. The rung is not re-offered and no new rung is spent.
6. Post-stop crash → **restart leaves the session paused and pending with no
   duplicate runtime**, and reaches no `target_ack` on its own — boot is
   passive (§6 rule 6).
7. The next **explicit** Continue after that restart recovers the **same
   generation** and produces exactly one runtime and one `target_ack`.
8. A `requested` attempt whose saga never started is re-driven by the next
   Continue on its own stored target and generation — one runtime, no second
   attempt row.
9. Duplicate Continue **while a saga is live** → no second attempt row, no second
   runtime.
10. Ladder exhausted → `FAILOVER_NO_TARGET`, still paused.
11. No automatic failover occurs anywhere; default mode stays `manual`.
12. `limit_detection_supported` remains `false` for every harness.

Items 4–8 replace a single earlier line that said "target launch failure →
attempt `failed`" and a line claiming a restart alone reaches `target_ack`. Both
were written before §6a split terminal from recoverable and before boot was
settled as passive, and each contradicted one of those decisions.
