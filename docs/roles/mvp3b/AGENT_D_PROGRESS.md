# Agent D — the Restart-assignment defect

## State: not started

## Next action
Read `docs/roles/PHASE3A_PAUSE_CONTRACT.md` §1, then
`Manager.ResumeAgentWithMode` and `RestoreWithMode` in
`backend/internal/session_manager/manager.go`, and find where
`RestoreModeSavedPrompt` is reported relative to where the prompt is actually
delivered.

## Brief

This is not a failover task. It is the defect the MVP's paused-dead state
exposes: **Restart agent** is the only way back for a session whose agent died
while paused, and today it can report success on a delivery that did not happen.

### You own (create/edit only these)

- the relaunch/restore delivery path inside
  `backend/internal/session_manager/manager.go` — the restore / resume-agent
  region only
- runtime-observation fencing around launch, in that same region
- new restart-contract tests (a new `restart_contract_test.go` is fine)

You must **not** create or edit any `failover*` file, the switch saga
(`switch.go`), `service/`, `httpd/`, `cli/`, or `frontend/`. Agent A is editing
the failover files and the switch seam at the same time as you.

If a fix genuinely requires touching `switch.go`, stop and report the exact
seam — the orchestrator resolves it at integration.

### Fix

1. **Stop reporting `saved_prompt` before delivery succeeds.** `RestoreResult.Mode`
   must describe what happened, not what was attempted. A restart that failed to
   deliver the assignment must not return a mode that says it replayed it.
2. **Restore the original assignment exactly once.** Not zero times (silent
   loss), not twice (a duplicated task prompt is a second instruction to the
   agent).
3. **Do not let launch-time death be overwritten as idle.** A runtime that dies
   during launch must not have its observation clobbered by a later "idle"
   reading — fence the observation around the launch window. This is the same
   class of bug as the paused-liveness one boot already guards.
4. **Surface delivery failure honestly.** An error the caller can act on, not a
   success with a misleading mode. Follow the wording standard of
   `ErrPromptNotReady`'s mapping: say what happened to the task, what happened
   to the session, and what has to change before a retry differs.

### Tests you must write

- branchless (scratch) session restart restores the assignment exactly once
- worktree session restart restores the assignment exactly once
- delivery failure → error surfaced, mode does **not** claim `saved_prompt`
- launch-time death is not overwritten by a subsequent idle observation
- a **paused-dead** session's restart is honest about both the pause and the
  delivery (this is the seam the MVP depends on)
- no regression: `TestRestore_*FallsBackToSavedPrompt` family still passes

### Verify

```bash
cd backend && go build ./... && go test ./internal/session_manager/ -run 'Restore|Resume|Restart'
```

Then the full package once, accepting that Agent A's in-flight `failover*.go`
files may break the build — if they do, report it rather than fixing their code.

## Done
_(nothing yet)_

## Remaining
- [ ] Read pause contract §1 + ResumeAgentWithMode + RestoreWithMode
- [ ] Locate the premature `saved_prompt` report
- [ ] Make mode describe the outcome
- [ ] Exactly-once assignment restore
- [ ] Launch-window observation fence
- [ ] Honest delivery-failure error
- [ ] Six tests above
- [ ] Restore/Resume package tests green

## Decisions / gotchas
_(record anything a cold reader would need)_

## Verification run so far
_(none)_
