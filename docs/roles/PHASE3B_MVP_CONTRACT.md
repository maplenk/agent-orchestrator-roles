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
- `generation_id` (the switch saga's `ForceLaunchID`; empty until the saga starts)
- `state` — `requested` | `acked` | `failed`, CHECK-constrained
- `created_at`, `updated_at`
- `UNIQUE(session_id, incident_id, seq)`; index on `(session_id, incident_id)`

Agent A owns the exact DDL, the sqlc queries and the store methods. The columns
above are the contract; adding a column is allowed, removing one is not.

---

## 6. Ordering guarantees (non-negotiable)

1. **Ledger before effect.** The `failover`/`requested` ledger row and the
   attempt row are durable *before* the switch saga touches the runtime. Same
   rule 3A applied to pause.
2. **Pause clears only after `target_ack`.** The pin stays through
   `requested`, `pre_stop` and `post_stop`. It is cleared under a CAS on the
   *same* `incidentId` — a newer pin must never be cleared by an older
   continuation.
3. **Failure leaves the pause and the attempt intact.** A failed launch is
   `state=failed` + ledger `failed`, pin untouched, session still paused. It
   does not schedule a retry; nothing in AO retries on its own.
4. **One relaunch path.** Continue calls the existing switch saga
   (`switchUnderOwnership` via the worker entry point). It must not open a
   second launch path.
5. **Idempotence.** A duplicate Continue for an incident whose latest attempt is
   still in flight returns that attempt (`reused: true`) — same `generationId`,
   same `attemptSeq`, no second attempt row, no second runtime.
6. **Crash recovery completes the same incident.** An incomplete `post_stop`
   whose generation matches the incident's latest attempt is finished through
   `RecoverSwitchFromPostStop` — one runtime, one `target_ack`. An incomplete
   `post_stop` from anything else is `FAILOVER_RECOVERY_REQUIRED`.
7. **Role identity is invariant.** `role_id`, `template_artifact_id`,
   `template_sha256` and `resolved_permissions` are byte-identical before and
   after. Only `resolved_harness` / `resolved_model` move.

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
  reason: "" | "no_role_pin" | "no_ladder" | "ladder_exhausted" | "limit_reached" | "not_paused" | "switch_unsupported"
} | null
```

`available: false` carries a machine-readable `reason` so the desktop can
disable the control and say *why* without inventing prose. The block is `null`
for sessions that are not paused **and** have no ladder — the ordinary case.

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
4. Target launch failure → pause retained, attempt `failed`, no retry.
5. Post-stop crash → restart → original generation, exactly one runtime, one `target_ack`.
6. Duplicate Continue → no second attempt row, no second runtime.
7. Ladder exhausted → `FAILOVER_NO_TARGET`, still paused.
8. No automatic failover occurs anywhere; default mode stays `manual`.
9. `limit_detection_supported` remains `false` for every harness.
