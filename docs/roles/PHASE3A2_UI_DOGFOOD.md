# Phase 3A-2 UI slice — live dogfood

Date: 2026-08-06. Branch `roles/multi-sub-v1`, commits `d6d96c89` (pause panel),
`5de2df0c` (delegation carries a role), `2a46fde8` (strict composer).

Two surfaces landed: the **paused session panel** and the **strict delegation
composer**. Both were exercised against a real daemon on a **fresh, isolated
data dir**, not fixtures — the renderer's browser preview mode
(`VITE_NO_ELECTRON=1`) serves `mockWorkspaces`, so a browser screenshot would
have proven nothing about the daemon. The Electron app was used instead.

## Isolation

| | |
|---|---|
| Data dir | `~/.ao/uislice/data` (created empty for this run) |
| Run file | `~/.ao/uislice/running.json` |
| Port | 3199 |
| tmux server | `-L ao-5796b9cd6658`, derived from the data dir |
| Role templates | `~/.ao/uislice/profiles` (copied from `profiles/`) |

The user's real desktop app stayed up on port 3001 throughout and was never
touched. Its tmux sessions live on the default socket; the demo's lived on
`ao-5796b9cd6658` and were killed independently at the end, leaving the real
server's sessions running — the socket namespacing from `9f687d79` doing
exactly what it was built for.

**The quarantined `~/.ao/dev/data/ao.db` was never opened.** md5 before and
after: `92667182dac2215c4cacd147529fcc90` — unchanged, still at the goose 52
state recorded in `UPSTREAM_SYNC_DOGFOOD.md`.

## What the role map refuses, live

The strict map could not be persisted at first, and the refusals were correct:

1. `roles[x].harness: unknown harness "fake"` — the test harness is not
   admissible in a project config.
2. `roles[orchestrator].permissions.workspaceWrites: must be false for
   orchestrator under strictDelegation`.
3. `workspaceWrites=false requires harness "claude-code" with
   read_only_enforced (got false)`.

(2) and (3) together mean a strict map is only expressible today with a harness
that can actually enforce read-only. **Codex can** (`--sandbox read-only`);
Claude Code cannot yet, which is the 2B-3 blocker. So the map used
`orchestrator: codex, workspaceWrites=false`. This is worth recording: a strict
project is not reachable at all on a Claude-only install until 2B-3 lands.

## The three shapes the composer must not send

Against the live daemon, with the strict map installed:

| Request | Response |
|---|---|
| `{brief}` — no role, what the old composer sent | `400 ROLE_REQUIRED` |
| `{brief, roleId, agent:"cursor"}` | `400 HARNESS_OVERRIDE_FORBIDDEN` |
| `{brief, roleId:"nonesuch"}` | `400 ROLE_UNKNOWN` |

`ROLE_UNKNOWN` rather than `ROLE_REQUIRED` for the third is itself the proof
that `roleId` now reaches the resolver: before `5de2df0c` the field did not
exist on the delegate contract and every one of these was `ROLE_REQUIRED`.

## Composer, live

`ao preview http://localhost:5177/` was run from inside the seeded session
(`AO_SESSION_ID=uislice-1`), setting that session's preview target.

The composer on the strict project renders:

- a **Role** picker, no Agent field, no Model field;
- "This project delegates by role. The role fixes the agent and model, so
  neither can be overridden here.";
- options **implementor, reviewer, verifier** — `orchestrator` is in the map
  and deliberately absent from the list, because delegation spawns a worker;
- on selecting `implementor`, the binding **`codex · gpt-5.6-codex`** shown as
  a fact, not an editable field.

The role reached the launch, not just the request: the spawned process carries
`developer_instructions` ending in the host's authoritative footer —
`Active role: implementor. Harness: codex. … canSpawn=false: you must not spawn
other agents.`

## Pause panel, live

`uislice-1` was paused through the operator-authenticated endpoint (a plain
POST is refused `PAUSE_AUTH_REQUIRED`; the agent-header path is refused
`PAUSE_AGENT_FORBIDDEN`). The inspector then showed:

> **Paused** · `Operator` · `Agent running`
> Detected by — Requested by a person
> Harness — codex
> Since — 8/6/2026, 10:31:18 PM
> Incident — limit-demo-001
> **Resume** — "Lifts the pause so AO may act on this session again."

This is the paused-**live** cell: the agent kept running the whole time.
Clicking **Resume** cleared the pause (`pause: None` on the API), the panel
disappeared, and **the agent was not restarted** — the same codex process was
still in the terminal afterwards. That separation is the entire reason Resume
and Restart are distinct controls.

The conflict path, checked directly because it is a race the UI cannot stage
reliably: with `limit-demo-A` holding the session, answering the older
`limit-demo-001` returns

```
409 PAUSE_INCIDENT_MISMATCH
"A different incident now holds this session; re-read it and answer the current one"
```

and answering the displayed `limit-demo-A` returns `{"ok":true}`. The panel
renders that code and message verbatim in a `role="alert"`
(`SessionPausePanel.test.tsx`), which is why it submits the incident it is
*displaying* rather than re-reading one at click time.

## Gaps found, not fixed here

- A missing role template surfaces as `500 INTERNAL_ERROR`
  (`open …/implementor.md.md: no such file`) rather than a mapped code. Every
  other role failure has one. Worth a `ROLE_TEMPLATE_MISSING`.
- `template: "implementor.md"` in a role map silently becomes
  `implementor.md.md`. The loader appends the extension; the config does not
  reject a name that already has one.

## Not covered

- The paused-**dead** cell was not staged live (it needs the agent to die while
  pinned). It is covered by `SessionPausePanel.test.tsx` and by the lifecycle
  tests for `ApplyRuntimeObservation`.
- Frontend suite at the time of this run: **1660 pass / 6 fail**, the six being
  the pre-existing `generate-markdown-twins` (5) and `api-client` rebase (1)
  failures that predate this slice. `tsc --noEmit` clean.
