# MVP acceptance agent progress

**Branch:** `codex/mvp-promoted-evidence`

**Frozen evidence SHA:**
`166e9e6338d0230b1a9c9f5c4976cf79789df188`

**Scope:** adversarial review, isolated acceptance tooling, and promoted live
evidence. The final evidence run did not edit production or the frozen runner
and never used the default AO data directory.

## Current status

| Work | Status |
|---|---|
| Frozen contracts read | complete |
| `IsAlive` consumer inventory | complete |
| P1 defect report and proposed fix | complete |
| Worker records 1-12 harness | complete |
| Orchestrator switch/recovery harness | complete |
| Harness self-tests | complete |
| Final worker records 1-12 on frozen SHA | **pass** |
| Final orchestrator matrix on frozen SHA | **pass** |
| Sanitized promoted evidence | **complete** |

## Final acceptance checkpoint

The complete promoted result is
[`FINAL_LIVE_ACCEPTANCE.md`](FINAL_LIVE_ACCEPTANCE.md). All live scenarios ran
sequentially from fresh roots against the immutable detached checkout at
`166e9e63`; the evidence branch is based on that exact commit.

The isolated kit lives under `test/mvp-acceptance/` with its one live Go probe
under `backend/test/mvp_acceptance/`. It provides:

- a marked, exact-path environment guard that refuses the default data/run
  paths, isolates provider configuration under an exact root-scoped `HOME`,
  and accepts only an explicit root under `/tmp` or `/private/tmp`;
- a checkout-built AO binary, fixed loopback port, data-directory tmux
  namespace, deterministic Codex/Claude stubs, and scoped tmux fault wrapper;
- instructions and assertions for all twelve worker records, including honest
  namespaced-server exit, terminal pre-stop, recoverable `post_stop`, passive
  worker boot, same-generation operator recovery, atomic requested-attempt
  re-drive, live duplicate fencing, ladder exhaustion, manual mode, and the
  executable capability registry check;
- strict orchestrator Codex -> Claude -> Codex, unauthorized-target no-effect,
  same-harness fresh, API and mux input fencing, automatic orchestrator
  `post_stop` boot recovery, one-runtime, one-owner, worker-address, and
  identity/template/permission/workspace preservation assertions; and
- sanitized evidence snapshots with the exact code SHA, AO data directory,
  daemon PID/port, project/prompt hashes, durable session facts, attempts,
  ordered ledger rows, active owners, and real tmux namespace state. Operator
  and spawn credentials are never exported.

### Self-test evidence

- `bash -n` and `git diff --check`: clean.
- `go test ./test/mvp_acceptance -count=1 -v`: pass; capability record passes
  and the live mux probe correctly skips without its explicit isolated URL/ID.
- Environment positive/negative checks: a marked `/private/tmp` root is
  accepted; unset/default AO paths are rejected.
- Root-scoped current-checkout binary build: pass.
- Provider controls: deterministic launch and exit events observed without
  recording prompt text.
- Real tmux wrapper: `keep-destroy` rejected both destroy commands while a real
  namespaced `has-session` proved the source alive; exact cleanup then removed
  it. `fail-create` prevented target creation and `delay-destroy` held the live
  fence for its requested interval.

### Promoted live result

- Worker records 1-12: pass.
- Strict orchestrator Codex→Claude→Codex: pass.
- Pending API and live-mux input fences: pass.
- Injected target-create failure, daemon SIGKILL, and same-generation recovery
  with exact `requested,pre_stop,post_stop,failed,target_ack`: pass.
- Unauthorized-target no-effect: pass.
- Same-harness Fresh with one handoff, one owner/runtime, and no stacking: pass.
- Twenty sanitized snapshots all identify the full frozen SHA. Record 12's
  capability matrix passed as a separate Go test.

The earlier review below is retained as historical context. Its P1 defects were
fixed before the frozen candidate; it is not the promoted evidence base.

## Historical P1: `ErrRuntimeUnavailable` became death outside the adapter

The adapter contract is sound at `be4321d1`: only a missing session on a live
server or literal `no server running` on an AO-namespaced socket returns
`(false, nil)`. `error connecting`, default-socket absence, permission errors,
and unknown output remain errors.

Two manager consumers violate that contract:

1. `session_manager.restartRuntime` (`manager.go:2348-2368`) converts every
   `ports.ErrRuntimeUnavailable` into `alive=false`, then may call `Create`.
   A tmux `error connecting` response therefore permits a second runtime even
   though the old runtime may still exist.
2. `session_manager.reconcileLive` (`manager.go:2491-2568`) converts the same
   sentinel into `alive=false`, then runs save-and-teardown and `RestoreAll`.
   The boot loop applies that decision independently to every active session,
   with no reaper-style mass-death breaker. A shared socket permission or stale
   socket failure can therefore mutate and relaunch a whole board without one
   authoritative death answer.

This is a P1 because `ports.ErrRuntimeUnavailable` is semantically broader than
absence. The tmux adapter intentionally uses it for `error connecting`.

### Smallest fail-closed production fix (for the owning agent)

Remove the `ErrRuntimeUnavailable` special cases from both consumers. Any
non-nil `IsAlive` error must stop the operation; only `(false, nil)` permits a
create, save-and-teardown, or relaunch.

No new authoritative-absence sentinel is needed. `be4321d1` already expresses
authoritative namespaced absence through the existing `(false, nil)` channel.
Adding another typed absence signal would create a second death channel and
make future consumer drift more likely. Default-socket absence deliberately
remains uncertain under the frozen classification and must not be recovered by
coercing `ErrRuntimeUnavailable`.

### Exact regression tests requested

- `restartRuntime`: `IsAlive -> (false, ErrRuntimeUnavailable)` returns a probe
  error and calls none of `Restart`, `Destroy`, or `Create`.
- `restartRuntime`: `IsAlive -> (false, nil)` still creates exactly once.
- `reconcileLive`: `IsAlive -> (false, ErrRuntimeUnavailable)` leaves the row,
  worktree, restore marker, pause/activity facts, and runtime handle unchanged.
- full `Reconcile`: several active sessions returning
  `ErrRuntimeUnavailable` cause zero teardown, marker, termination, restore, or
  runtime-create effects across the board.
- full `Reconcile`: authoritative `(false, nil)` still follows the intended
  dead-runtime recovery path.
- adapter/manager composition: literal namespaced `no server running` reaches
  the authoritative path, while `error connecting` and default-socket absence
  reach the fail-closed path.

## Full `IsAlive` consumer audit

| Consumer | Verdict at `be4321d1` |
|---|---|
| switch `destroyRuntimeProbed` | safe: probe error is `SWITCH_UNCERTAIN`; only `(false,nil)` proves death |
| switch post-stop recovery | safe: probe error blocks; matching live generation acks; dead relaunches same generation |
| failed-launch/superseded cleanup | safe: both reuse `destroyRuntimeProbed` |
| interface-transition drain | safe: probe errors do not prove exit; transition remains draining or times out |
| interface-transition conclusive stop | safe: error enters recovery and target is not launched |
| reaper `probeOne` | safe: errors become `ProbeFailed` |
| reaper `Tick` | safe: >=5 and >50% dead is downgraded to `ProbeFailed` before observations apply |
| boot `reconcileLive` | **P1 unsafe**: `ErrRuntimeUnavailable` is coerced to dead |
| boot `reconcileReap` | safe: uncertainty keeps the terminated row/runtime untouched |
| explicit restore/restart `restartRuntime` | **P1 unsafe**: `ErrRuntimeUnavailable` is coerced to dead |
| shell-terminal list/reap/close | safe: errors keep rows; only confirmed dead deletes |
| terminal attachment | safe: errors retry/fail; only confirmed dead marks exited |
| reviewer cancel verification | safe: probe error preserves the running review state |
| tmux create/restart verification | safe: errors fail the operation; confirmed dead fails readiness |

A literal missing namespaced server can affect every session because the socket
is per data directory. Outside the reaper, boot recovery and pending-switch
recovery may consequently process several sessions. For a genuine
`(false,nil)` answer this does not create a duplicate-runtime ambiguity: all
source panes are authoritatively gone, paused failovers remain passive, switch
recovery keeps its generation, and orchestrator recovery holds the project
ownership gate. The unsafe board-wide path is the broader
`ErrRuntimeUnavailable` coercion described above.

## Documentation inconsistency noted

Migration `9008_session_failover_attempts.sql` still says a `post_stop` attempt
may be completed by boot recovery. The frozen contract and current `Reconcile`
code make paused failover boot recovery passive. The migration is immutable and
must not be edited; current acceptance evidence follows the frozen contract.
