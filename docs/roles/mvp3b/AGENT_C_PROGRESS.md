# Agent C — desktop UI

## State: COMPLETE — code, tests and all eight catalogs landed; rebased onto the generated schema; typecheck + full suite green

## Next action
Nothing outstanding. If anything changes upstream, the two things to re-check
are (a) `SessionFailoverView` still resolving from
`components["schemas"]["SessionFailoverView"]` after a schema regen, and (b) a
live paused role-pinned worker actually rendering Continue once a daemon is
running — the desktop path has never been exercised against a real
`failover` block, only against the read model's shape.

## Brief

Add a third control beside Resume and Restart agent, without teaching React
anything about harnesses or ladders.

### You own (create/edit only these)

- `frontend/src/renderer/components/SessionPausePanel.tsx` + `.test.tsx`
- `frontend/src/renderer/components/SessionInspector.tsx` + `.test.tsx`
- `frontend/src/renderer/hooks/*` (the session/pause hooks you need)
- `frontend/src/renderer/lib/api-client.ts`
- `frontend/src/renderer/types/*`
- `frontend/src/renderer/i18n/*.json` — **all eight locales**

You must **not** edit anything under `backend/`, including the generated
`frontend/src/api/schema.ts` (Agent B regenerates it).

### Start against a fixture, rebase later

`schema.ts` will not have the `failover` block until Agent B regenerates it.
Define a local fixture type matching contract §9 exactly, build against it, and
tell the orchestrator you are ready to rebase. Do not hand-edit `schema.ts`.

```ts
type SessionFailoverView = {
  available: boolean
  roleId: string
  nextTarget: { harness: string; model: string } | null
  nextRungIndex: number
  attemptsUsed: number
  maxAttempts: number
  incidentId: string
  reason: '' | 'no_role_pin' | 'no_ladder' | 'ladder_exhausted' | 'limit_reached' | 'not_paused' | 'switch_unsupported'
} | null
```

### Implement

1. **State-appropriate, visibly distinct controls**, never collapsed into one
   menu. Your D1 is **confirmed and now frozen in contract §2**:
   - **paused-live** → Resume + Continue. **Restart must be absent** — the
     process is alive, and offering to restart it invites a second runtime.
   - **paused-dead** → Resume + Restart agent + Continue.

   Meanings: **Resume** lifts the pin and starts nothing; **Restart agent** is
   same-harness relaunch for a session whose agent is gone; **Continue with
   `<target>`** takes the next failover rung. The requirement is that the three
   operations never blur into one another, *not* that all three render in every
   state. Continue appears in **both** cells — a live-but-limited agent is
   exactly what failover is for. Label Continue from `failover.nextTarget`
   supplied by the backend — never construct a harness or model name in React,
   and never map a harness id to a target yourself.
2. **Carry the displayed incident id to submission.** Read it from what rendered
   the pause; do not re-read the current pin at click time. That re-read is the
   stale-resume bug, and it is worse on Continue: it would move a session onto a
   rung for an incident nobody looked at.
3. **Both paused cells.** Paused-live and paused-dead (`activity.state`
   distinguishes them) each get the right control set. Continue is offered in
   both.
4. **Disabled states with a reason.** When `available` is false, disable
   Continue and explain using `reason` — every one of the seven values gets a
   localized string. Never render a bare disabled button.
5. **Pending state.** Disable all three controls while a continuation is in
   flight; a double-click must not be able to issue two requests.
6. **Errors.** Surface `PAUSE_INCIDENT_MISMATCH` as "the world moved, re-read"
   (not "retry"), and `FAILOVER_NO_TARGET` / `FAILOVER_LIMIT_REACHED` as
   terminal-but-still-paused. Nothing in the UI may suggest an automatic retry
   or a countdown — `retryAfter` is advisory and nothing schedules against it.
7. **Design.** shadcn primitives from `components/ui/*` where one fits; the
   renderer clones the agent-orchestrator web app verbatim. No new visual
   idioms.
8. **All eight locales** get real strings: `en`, `de`, `es`, `fr`, `ja`, `ko`,
   `pt-BR`, `zh-CN`. No English fallback left in a non-English catalog.

### Tests you must write

- **paused-live renders Resume + Continue, and asserts Restart is ABSENT**
  (`queryByRole("button", {name: "Restart agent"})` is null) — this is a
  regression guard on 3A's existing assertion, not a new preference
- **paused-dead renders Resume + Restart agent + Continue**, three adjacent and
  distinctly named controls
- Continue label comes from `nextTarget`, not from any local mapping
- submitted incident id equals the **displayed** one, even after the underlying
  session prop changes to a newer incident mid-render
- disabled + correct copy for each of the seven `reason` values
- all controls disabled while pending; no double submit
- each error code renders its own message
- locale coverage test across all eight catalogs

### Verify

```bash
cd frontend && npm run typecheck && npx vitest run src/renderer/components/SessionPausePanel.test.tsx src/renderer/components/SessionInspector.test.tsx
```

When showing the change, run `ao preview` from inside the session so it renders
in the desktop browser panel — do not just describe it.

## Done
- [x] Read contract §2/§4/§9, PHASE3A_PAUSE_CONTRACT §1–3, DESIGN.md banner,
      `SessionPausePanel.tsx` + test, `SessionInspector.tsx` (pause region,
      `ResumeAgentControl`), `lib/api-client.ts`, `types/workspace.ts`,
      `hooks/useWorkspaceQuery.ts`, i18n plumbing.

- [x] `types/workspace.ts`: `SessionFailoverReason` / `SessionFailoverTarget` /
      `SessionFailoverView` (contract §9 verbatim) + `toSessionFailover`
      narrowing + `WorkspaceSession.failover`.
- [x] `hooks/useWorkspaceQuery.ts` maps `failover` (now a direct, typed read —
      see D4, the rebase is done).
- [x] `hooks/useRestartAgent.ts` — the resume-agent mutation, shared by the
      Activity control and the pause panel.
- [x] `lib/api-client.ts` — `continue` added to the operator-token path regex
      (D5) and `ApiActionError` so a surface can key copy off the daemon's CODE
      instead of guessing from message text. The temporary `PendingRoutePost`
      seam was removed again by the rebase (D4).
- [x] `SessionPausePanel.tsx` — Continue control, incident carried, both paused
      cells, seven reasons, 18 error codes, pending/disable, no double submit.
- [x] `SessionInspector.tsx` — Activity's Restart hidden when paused, single
      shared mutation, comment updated.
- [x] Eight locale catalogs, 33 keys each (264 strings), inserted textually so
      every untouched line stays byte-identical.
- [x] `npm run typecheck` clean.

- [x] `SessionPausePanel.test.tsx` — 39 tests: control set per paused cell,
      label from `nextTarget` (incl. an unknown harness id and an empty model),
      displayed-id === submitted-id after the prop moves to a newer incident,
      `failover.incidentId` never substituted, all seven reasons, availability
      without a rung, pending/no-double-submit, 10 error codes + fallback + a
      "no retry, no countdown" assertion, eight-locale coverage.
- [x] `SessionInspector.test.tsx` — the triad asserted where it is assembled:
      three adjacent distinct controls on paused-dead, exactly ONE "Restart
      agent" on screen, Continue on paused-live with Restart absent, Activity
      keeping Restart for a merely-exited session.
- [x] Rebased onto Agent B's regenerated `schema.ts` (D4). No fixture type left.
- [x] Verified — see "Verification run".

## Remaining
_(nothing)_

## Decisions / gotchas

### D1 — the paused-live cell keeps **two** controls, not three (RESOLVED: confirmed and frozen in contract §2)
The brief's original test list said "three distinct controls on paused-live and
on paused-dead". That collided with what 3A already landed and asserted:
`SessionInspector.test.tsx` "SessionInspector paused-live cell" says Restart
"has to be **absent**, not merely worded differently" on a live agent, and
`queryByRole("button", { name: "Restart agent" })` must be null there. Frozen
contract §2 requires the three operations stay **distinct**, not that all three
render in every state. Raised with the orchestrator, who confirmed the reading
and froze it: paused-dead → Resume + Restart + Continue (three, adjacent,
distinctly named); paused-live → Resume + Continue, Restart absent because
offering to restart a live process invites a second runtime. Continue appears in
both cells. Implemented and asserted that way from the start, so the ruling
required no code change; the paused-live test is a regression guard on 3A's
assertion, not a preference.

### D2 — Restart moves *into* the pause panel when the session is paused
Contract §2 wants the three controls read as one triad. `ResumeAgentControl`
used to live in the Activity section. Now: the resume-agent mutation is
extracted to `hooks/useRestartAgent.ts`; `SessionPausePanel` renders the Restart
button itself when the agent is dead, and `ResumeAgentControl` returns null when
`session.pause` is set (so there is never a second "Restart agent" button).
Non-paused sessions are untouched — Activity still owns Restart there, which is
what the existing Activity-section tests assert. Extracting the mutation (rather
than duplicating it) is what lets the panel disable Restart while a Continue is
in flight (brief item 5) and keeps the `saved_prompt` notification in one place.

### D3 — Continue submits `pause.incidentId`, never `failover.incidentId`
The panel displays the pin's incident id, so that is what it submits — one
`const` feeds both the `<dd>` and every click handler. `failover.incidentId` is
the preview's incident; if the two ever diverge the daemon answers
`PAUSE_INCIDENT_MISMATCH` and the panel says "re-read", which is the contract's
designed remedy (3A §3). React does not second-guess it with a local staleness
rule. The regression test asserts *DOM-displayed id === submitted id* after the
session prop changes to a newer incident, which is the invariant that actually
matters; freezing the id at mount would keep a stale incident on screen forever.

### D4 — the schema rebase is DONE
Built first against a hand-mirrored fixture type, because `schema.ts` had no
`failover` block yet; the call went through a `PendingRoutePost` cast of
`apiClient.POST` so there was exactly one request seam and the rebase was
"delete the cast". Agent B's regenerated `schema.ts` then landed in the shared
tree carrying both `SessionFailoverView` and `POST /sessions/{id}/continue`, and
it matches contract §9 field-for-field, so the rebase was taken immediately:

- `types/workspace.ts` now derives from the generated schema —
  `SessionFailoverView = NonNullable<components["schemas"]["SessionFailoverView"]>`,
  `SessionFailoverTarget = NonNullable<SessionFailoverView["nextTarget"]>`,
  `SessionFailoverReason = SessionFailoverView["reason"]`. A daemon that grows an
  eighth reason now fails the typecheck at the reason→copy map, which is the one
  place that has to care.
- `useWorkspaceQuery` reads `session.failover` directly (no index cast).
- `SessionPausePanel` calls `apiClient.POST("/api/v1/sessions/{sessionId}/continue", …)`
  with no cast; the generated request type has nowhere to put a target, which is
  contract §3's "structurally impossible" made literal.
- `PendingRoutePost` was deleted from `lib/api-client.ts`.

No fixture type and no rebase marker remain. `schema.ts` was never edited.

### D5 — api-client must send the operator token on `/continue`
Contract §3 reuses `authorizeOperatorPause` verbatim, so the desktop has to
inject `X-AO-Operator-Spawn-Token` on `/continue` exactly as it does on
`/pause` and `/resume`, or every Continue 403s. The privileged-path regex in
`applyOperatorSpawnHeaders` gains `continue`.

`ROUTE_TEMPLATES` in the same file was deliberately NOT touched: `/pause` and
`/resume` are already absent from it, and `fallbackNormalize` yields the
identical `POST /api/v1/sessions/:id/continue` for telemetry, so adding the row
would change nothing. Flagged for the orchestrator as a pre-existing gap between
that list and its "keep in sync with schema.ts" comment — worth one follow-up
that adds pause, resume and continue together rather than a drive-by here.

### D6 — the Continue label is composed, never mapped
`SessionInspector` has a `formatHarnessName()` that turns `kimi` into `Kimi`.
That must **not** touch the Continue label. The label is
`t("inspector.pause.continue", { target })` where `target` is
`nextTarget.model ? "<harness> · <model>" : "<harness>"` — two backend strings
joined for display, no id→name table, no ladder resolution. The test asserts the
raw harness id survives verbatim.

## Verification run so far

All from `frontend/`, after the rebase onto the regenerated `schema.ts`:

| Command | Result |
|---|---|
| `npm run typecheck` | clean (run 3×: pre-rebase, post-rebase, post-`Object.hasOwn`) |
| `npx vitest run src/renderer/components/SessionPausePanel.test.tsx` | 39 passed, 0 failed — repeated 5× consecutively, all green |
| `npx vitest run …SessionPausePanel.test.tsx …SessionInspector.test.tsx src/renderer/i18n` | 134 passed, 0 failed |
| `npx vitest run` (whole renderer suite) | 150 files / 2028 tests passed, exit 0 |

`npm run build` is in AGENTS.md's frontend checklist but does not exist in
`frontend/package.json` (the scripts are `package`/`make`); `typecheck` is the
real gate. Worth someone fixing that line in AGENTS.md.

**One flake, not reproduced.** One full-suite run reported 2027 passed / 1
failed; the summary did not name the test. The identical code then passed the
full suite three more times (150/150 files) and the panel file 5/5, with three
other agents running Go suites on the same machine at the time. Not attributable
to a file I own, but recorded rather than dropped — if it resurfaces in CI it is
pre-existing, not introduced here.

**Not verified live.** No `ao preview` demo: a live paused, role-pinned worker
is needed for the daemon to emit a `failover` block at all, and that path only
exists once A/B/D are integrated. The desktop has been exercised against the
read model's *shape*, not against a running daemon. This is the one gap the
orchestrator should close during integration acceptance (contract §12 items 1
and 2 are exactly this, on both paused cells).
