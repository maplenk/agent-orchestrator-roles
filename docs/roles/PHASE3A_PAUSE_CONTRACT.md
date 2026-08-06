# Phase 3A — the pause / resume / restart contract

**This document is the precondition review made blocking on the 3A-2 surface.**
3A-1 landed a durable pause whose boot fencing deliberately creates a state the
UI has never had to render before, and exposing a single "resume" control over
it would be wrong in a way no amount of backend correctness can fix.

## 1. The state that forces this

3A-1 stops boot from relaunching a paused session (post-stop recovery, the live
pass's save-and-teardown, `RestoreAll`'s worker loop and orchestrator election).
The consequence is deliberate: a paused session whose agent dies stays **active
with a dead runtime** instead of being torn down and restored.

So a paused session is really **two independent facts**:

| | agent alive | agent dead |
|---|---|---|
| **paused** | pinned, process running, AO silent | pinned, process gone, AO silent |
| **not paused** | ordinary live session | ordinary exited session |

`ResumeSession` moves the row **left to right on the pause axis only**. It does
not start a process. On the bottom-right cell, resuming yields a session that is
un-paused and still dead — which is correct, and is exactly what a single
"Resume" button would misrepresent.

## 2. Three controls, not one

The surface must present these as distinct operations:

| Control | Meaning | Backend |
|---|---|---|
| **Paused** (indicator, not a control) | why AO stopped acting, since when, on which harness, which incident | read model |
| **Resume** | lift the pin; AO may write again | `POST /sessions/{id}/resume` |
| **Restart agent** | launch a process for a session whose agent is gone | the existing restore / resume-agent endpoints |

**Resume must never imply Restart.** MASTER_PLAN §7 rule 2 is "durable pause;
zero automatic send/restart", and a resume that relaunched would reintroduce the
automatic restart one layer up — the same defect as an auto-resume timer, moved
into the UI. Restart stays a second act the human chooses, on a session they can
see is dead.

A resume on a dead session is still meaningful and must stay available: it
records the human's decision in the ledger and makes the session restore-eligible
again.

## 3. Incident ids are carried, not re-read

`PauseSession` and `ResumeSession` both require a caller-supplied incident id.

- **Pause**: the id must be *stable for the incident*. Detection derives it from
  the envelope; an operator pause uses a client-generated id. This is what makes
  a retry after a failed pin write idempotent instead of opening a second
  incident (the ledger is written first, so a minted id the caller never learns
  cannot be retried against).
- **Resume**: the id names the incident the human is answering. The surface must
  carry it through **from whatever displayed the pause**, and must not re-read
  the current pin at submit time — re-reading is precisely the stale-resume bug:
  an action raised for incident A landing after B replaced it would resume B, on
  evidence nobody looked at.

A `409 PAUSE_INCIDENT_MISMATCH` therefore means "the world moved: re-read and
show the human what actually holds the session now", not "retry".

## 3b. Authorization — pause is operator-owned

Both endpoints require **LAN authentication or a valid operator credential**. A
caller presenting session capability headers is refused with
`403 PAUSE_AGENT_FORBIDDEN`, explicitly rather than by falling through.

This is stricter than `switch`, deliberately. The incident id is in the session
read model, so a worker can read its own; if the spawn capability were accepted
here, that worker could POST `/resume` and lift the pause a human placed on it,
and a sibling could pause a competitor to stop it. Either makes the pause
guarantee vacuous. The capability authorizes an agent to **spawn**, which is a
different question from whether it may release a session a human parked.

The desktop is an operator and injects `X-AO-Operator-Spawn-Token` on these two
paths, alongside spawn and switch.

## 4. HTTP surface (3A-2)

```
POST /api/v1/sessions/{sessionId}/pause
  { "incidentId": "<client-generated, required>",
    "reason": "operator" }
  200 -> { ok, sessionId, pause: {...} }

POST /api/v1/sessions/{sessionId}/resume
  { "incidentId": "<the incident being answered, required>" }
  200 -> { ok, sessionId }
```

`reason: "usage_limit"` is **not** accepted on this endpoint at any point: it
requires a structured envelope, which only a harness adapter can produce
(3A-2b). Operators pause; they do not report limits.

### Error codes

| Code | Status | Meaning |
|---|---|---|
| `PAUSE_INCIDENT_REQUIRED` | 400 | no incident id supplied |
| `PAUSE_REASON_INVALID` | 400 | a reason the operator endpoint may not set |
| `SESSION_ALREADY_PAUSED` | 409 | a *different* incident holds it; response names which |
| `SESSION_NOT_PAUSED` | 409 | nothing to resume |
| `PAUSE_INCIDENT_MISMATCH` | 409 | a newer incident holds it; response names which |
| `SESSION_TERMINATED` | 409 | cannot pause a terminated session |
| `PAUSE_INCIDENT_INVALID` | 400 | id over 128 bytes or outside `[A-Za-z0-9._-]` |
| `PAUSE_AUTH_REQUIRED` | 403 | no operator credential and not LAN |
| `PAUSE_AGENT_FORBIDDEN` | 403 | a session principal tried to pause/resume |
| `OPERATOR_CREDENTIAL_INVALID` | 403 | operator credential present but wrong |

`incidentId` is bounded because it is durable in three places — `pause_json`,
the ledger `generation_id`, and the ledger PRIMARY key, which is built as
`<session>:<incident>:<kind>`. A separator there would make the key
structurally ambiguous, so the charset excludes one. Bodies are capped at 4 KiB.

Re-pausing the **same** incident is `200`, not a conflict: polling detectors
repeat, and a repeat is not an error.

## 5. Read model

The session view carries pause state so the UI never has to infer it:

```
pause: { incidentId, reason, detectedBy, harness, pausedAt, retryAfter? } | null
```

`retryAfter` is displayed as information only, and must never be rendered as a
countdown to an automatic resume — nothing schedules against it.

Liveness is already in the read model (`activity.state`), and that is what
distinguishes the two paused cells above. The UI must read both.

**Boot makes this fact truthful.** When reconcile proves a paused session's
runtime is dead it records an `exited` observation — without terminating the
session, writing a restore marker, or touching the pin. Without that, the
pre-crash activity would survive and the row would serialize as paused **and**
working, leaving the UI unable to know it should offer "Restart agent".

## 6. What this does not cover

Structured limit **detection** (3A-2b), manual continue on a ladder rung (3B),
and the `limit_detection_supported` promotion (after structured-limit tests).
`limit_detection_supported` stays **false** everywhere until then.
