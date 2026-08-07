# Agent C — desktop UI

## State: not started

## Next action
Read `docs/roles/PHASE3B_MVP_CONTRACT.md` §2/§9, `DESIGN.md`'s
"clone agent-orchestrator verbatim" banner, then
`frontend/src/renderer/components/SessionPausePanel.tsx` and its test.

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

1. **Three visibly distinct controls**, never collapsed into one menu:
   - **Resume** — lifts the pin, starts nothing
   - **Restart agent** — same harness, for a session whose agent is gone
   - **Continue with `<target>`** — the next failover rung
   The pause contract's whole point is that Resume must never imply Restart, and
   Continue must never look like either. Label Continue from
   `failover.nextTarget` supplied by the backend — never construct a harness or
   model name in React, and never map a harness id to a target yourself.
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

- renders three distinct controls on paused-live and on paused-dead
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
_(nothing yet)_

## Remaining
- [ ] Read contract §2/§9 + DESIGN.md banner + SessionPausePanel
- [ ] Fixture type + api-client method
- [ ] Continue control beside Resume / Restart agent
- [ ] Incident id carried from display to submit
- [ ] Paused-live and paused-dead states
- [ ] Seven disabled reasons + error copy
- [ ] Pending/disable handling
- [ ] Eight locale catalogs
- [ ] Tests green + typecheck clean
- [ ] Report ready-to-rebase onto generated schema.ts

## Decisions / gotchas
_(record anything a cold reader would need)_

## Verification run so far
_(none)_
