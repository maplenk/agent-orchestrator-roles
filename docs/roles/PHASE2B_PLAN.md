# Phase 2B — Orchestrator ownership transfer

**Status:** design, not started. Written after Phase 2A close-out (`5e8476d5`).
**Canonical design:** `MASTER_PLAN.md` §5.4. **Execution status:** `REMAINING_PLAN.md`.
**Prereq reading:** `AGENT_HANDOFF.md` §5 (2A hard lessons — all still binding).

---

## 0. Scope decisions (human, this phase)

| Decision | Choice |
|----------|--------|
| Protocol shape | **In-place switch of the orchestrator session now**; successor-session handoff deferred to a later slice |
| Pending message transfer | **Fence only — no new durable inbox.** Transfer only what the saga itself owns |
| Failover / limits | Out of scope (Phase 3A/3B) |

The "no new inbox" decision is not a shortcut. Upstream shipped durable
orchestrator coordination **twice** and reverted both:

| Attempt | Added | Removed | Reason |
|---------|-------|---------|--------|
| Worker-idle outbox (`worker_idle_events`) | `0025` | `0037` @ `2f6d98f2` | "remove worker idle orchestrator nudges" (−1318 lines) |
| Orchestrator re-engagement (`orchestrator_reengagements`) | `0038` @ `79a70e82` | `0039` @ `ef4d6c12` | "The daemon-authored re-engagement message fired repeatedly at orchestrator sessions (3-4 duplicates in a row)" |

Rebuilding that substrate is a separate, evidence-driven decision — not a
side effect of Phase 2B.

---

## 1. Blocking finding — read before estimating

**Under `strictDelegation`, an orchestrator can only ever be Codex, so
cross-harness orchestrator switch is currently impossible on the strict path.**

Chain of constraints, each verified against the runtime registry:

1. `RoleMap.Validate` requires the orchestrator role to be
   `workspaceWrites:false` under strict delegation (`domain/rolemap.go:207-213`).
2. `workspaceWrites:false` requires `read_only_enforced`
   (`capabilities.go:117-119`).
3. Only **Codex** has `read_only_enforced=true` (`capabilities.go:38-44`);
   Claude is deliberately false pending 1-B.
4. Failover rungs inherit the owning role's permissions
   (`capabilities.go:98-105`), so a strict orchestrator ladder also cannot
   contain Claude.
5. `SwitchWorker` re-checks RO on the target before launch
   (`session_manager/switch.go:88-92`).

Probed directly against `ValidateRoleMap` + `RoleAuthorizedSwitchTargets`:

```text
strict orch primary=codex        -> valid
strict orch primary=claude-code  -> workspaceWrites=false requires read_only_enforced (got false)
strict orch primary=pi           -> workspaceWrites=false requires read_only_enforced (got false)
strict orch codex + claude ladder-> failover.roles[orchestrator][0]: ... read_only_enforced (got false)
authorized switch targets for orchestrator: [{Harness:codex Model:}]
```

**Consequence.** For a strict project the only legal in-place operation is
`codex → codex`, which `resolveSwitchTarget` (`switch.go:613-623`) classifies as
`fresh_conversation`, not `switch`.

That is still worth building — the orchestrator is the longest-lived session in
any project and therefore the worst P1 context-exhaustion offender, and
MASTER_PLAN §5.3 already defines same-harness fresh conversation as the P1
remedy. But it must be named honestly.

**Roadmap correction.** `AGENT_HANDOFF.md` §8.2 lists 1-B (Claude RO) as
"parallel optional — does not block 2B". That is true for 2B-1 and 2B-2 below
and **false for 2B-3**: cross-harness orchestrator switch on strict projects is
blocked on Claude RO. Non-strict projects are unaffected.

---

## 2. Current state — what exists, what does not

### 2.1 There is no coordinator lease; ownership is *derived*, and derived two different ways

| Resolver | Location | Rule |
|----------|----------|------|
| `activeOrchestratorSessionID` | `session_manager/manager.go:2740-2751` | **first** non-terminated orchestrator in `ListSessions` order |
| `newestSession(activeOrchestrators(...))` | `service/session/service.go:346`, `:402-410` | **newest by `CreatedAt`** |

These disagree whenever a project has more than one active orchestrator — which
is exactly the transfer window. `lockOrchestratorProject`
(`service/session/service.go:322`) is a **process-local** mutex in the service
layer only; it does not exist in the session manager where the switch fence
lives, and it does not survive a daemon restart.

**This is the single real gap `MASTER_PLAN` §5.4 calls "coordinator lease".**

### 2.1b The orchestrator workspace and branch are keyed by *project*, not session

- Path: `<managedRoot>/<projectID>/orchestrator/<prefix>-orchestrator`
  (`adapters/workspace/gitworktree/workspace.go:1236-1245`) — session id is
  deliberately excluded, and `validateConfig` does not require one for
  orchestrators (`workspace.go:1164-1188`).
- Branch: `ao/[dev/]<prefix>-orchestrator`
  (`session_manager/manager.go:2475-2486`) — also session-independent.

Two consequences:

1. **In-place switch is a natural fit.** The workspace and branch are already
   project-canonical and stable across a harness change; nothing needs to move.
2. **Successor-session handoff has a path collision.** A successor must wait for
   the predecessor's `ForceDestroy` to release the worktree, because
   `gitworktree.Create` **adopts** an already-registered worktree rather than
   failing (`workspace.go:139-143`, `:674-705`) and returns the *registered*
   branch, not the requested one (`:698-701`). Two live orchestrators would
   silently share one directory and one branch. This is further support for
   deferring the successor shape.

### 2.2 Nothing rebinds already-running workers

A worker's system prompt embeds the orchestrator session id
(`manager.go:2664-2670`), recomputed at spawn/restore only
(`manager.go:2647-2649`). A worker that is already live keeps pointing at the
previous orchestrator id until it restarts. In-place switch **sidesteps this
entirely** — the session id never changes — which is a substantive argument for
the in-place shape and against successor-session handoff.

### 2.3 Nothing durable is in flight to transfer

Confirmed by evidence sweep: no inbox, no outbox, no queue. The only durable
"message" state is `pr.last_nudge_signature`, which records *"already sent"*
(not *"still owed"*), is keyed by PR URL, and never targets an orchestrator.
Unsent bytes live only in `attachment.pendingInput` (`terminal/attachment.go:67`,
nulled on close) and the in-memory `confirmActive` retry loop
(`manager.go:2273-2306`).

The existing transfer message — `sendRetireNotice` — is fire-and-forget:
`_ = s.sendRetireNotice(ctx, orch.ID)` (`service/session/service.go:335`), after
which `RetireForReplacement` destroys the runtime whether or not it landed.

### 2.4 What blocks orchestrators from the 2A saga today

| Blocker | Location |
|---------|----------|
| `ErrNotWorker` on switch | `switch.go:64-66` |
| `ErrNotWorker` on recover | `switch.go:254-256` |
| `Reconcile` skips non-workers, so boot recovery never runs | `manager.go:1673-1675` |
| Service rejects non-worker with `NOT_A_WORKER` | `service/session/switch.go:59-61` |

### 2.4b Pre-existing defects in the replacement path (fold into 2B, do not inherit)

Found while mapping; all predate this phase.

| # | Defect | Evidence |
|---|--------|----------|
| D1 | **Two active orchestrators are reachable.** `Service.Restore` takes no orchestrator lock and does no uniqueness check; boot `RestoreAll` relaunches every marker-carrying session with no per-project dedup. The service mutex is process-local (`map[ProjectID]*sync.Mutex`), so it is also useless across daemons. | `service/session/service.go:438-449`, `:121-122`, `:422-436`; `manager.go:1720-1745` |
| D2 | **Retire-succeeded-but-spawn-failed leaves the project with zero orchestrators and no recovery path.** `verifyOrchestratorReplacement` errors *after* the successor is live, with no rollback, and the predecessor is already destroyed. | `service/session/service.go:334-350`, `:374-389` |
| D3 | **The retire notice is factually wrong.** It promises "a fresh orchestrator will take over in a **new workspace**"; the successor reuses the identical canonical path and branch. | `service/session/service.go:365` vs `workspace.go:1238-1244` |
| D4 | **Workspace-kind projects break the branch invariant.** `DefaultSpawnBranch` returns `ao/<sessionID>` for `ProjectKindWorkspace` regardless of kind, while `verifyOrchestratorReplacement` asserts the canonical orchestrator branch. | `manager.go:2488-2497` vs `service/session/service.go:384-387` |
| D5 | **Stale dead code/comment.** `lifecycle/manager.go:23-25` still documents a worker-idle "dispatcher [that] reads it to resolve the current orchestrator at delivery time"; that dispatcher was deleted in `2f6d98f2`. `sessionguard.NudgeCoordination` (`guard.go:158-168`) has zero production callers for the same reason. | as cited |

D1 is the important one: it is the same gap as the missing lease, and it has a
cheap enforcement — a **partial unique index** on
`(project_id) WHERE kind='orchestrator' AND is_terminated=0`. That makes the
database, not a process-local mutex, the arbiter of "one coordinator per
project". Boot restore must then handle the constraint by failing that one
restore closed rather than aborting the whole pass.

### 2.5 What transfers unchanged from 2A (reuse, do not reinvent)

- The **three-fence pattern over one durable field** (`switch_pending_json`):
  sessionguard (`guard.go:191-195`), terminal `InputGate`
  (`terminal/manager.go:339-344` → `manager.go:3141-3166`), and the
  `DeliverHost` host-owned escape hatch (`guard.go:135-139`).
- **Persist-fence-before-destroy** ordering (`switch.go:150-170`).
- **Probe-authoritative stop** (`destroyRuntimeProbed`, `switch.go:596-611`).
- **Ledger-ack-then-promote** (`switch.go:520-539`), stable ids
  `{sessionID}:{generationID}:{phase}`.
- `ForceLaunchID` generation fencing.

---

## 3. Design

### 3.1 State machine

Identical phases to 2A — deliberately, so recovery and ledger tooling are shared:

```text
requested → pre_stop → post_stop → target_ack
                 \→ failed (terminal, with phase context)
```

New ledger kind: `orchestrator_switch` (and reuse `fresh_conversation` when the
harness is unchanged). `LifecycleLedgerKind.Valid()`
(`domain/lifecycle_ledger.go:17-25`) must accept it, and
`isSwitchLedgerKind` (`switch.go:747-749`) must include it or recovery will not
find the rows.

### 3.2 The coordinator lease

Durable, project-scoped, and the **single** answer to "who is the orchestrator".

**Prefer a constraint over a table.** Migration **0046** (next free) adds a
partial unique index:

```sql
CREATE UNIQUE INDEX idx_sessions_one_active_orchestrator
    ON sessions(project_id)
    WHERE kind = 'orchestrator' AND is_terminated = 0;
```

This makes "one coordinator per project" a database invariant rather than a
process-local convention, and it closes D1 (which no amount of service-layer
locking can close, since `Restore`/`RestoreAll`/a second daemon all bypass the
mutex). With the invariant enforced, "who is the orchestrator" becomes a
*total* function and no lease row is required.

- `activeOrchestratorSessionID` and `activeOrchestrators`/`newestSession` both
  become readers of that single guaranteed row, eliminating the §2.1 divergence.
  The tie-break disagreement stops being reachable rather than merely being
  papered over.
- **Migration risk:** existing databases may already violate the constraint
  (D1 is reachable today). The migration must terminate or reconcile duplicates
  before adding the index, and boot restore must fail a violating restore
  closed without aborting the whole pass.
- The lease is **not** released mid-saga: an in-place switch keeps the same
  session id, so the lease value is stable across the whole transfer and only
  the generation changes. This is what makes the in-place shape cheap.
- Mutual exclusion must be keyed by **project**, not session — the existing
  `beginSwitch` single-flight (`switch.go:667-678`) is session-keyed and is
  necessary but not sufficient.

### 3.3 Handoff payload for an orchestrator

`ObservedWorkspaceV1` is git-anchored and near-meaningless for an orchestrator
(promptless, no task worktree, `manager.go:3287` explicitly permits an empty
prompt for `KindOrchestrator`). Replace it, do not fake it:

- Keep `SemanticHandoffV1` (agent-authored, untrusted) unchanged.
- Add a host-computed **worker roster**: for each live worker in the project —
  session id, role id, harness, activity state, branch, PR facts. This is
  entirely AO-owned data read from the sessions table, so it is legitimately
  *observed* under DoD invariant 11 and needs no agent attestation.
- Compiler rule stays: observed facts override semantic claims.

`SwitchPending.OriginalTask` is meaningless here and must be left empty rather
than populated with a synthetic prompt.

**Why this is a net improvement over today.** An orchestrator is promptless by
design (`manager.go:3284-3289`), so whenever the adapter has no
`AgentSessionID` to resume from, restore already relaunches it *fresh with the
system prompt only* — its accumulated coordination context is simply gone, with
no handoff at all. Retire-and-replace loses it unconditionally. A compiled
handoff carrying the worker roster is therefore strictly more context than
either path preserves now, which is the concrete P1 win for this phase.

---

## 4. Slices

| Slice | Scope | Est. | Depends on |
|-------|-------|------|------------|
| **2B-0** | **Coordinator uniqueness**: migration 0046 partial unique index + duplicate reconciliation, both resolvers read the single row, restore paths fail closed on violation. Closes **D1**, and fixes **D3**/**D4** (both one-liners in the same code) | 1–2 d | — |
| **2B-1** | Orchestrator in-place **fresh conversation**: parameterize the `KindWorker` guards (`switch.go:64`, `:254`, `manager.go:1673`, `service/session/switch.go:59`), project-keyed single-flight, `Reconcile` recovery for orchestrators, worker-roster handoff, new ledger kind | 2–3 d | 2B-0 |
| **2B-2** | Replacement-path **recovery**: close **D2** so a failed successor spawn cannot leave a project with zero orchestrators | 1 d | 2B-0 |
| **2B-3** | **Cross-harness** orchestrator switch | 1–2 d | 2B-1. non-strict only; **strict blocked on 1-B (Claude RO)** |
| *deferred* | Successor-session handoff (new session id, worker rebind push) | — | 2B-2; needs both a live-worker rebind mechanism and worktree-release sequencing (§2.1b), neither of which exists |

Estimate for 2B-0..2B-3 ≈ **5–8 working days**, versus the 3–5 in MASTER_PLAN §8.
The delta is coordinator uniqueness and replacement recovery — real gaps that
estimate did not account for, both discovered by mapping rather than assumed.

**Sequencing note.** 2B-0 is first because every later slice assumes a single
well-defined coordinator. Landing the saga on top of a project that can hold two
active orchestrators sharing one worktree would produce exactly the ambiguous
generation-ownership failures Phase 2A spent its review budget eliminating.

---

## 5. Definition of done

1. An orchestrator fresh conversation preserves session id, workspace, branch,
   role pin, and `canSpawn` capability hash.
2. At most one generation owns orchestrator input at the boundary — all three
   2A fences apply, verified by test.
3. Pre-stop failure leaves the orchestrator usable (probe-confirmed-alive
   rollback), exactly as 2A.
4. Post-stop failure retains the handoff and permits retry; boot `Reconcile`
   recovers an orchestrator saga (regression-guarded, since today it silently
   skips non-workers).
5. Exactly one session answers "who is the orchestrator" for a project, across
   both resolvers — enforced by the database, and verified to hold through
   `Restore`, boot `RestoreAll`, and a concurrent second daemon.
5b. A failed successor spawn never leaves a project with zero orchestrators
   (D2), and the retire notice describes what actually happens (D3).
6. Lifecycle ledger records every orchestrator switch/fresh with the same
   `requested → pre_stop → post_stop → target_ack` sequence and no unexplained
   `failed`.
7. Workers spawned before and after the transfer both address the surviving
   orchestrator.

## 6. Non-claims

- **No durable orchestrator inbox.** Signals in flight during a transfer are
  best-effort exactly as they are today; this phase does not change that and
  must not claim it does.
- **No automated re-engagement / re-nudging.** Explicitly out of scope; see the
  `0038`/`0039` revert.
- **No cross-harness orchestrator switch on strict projects** until Claude RO
  (1-B) lands. Non-strict only.
- **No live-worker rebind.** Not needed for in-place; required before
  successor-session handoff is viable.
- **No limit detection or failover.** Phase 3.
