# Absent tmux server as an authoritative liveness answer

**Change:** `tmux.Runtime.IsAlive` now returns `(false, nil)` — confirmed dead —
when the probe fails with tmux's `no server running` **and** the runtime is on a
namespaced (AO-owned) socket. Everything else is unchanged.

**Why:** tmux panes live inside the server process. If that process does not
exist, no pane it hosted can be alive. Reading that as inconclusive left a
session whose agent had exited **permanently un-switchable**: every Continue,
switch and recovery answered `SWITCH_UNCERTAIN` forever. Phase 3B is the first
feature to depend on this, because **paused-dead is a first-class MVP state**
and an exited agent is the ordinary way a session reaches it.

## The rule

| Probe outcome | Classification | Rationale |
|---|---|---|
| `has-session` succeeds | **alive** | unchanged |
| `can't find session` / `session not found` | **dead** `(false, nil)` | unchanged; the server answered |
| `no server running`, **namespaced** socket | **dead** `(false, nil)` | **new.** The server AO owns does not exist, so its panes do not either |
| `no server running`, **default** socket | uncertain (`ErrRuntimeUnavailable`) | that server is shared with the human's own tmux; its absence is not a fact about AO's sessions |
| `error connecting` (any socket) | uncertain | permission denied / stale socket is a *reachability* failure, not an answer |
| any other stderr, non-`ExitError`, malformed handle | uncertain / error | unchanged |

Two deliberate narrowings beyond the brief:

1. **Only the literal absence message counts.** `serverUnreachableOutput` still
   matches `error connecting` too, but that is a failure to obtain an answer, not
   an answer. It keeps returning `ErrRuntimeUnavailable`.
2. **Only namespaced sockets.** `SocketForDataDir` returns `""` for the default
   data directory *on purpose* (continuity for an existing install's panes), so
   the default server is shared with whatever tmux the human runs.

`destroyRuntimeProbed` is conceptually unchanged: only `(false, nil)` proves
death. The classification moved into the adapter, which knows tmux's stderr
vocabulary — `session_manager` does no string matching.

## Blast radius, stated plainly

The socket is per **data directory**, not per session. One namespaced server
hosts every session of a daemon, so if that server dies, this change confirms
death for **all of them at once**. That is factually correct — those panes are
genuinely gone — but it is the shape of issue #3475, where "a killed tmux server
read as 28 session deaths archived the whole board".

Two things bound it:

- **The reaper's mass-death circuit breaker** (`reaper.go`, `massDeadMinSessions
  = 5`, `massDeadFraction = 0.5`) already downgrades any pass where ≥5 sessions
  and >50% of the board conclude dead into `ProbeFailed`. That is #3475's actual
  guard, it sits above the adapter, and this change does not touch it. Boards
  below the threshold get exact behaviour, which is the documented intent.
- **Uncertainty still never becomes death.** A probe *error* remains
  `ProbeFailed` at the reaper and `SWITCH_UNCERTAIN` at the saga. Only an
  authoritative absence answer changed meaning.

## Tests

In `internal/adapters/runtime/tmux/liveness_absence_test.go`:

1. missing namespaced server → confirmed dead
2. missing session on a live server → confirmed dead (pre-existing, pinned)
3. unexpected failure (`error connecting` ×2, unknown stderr, empty) → uncertain
4. absent **default** server → uncertain, `ErrRuntimeUnavailable`
5. existing session → alive
6. malformed handle → error, not a death verdict
7. `killSessionMissingOutput` stays generous — teardown must still treat both
   absence and unreachability as nothing-to-kill

In `internal/observe/reaper/reaper_test.go`:

8. an unreachable runtime stays `ProbeFailed`, never `ProbeDead` — the half of
   the split that must not move
9. (pre-existing) `TestTick_MassDeathPassIsReportedAsInconclusive` still guards
   #3475 above this layer

`TestContinueFailover_PausedDeadSourceWorks` covers the paused-dead continuation
at manager level; the live version is dogfood record 2.

The tests construct a real `*exec.ExitError`, because `IsAlive` only inspects
output when `errors.As` finds one — a plain error would skip the whole branch and
pass for the wrong reason.
