# Role Pipeline Live Test — 2026-08-07

## Result

**Pipeline completed only with workarounds. Not a clean PASS.**

The requested role pins were preserved throughout:

| Role | Session | Harness / effective model | Outcome |
|---|---|---|---|
| orchestrator | `pelican-pedal-1` | Claude Code / Opus 5, xhigh | coordinated and waited for evidence |
| implementor | `pelican-pedal-5` | Grok / Grok 4.5, high | implemented, live-tested and committed |
| verifier | `pelican-pedal-6` | Codex / `gpt-5.6-sol`, xhigh | ran independently; **not approved** |

The normal path was blocked twice: Grok prompt delivery was routed into a repository-trust
screen, and the Codex verifier's role prompt plus assignment failed at spawn with a generic
internal error. Both were worked around without changing role pins so the remainder of the
pipeline could be exercised.

## Environment

- Checkout: `roles/upstream-sync-2`
- Daemon launch base: `f39ee59b` (the branch advanced while the already-running daemon
  remained live)
- Documentation-time HEAD: `b5071200`
- Renderer: `http://localhost:5173`
- Daemon: `127.0.0.1:3002`
- Isolated data: `/Users/tagtaste/.ao/pelican-smoke-20260807/data`
- Run file: `/Users/tagtaste/.ao/dev/running.json`
- Isolated tmux namespace: `ao-1272b9dd1478`
- Fixture: `/private/tmp/ao-pelican-pedal-20260807`
- Fixture baseline: `247ad6a`
- Implementation commit: `97d546c`

The installed AO instance on port 3001 was not used.

## Test design caveat

The human brief used for this run was too detailed. It specified the decomposition and
acceptance handoff instead of allowing the orchestrator role prompt to expand a small
request. This run therefore proves coordination, failure handling and role fidelity, but
does **not** prove that the orchestrator can expand the intended one-sentence brief by
itself. The reusable test now fixes this by requiring the brief:

> Build a tiny Pelican Pedal browser game. Coordinate implementation and verification.

## Successful evidence

- Claude reported Opus 5 with xhigh effort and stayed in the coordinator role.
- Claude did not implement code, substitute harnesses or launch verification early.
- After Grok trust was approved, the same pinned role and same brief succeeded as
  `pelican-pedal-5`; no repin was required.
- Grok created exactly the three contracted product files and committed them as
  `97d546c`; its worktree was clean and fixed fixture files were unchanged.
- Independent `node --test` runs reported 4 tests, 4 passed, 0 failed.
- Live preview registered on the isolated daemon and demonstrated score reaching 2,
  collision/game-over, restart, keyboard/button input and a narrow inspector layout.
- Claude inspected commit scope and cleanliness before starting verification.
- Codex ran as the read-only `verifier` role in a separate session and received the
  implementor session id, branch, worktree and commit rather than an unverified summary.

## Errors and recoveries

| # | Error / observation | Impact | Recovery / status |
|---:|---|---|---|
| 1 | Claude's first implementor brief exceeded AO's 4 KiB spawn-prompt limit. | Initial role spawn was refused. This was a transport limit, not Claude context exhaustion. | Claude condensed the brief while retaining the required acceptance structure and retried. |
| 2 | Grok opened a repository-trust screen containing the generic text `Grok Build`; AO treated that as prompt readiness and pasted the assignment into it. An `n` in the brief selected “No, quit.” | `pelican-pedal-2`, `-3` and `-4` exited before receiving the task; no Grok conversation or implementation was created. | Trust was approved only for the disposable fixture, then the same pinned Grok role succeeded as `pelican-pedal-5`. The readiness matcher remains a product defect. |
| 3 | Attaching the Grok terminal was initially suspected as the fix, but an attached retry exited on the same ~200 ms timeline. | Produced a red-herring diagnosis and an impractical race with UI event latency. | Direct isolated tmux probes proved detached Grok stays alive and isolated the trust screen as the real cause. |
| 4 | Desktop “Restart agent” on the exited Grok session recreated a bare shell and briefly appeared idle while the durable activity remained exited; the assignment was not restored. | Process recovery did not recover task delivery and could mislead the user about worker state. | Session was terminated; a fresh role-pinned spawn was used after trust approval. |
| 5 | Grok's first two browser inspection commands timed out after 45 seconds. | Visual verification was delayed and the worker had to probe alternate browser commands. | Fallback browser commands succeeded; AO's native Browser tab also rendered the preview. |
| 6 | Browser automation intermittently reported non-interactable/timing-sensitive controls. | The worker could not initially prove scoring/restart from its first automated sequence. | It used accessible controls and careful timing, then recorded score 2, game-over and restart. Treat as a browser-driver/tooling residual unless reproduced by normal user input. |
| 7 | The normal Codex verifier spawn failed twice with generic `INTERNAL_ERROR`; no partial session was created. Claude isolated the trigger as the verifier's roughly 11.6 KiB host prompt plus the task brief. | The normal role-pinned spawn-with-prompt path did not work, and the API did not return a typed prompt-too-long error. | Claude spawned `pelican-pedal-6` without a task prompt, confirmed it alive, then delivered the full brief with `ao send`. This is a diagnostic workaround, not a pass for the normal path. |
| 8 | Codex paused on a local codebase-memory MCP approval. | Verification stopped at `waiting_input`. | “Allow for this session” was selected in the native desktop UI. |
| 9 | The verifier's first graph lookup used the friendly project name and returned “project not found or not indexed,” while the indexed repository used a path-derived name. | Read-only code discovery took an extra step; core command checks had already completed. | Verifier continued with the available indexed project/fallback discovery. |
| 10 | Repeated non-blocking `nvm` warnings reported `npm_config_prefix=/opt/homebrew` during verifier shell commands. | No command failure; noisy evidence. | Recorded for environment cleanup; verification continued. |
| 11 | Electron dev logs repeatedly emitted an xterm `dimensions` TypeError. | No observed terminal-data loss in this run, but it is a recurring frontend error. | Open; record separately if reproducible outside the dev build. |
| 12 | A project added through the API was not immediately visible in the already-open renderer. | Native UI did not show the fixture until the dev app was restarted. | Electron was restarted once; isolated daemon/data remained intact. |
| 13 | The read-only Codex verifier could not reach AO's loopback daemon. Both `ao preview index.html` and `ao browser status` failed with `dial tcp 127.0.0.1:3002: connect: operation not permitted`. | Codex could independently verify the commit, clean scope, fixed tests and static acceptance facts, but could not perform the required live-browser pass. | Open. The verifier must report the browser criteria as unverified rather than inherit Grok's self-report. This leaves the end-to-end verifier gate incomplete. |
| 14 | The verifier found that the delivery expected to open directly from disk uses an external ES module. Standard browsers normally block that loading mode under `file://`. | The implementation may satisfy tests and AO-hosted preview while failing the fixture's direct-disk acceptance criterion. | Confirmed by the verifier and then independently accepted by the orchestrator as a high-severity defect. Route back to the implementor in a future run after deciding the module-versus-direct-disk contract. |

## Current verifier state

Codex verifier session `pelican-pedal-6` definitely ran and returned **not approved —
overall FAIL**. It independently checked the clean tree, fixture diff, commit history,
product-file scope, dependency/network absence, and fixed test suite. The tests reported
4 passed and 0 failed. It found the direct-disk module-loading deviation and attempted the
required AO live-browser pass, but its read-only sandbox refused access to the loopback
daemon. Keyboard, scoring, collision, restart and narrow-layout behavior therefore remain
unverified by the verifier, even though the implementor demonstrated them earlier.

The verifier made no workspace edits. The orchestrator waited for the verifier, rechecked
the clean worktree and two-commit history, read the verdict, independently confirmed the
direct-disk concern, and produced a final outcome that did not claim completion. It
recommended returning the defect to the implementor and separately making live-browser
verification reachable from the verifier harness.

## Cleanup result

Cleanup completed after both role stages and the orchestrator's final synthesis:

- the foreground dev Electron process and daemon PID 69884 stopped;
- no listener remained on port 3002;
- tmux servers `ao-1272b9dd1478` and `ao-grok-probe-20260807` were terminated and
  confirmed absent;
- `/Users/tagtaste/.ao/pelican-smoke-20260807` was removed;
- `/private/tmp/ao-pelican-pedal-20260807` was removed;
- the isolated run file had already been removed by normal shutdown.

The installed AO instance on port 3001 and unrelated tmux servers were not stopped,
enumerated, or altered.
