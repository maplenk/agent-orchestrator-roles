# Agent D — the Restart-assignment defect

## State: DONE — verified by the orchestrator and committed

## Next action
None. Integrated in wave-2 position 2. If reopened, the seam to watch is the
post-`MarkSpawned` fence against Agent A's `ForceGenerationID` change to
`switch.go` — they do not overlap today (A touches `switch.go` and new files;
D touches `manager.go`'s relaunch region), but both reason about generation
identity around launch.

## Orchestrator verification (2026-08-08)
`go test ./internal/session_manager/ -run 'Restore|Resume|Restart'` → **102
passed**. The `go build ./...` failure Agent D reported was Agent B's in-flight
`ctx.continueSession` in the shared checkout, not a defect in this slice; it is
resolved and the tree builds. D reported it rather than editing the CLI, which
was the correct call under the ownership rule.

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
- [x] Read the durable Agent D brief and confirmed the strict file ownership.
- [x] Read the pause contract: pause and runtime liveness are independent facts; Restart must remain a separate explicit act from Resume.
- [x] Read `AGENTS.md`; preserve durable/observed facts, and never infer death from failed or unknown runtime probes.
- [x] Read `RestoreWithMode`, `ResumeAgentWithMode`, `relaunchSession`, prompt readiness/delivery, and `freshLaunchArgv`.
- [x] Located the premature report: `freshLaunchArgv` sets `RestoreModeSavedPrompt` when a saved prompt merely exists, before after-start delivery runs.
- [x] Finalized the manager-only fence: fresh argv construction returns a provisional `fresh` mode; relaunch promotes it to `saved_prompt` only after the selected delivery path succeeds. Command-delivered fallback additionally rechecks a generation-scoped supervised workload after `MarkSpawned`; only a confirmed dead result fails the launch, while unsupported/failed probes are not death conclusions.
- [x] Implemented outcome-based mode promotion and actionable delivery-failure errors in the owned relaunch path.
- [x] Implemented the post-`MarkSpawned` command-delivery fence and shared two-sample supervised-workload probe; after-start delivery remains fail-closed on an unknown probe, while command delivery logs and preserves unknown as unknown.
- [x] Added `restart_contract_test.go` with the six required restart-contract tests, including a failed-probe subcase proving unknown liveness is not death.
- [x] Ran the new restart-contract tests: all pass.
- [x] Ran `go test ./internal/session_manager/ -run 'Restore|Resume|Restart'`: pass, including the existing Codex/OpenCode/Claude Code fallback family.
- [x] Ran `go build ./...`: blocked by another agent's in-flight forbidden-layer change in `internal/cli/session.go:252` (`ctx.continueSession undefined`). Per ownership, did not edit CLI code.
- [x] Ran the full `go test ./internal/session_manager/` package once: pass.

## Remaining
- [x] Read pause contract §1 + ResumeAgentWithMode + RestoreWithMode
- [x] Locate the premature `saved_prompt` report
- [x] Make mode describe the outcome
- [x] Exactly-once assignment restore
- [x] Launch-window observation fence
- [x] Honest delivery-failure error
- [x] Six tests above
- [x] Restore/Resume package tests green

## Decisions / gotchas
- Code graph access was attempted first as required, but both codebase-memory and context MCP reads were denied by the current permission mode; continue with scoped `Grep`/`Read` only.
- `RestoreModeSavedPrompt` is currently an attempted-mode from `freshLaunchArgv`; for after-start adapters it must remain non-success until `deliverAfterStartPrompt` returns nil. In-command delivery is part of the launched argv and may be reported only after launch adoption plus the supervised startup fence.
- The fence belongs after `MarkSpawned`: that lets it repair the exact race where an earlier launch-death observation was overwritten by the idle seed. A dead result parks the failed relaunch; an unsupported or failed probe is not converted into proof of death.

## Verification run so far
- `go test ./internal/session_manager/ -run '^TestRestartContract_'` — pass
- `go test ./internal/session_manager/ -run 'Restore|Resume|Restart'` — pass
- `go build ./...` — blocked outside Agent D ownership: `internal/cli/session.go:252:15: ctx.continueSession undefined`
- `go test ./internal/session_manager/` — pass
