# Role Pipeline Live Test

## Purpose

Exercise the user-visible role pipeline with a deliberately small human brief:

> Build a tiny Pelican Pedal browser game. Coordinate implementation and verification.

The brief must stay small. The test is intended to show whether the orchestrator's
host-authored role prompt expands the work into an implementor assignment, waits for
evidence, and routes an independent verification pass. Do not preload the orchestrator
with a detailed implementation or verification plan.

This is a manual live dogfood test. Run it after changes to role resolution, spawn
prompt composition, prompt delivery, terminal readiness, session recovery, or the
orchestrator/implementor/verifier desktop flow.

## Required role map

Use a strict project for this harness combination. Strictness enforces durable
role identity, host-owned routing and delegation; it does not implicitly make
the orchestrator read-only. The verifier remains explicitly read-only and must
therefore use an enforcing harness:

| Role | Harness | Model | Workspace writes | Can spawn |
|---|---|---|---:|---:|
| orchestrator | claude-code | opus | yes | yes |
| implementor | grok | provider default | yes | no |
| verifier | codex | provider default | no | no |

The live harnesses should report their effective models in their own UI. Record those
models in the evidence log rather than assuming the configured alias names the runtime
model.

## Fixture

Create a disposable Git repository outside the AO source checkout with:

- a short `README.md` defining the tiny game and its acceptance criteria;
- fixed dependency-free tests that fail before implementation;
- no implementation files;
- a committed clean baseline.

Keep the fixture small enough that the human brief can remain one sentence. The
orchestrator may read the fixture and construct detailed worker/verifier briefs itself.

Minimum acceptance criteria:

1. The implementor produces a playable dependency-free browser game.
2. The fixed tests pass without being edited.
3. Live preview proves input, score, collision/game-over, restart, and a narrow layout.
4. The implementor commits only the contracted product files and leaves a clean tree.
5. The read-only verifier reruns the tests and checks the live behavior independently.

## Isolation

Use a fresh `AO_DATA_DIR`, run file, daemon port, Electron profile, project id, and tmux
namespace. Do not open an existing AO data directory. Record all exact paths and PIDs in
the evidence log so cleanup can be scoped.

## Procedure

1. Record the AO branch and commit used to launch the daemon.
2. Start the real Electron desktop app against the isolated data directory.
3. Register the fixture and role map.
4. Spawn the role-pinned orchestrator and confirm Claude reports Opus 5.
5. Send only the one-sentence human brief above.
6. Observe without supplying an implementation plan:
   - the orchestrator inspects durable project/role state;
   - it creates a bounded implementor brief;
   - it spawns only the Grok implementor role;
   - it waits for durable evidence and does not implement the task itself.
7. If a repository-trust prompt appears, AO must not paste the assignment into that
   prompt. Record whether AO waits for human approval, fails closed, or misroutes input.
8. Confirm the implementor receives the full brief, produces the fixture result, runs
   tests and live preview, commits, and becomes idle with a clean tree.
9. Confirm the orchestrator independently checks scope and commit state before spawning
   the verifier.
10. Spawn the Codex verifier with its full brief in the normal `ao spawn --role` call.
    A launch-size failure or generic internal error is a test failure; a separate empty
    spawn plus `ao send` may be used only to continue diagnosis and must be recorded as a
    workaround.
11. Confirm the verifier is read-only, runs in its own session, tests the implementor's
    worktree, does not edit it, and reports an evidence-backed verdict.
12. Confirm the orchestrator reads the verifier result and gives a final user-facing
    outcome without relying only on worker self-report.

## Evidence to record

- AO branch, launch commit, current commit, port, isolated paths and tmux namespace.
- Project id and exact role/harness/model bindings.
- Every session id, role, effective model, terminal state and termination state.
- Fixture baseline commit and implementation commit.
- Literal test pass/fail summary, clean-tree result and fixture-integrity result.
- Live preview observations for input, score, collision, restart and narrow layout.
- Each prompt/transport/readiness/permission/tooling/limit error, including the API code,
  whether a partial session was created, and the recovery used.
- Whether the orchestrator expanded the one-sentence brief without human-authored task
  decomposition.
- Cleanup result proving the isolated daemon, Electron process, tmux server, data
  directory and fixture were removed while the normal AO instance was untouched.

## Pass criteria

The run passes only when the normal role-pinned path completes:

`human brief -> Claude orchestrator -> Grok implementor -> Codex verifier -> orchestrator verdict`

Workarounds may let diagnostic work continue, but they do not convert the affected gate
to PASS. Any generic 500, lost prompt, prompt delivered into a trust/permission screen,
untracked partial session, verifier edit, or automatic harness substitution is a failure.
