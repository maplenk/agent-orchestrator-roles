# Absent tmux server as a typed liveness fact

**Implementation:** `6a07d5d6` (adapter classification) and `24906d35`
(consumer-specific recovery)

**Acceptance status:** code, review, and the targeted default-data-dir live
replay are complete. The prior namespaced-socket dogfood remains historical;
the supplemental default-socket record ran on exact runtime head `3c3aef51`.

## Why the classification is typed

tmux panes live inside the tmux server process. Literal `no server running`
therefore proves that the server, and every pane it hosted, is absent. It is a
server-wide fact, not N independent per-session answers. Returning
`(false, nil)` for every handle would let a steady board probe turn one server
loss into mass session termination — the issue #3475 failure shape.

`tmux.Runtime.IsAlive` instead returns an error wrapping
`ports.ErrRuntimeServerAbsent`. That sentinel itself wraps
`ErrRuntimeUnavailable`, so consumers that know only the older, broader error
remain fail-closed. A reviewed consumer must explicitly opt into the narrower
fact before it may launch, restore, or conclude cleanup.

The classification applies to both socket kinds. The normal/default data
directory deliberately uses tmux's default socket (`SocketForDataDir` returns
an empty socket name there), so restricting the fact to namespaced sockets made
the original paused-dead fix inert in the configuration users actually run.

## Adapter rule

| Probe outcome | Adapter result | Meaning |
|---|---|---|
| `has-session` succeeds | `true, nil` | session is alive |
| `can't find session` / `session not found` | `false, nil` | responding server says this session is absent |
| literal `no server running`, namespaced socket | error wrapping `ErrRuntimeServerAbsent` | server-wide authoritative absence |
| literal `no server running`, default socket | error wrapping `ErrRuntimeServerAbsent` | same server-wide fact on the normal install path |
| `error connecting` | error wrapping `ErrRuntimeUnavailable` only | permission/stale-socket reachability failure; no liveness answer |
| other stderr, non-`ExitError`, malformed handle | ordinary error | no liveness answer |

Only the literal absence text is classified. `error connecting` remains
uncertain even when it mentions `No such file or directory`, because the same
shape covers permission failures and stale sockets.

## Consumer policy

The sentinel has different safe meanings at different boundaries; there is no
global “server absent means mark every row dead” rule.

| Consumer | `ErrRuntimeServerAbsent` behavior | Why safe |
|---|---|---|
| Restart Agent (`restartRuntime`) | treat old runtime as absent and create one replacement | a pane cannot survive the absent server; all other probe errors still refuse a second launch |
| Boot live reconciliation (`reconcileLive`) | enter the existing save/teardown/restore path | boot is recovering a runtime that could not have survived; ambiguous reachability still leaves the row untouched |
| Boot terminated-row reap (`reconcileReap`) | conclude there is no leaked runtime to collide with restore | the row is already terminated and the missing server cannot host its old pane |
| Switch/failover teardown (`destroyRuntimeProbed`) | after targeting the handle for destruction, accept absence as confirmed teardown | the effectful saga is resolving one already-selected source handle |
| Steady board reaper (`observe/reaper.Tick`) | keep `ProbeFailed`; never convert the error to `ProbeDead` | one server-wide event must not archive the board as N independent deaths |

The steady reaper also retains its mass-death circuit breaker
(`massDeadMinSessions = 5`, `massDeadFraction = 0.5`) for ordinary
`(false, nil)` session answers. Typed server absence does not consume or bypass
that breaker; it remains an error in this path.

## What the tests pin

Adapter tests use a real `*exec.ExitError`, because stderr classification runs
only when `errors.As` finds that type. They cover:

1. literal absence on default and namespaced sockets wraps
   `ErrRuntimeServerAbsent` and, transitively, `ErrRuntimeUnavailable`;
2. a missing session on a responding server remains `false, nil`;
3. `error connecting`, permission errors, stale sockets, unknown output, and
   malformed handles never become death;
4. restart creates one replacement on typed default-socket absence;
5. boot live reconciliation saves and restores once on the same boot;
6. boot reap permits restore without trying to destroy a server that is gone;
7. switch teardown accepts typed namespaced-server absence; and
8. the steady reaper classifies both `ErrRuntimeServerAbsent` and ordinary
   `ErrRuntimeUnavailable` as inconclusive.

## Live evidence

The promoted matrix in [`FINAL_LIVE_ACCEPTANCE.md`](FINAL_LIVE_ACCEPTANCE.md)
ran before this typed default-socket correction and remains a separate dated
evidence set. The supplemental replay used an isolated `HOME` with
`AO_DATA_DIR` and `AO_RUN_FILE` unset, explicit profiles, an isolated
`TMUX_TMPDIR`, and tmux's actual default socket on exact `3c3aef51`.

The record proved:

- a fresh no-server boot reached ready state;
- after spawning one row/runtime, killing the exact sole server and choosing
  Restart Agent returned 200/native with one replacement;
- boot adopted a surviving live runtime without duplication;
- server loss for an active row reconciled and restored exactly once;
- a terminated saved row passed `reconcileReap` plus `RestoreAll`, consumed its
  marker, and ended as one active row with one runtime; and
- no boot/reconcile errors occurred (only expected GitHub/SCM warnings).

Focused sentinel tests for tmux, session manager, and steady reaper passed under
`-race`. The subsequent `663f9339` and `72f274a7` integration commits touch only
storage and service behavior, not the runtime classification or its consumers.
