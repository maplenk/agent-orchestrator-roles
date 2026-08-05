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
| D1 | **Two active orchestrators are reachable in-process.** `Service.Restore` takes no orchestrator lock and does no uniqueness check; boot `RestoreAll` relaunches every marker-carrying session with no per-project dedup. The service mutex is process-local and is not taken on either path. (It is *not* a cross-daemon gap — `datadirlock` has excluded a second daemon since `9480bdc7`.) | `service/session/service.go:438-449`, `:121-122`, `:422-436`; `manager.go:1720-1745` |
| D2a | **Spawn failure after retirement leaves zero orchestrators, with no recovery path.** The predecessor is already destroyed and no replacement intent is persisted, so nothing retries. | `service/session/service.go:334-350` |
| D2b | **Verification failure misreports a live successor.** `verifyOrchestratorReplacement` errors *after* the successor is spawned and serving; the operation reports failure even though the project has a healthy orchestrator. Distinct from D2a — the owner count is correct, only the reported outcome is wrong. | `service/session/service.go:354`, `:374-389` |
| ~~D3~~ | ~~Retire notice promises a "new workspace"~~ — **fixed** in `85065146` | — |
| ~~D4~~ | ~~Workspace-kind projects break the orchestrator branch invariant~~ — **fixed** in `85065146` | — |
| ~~D6~~ | **Canonical-workspace alias — the most serious defect found in this phase, and *sequential*, not a race.** `RetireForReplacement` never cleared the retired row's `WorkspacePath`/`Branch`, but the orchestrator worktree is canonical per project, so after replacing A with B the terminated row A still named the path B now owns. Any later path-keyed teardown on A destroyed B's live worktree: `Kill` (which also does not stop on `IsTerminated`) and `Cleanup` (which iterates terminated rows and reclaims by recorded path). Fixed by a single retirement finalizer — release-then-terminate — reached by **all three** retirement branches (branchless/scratch, workspace-project, single-repo), plus suppression of *both* the root `WorkspaceInfo` and the saved workspace-project rows in `Kill`, since those rows name the successor's child worktrees too. Runtime teardown is tracked separately from shared-workspace suppression so a predecessor's own process is never left running untracked. — **fixed** | `manager.go` retire/`Kill`/`Cleanup` |
| D5 | **Stale dead code/comment.** `lifecycle/manager.go:23-25` still documents a worker-idle "dispatcher [that] reads it to resolve the current orchestrator at delivery time"; that dispatcher was deleted in `2f6d98f2`. `sessionguard.NudgeCoordination` (`guard.go:158-168`) has zero production callers for the same reason. | as cited |

D3 and D4 were landed separately as `85065146`, ahead of this phase: they are
independent user-visible correctness bugs and do not belong in the migration's
failure surface. **D5 is likewise independent cleanup and must not ride in the
migration commit** — dead-code removal and a schema change should not share a
revert boundary.

D1 is the structural one. It is the same gap as the missing lease, and the
database — not a process-local mutex — should arbitrate it. See §3.2, which also
explains why the index alone is insufficient.

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
process-local convention. `activeOrchestratorSessionID` and
`activeOrchestrators`/`newestSession` both become readers of that single
guaranteed row, so the §2.1 tie-break divergence stops being *reachable* rather
than merely being papered over.

**The index is necessary but not sufficient, and must not be mistaken for the
ownership boundary.** It arbitrates final row cardinality; it cannot serialize
the operations that race to produce those rows, and how early it bites depends
on the path:

| Path | Row vs. workspace ordering | Does the index bite in time? |
|------|----------------------------|------------------------------|
| `Manager.Spawn` | Writes the **active seed row** (`manager.go:489`) *before* `createSessionWorkspace` (`:503`) | **Yes** — a competing new orchestrator spawn is rejected at `CreateSession`, before any worktree work, and `rollbackSpawnSeedRow` already handles the unwind |
| `Restore` / boot `RestoreAll` | `workspace.Restore` creates or **adopts** the worktree (`manager.go:1769`, `:1875`, `:2084`) *before* `relaunchSession` → `MarkSpawned` (`:1421`) flips the existing terminated row to active | **No** — the constraint is only reached after the canonical workspace has already been adopted |

So the late-constraint hazard is specific to **restore and any other transition
that activates an existing row after creating or adopting the workspace**, not
to new spawns. That is precisely where `gitworktree.Create`'s adopt-rather-than-
fail behaviour (§2.1b) does damage the index cannot prevent, and it is why the
gate — not the constraint — has to be the ownership boundary.

**Required: a manager-owned, project-keyed gate** (or an equivalent durable
CAS) that spans the *entire* operation, explicitly including the interval
between retirement and successor spawn. Requirements:

- Lives in the session manager, where the switch fence lives — not only in the
  service layer. Today's `lockOrchestratorProject`
  (`service/session/service.go:422-436`) is service-layer and process-local, and
  `Service.Restore` (`:438-449`) and `RestoreAll` never take it at all.
- Covers: orchestrator switch/fresh, switch recovery, orchestrator `Restore`,
  boot `RestoreAll`, and retire-through-successor-spawn as one critical section.
- Composes with the existing session-keyed `beginSwitch` single-flight
  (`switch.go:667-678`), which remains necessary and is not a substitute.

**Prescribed shape (agreed at review):**

| Rule | Detail |
|------|--------|
| One manager command | A single high-level manager entry point covers the whole ensure/retire→spawn operation — active-owner lookup, retire notice delivery, retirement, successor spawn |
| Service keeps its layer | Authorization, API error mapping, telemetry, and `toSession` presentation stay in the service. The manager returns the raw record plus prompt metrics so the service can convert **after** the gate is released |
| No lock handles cross the boundary | Do **not** expose `Lock()/Unlock()` or a callback-based lock to the service. The service never holds a second lock |
| Public entry points self-acquire | Every public manager entry acquires the gate itself; private `…UnderOwnership` helpers do the work, preventing reentrant deadlock |
| Lock order | **project ownership gate → session `beginSwitch` fence → lifecycle/store locks.** Fixed, documented, never inverted |
| Reload after acquire | Re-read the session *after* taking the gate, before acting — pre-gate reads are stale by construction |
| No bypass | Direct `Manager.Spawn(…KindOrchestrator)` and `RetireForReplacement` must not be able to skip the gate |
| Per-project concurrency | Serialize identical project ids only; different projects stay fully concurrent |

**Correction on prior reasoning.** An earlier draft justified the index partly
as protection against a second daemon. That is wrong: `datadirlock` has taken an
exclusive lease on `AO_DATA_DIR` before `sqlite.Open` since `9480bdc7`, so a
second AO daemon never reaches the store. The index remains worthwhile for
legacy databases already carrying duplicates, for in-process restore races, and
as defense in depth — not for multi-daemon.

### 3.2b Migration and losing-runtime reconciliation (must be specified before coding)

Existing databases can already violate the constraint, so `0046` cannot simply
add the index. Specify and test:

| Concern | Requirement |
|---------|-------------|
| Survivor | A **deterministic** rule (newest by `CreatedAt`, then `UpdatedAt`, then lexical `ID` — matching `sessionNewer`, `service.go:412`), not `ListSessions` order |
| Losers | Marked terminated **and** their `session_worktrees` restore markers neutralized, or boot `RestoreAll` will resurrect them on every restart |
| Live loser runtimes | **Probe-authoritative** reap before serving traffic — a best-effort `Destroy` can leave a live process holding the canonical worktree |
| Ordering | Reconciliation must complete before the daemon serves, alongside the existing `Reconcile` passes |
| Restore preflight | `Restore` of a terminated orchestrator must be refused **before** runtime/workspace creation when another active owner exists — failing after creation leaks a worktree |
| Constraint loss at spawn | If `MarkSpawned` loses the unique-index race, cleanup of the just-created runtime must also be probe-authoritative, reusing `destroyRuntimeProbed` (`switch.go:596-611`) rather than a bare `Destroy` |
- The lease is **not** released mid-saga: an in-place switch keeps the same
  session id, so the lease value is stable across the whole transfer and only
  the generation changes. This is what makes the in-place shape cheap.
- Mutual exclusion must be keyed by **project**, not session — the existing
  `beginSwitch` single-flight (`switch.go:667-678`) is session-keyed and is
  necessary but not sufficient.

### 3.3 Handoff payload for an orchestrator

`ObservedWorkspaceV1` is a host-computed observation of a **worker session's
git workspace**. An orchestrator has no task worktree (promptless;
`manager.go:3287` explicitly permits an empty prompt for `KindOrchestrator`), so
there is nothing for it to observe. Fabricating one would be a lie in an
artifact whose entire value is that it is trustworthy.

- Keep `SemanticHandoffV1` (agent-authored, untrusted) unchanged.
- Add a **sibling versioned artifact, `ObservedOrchestratorV1`**, used on the
  orchestrator path. `ObservedWorkspaceV1` is unchanged and remains the
  host-observed artifact for worker workspaces — nothing is replaced or
  reshaped. Two observed types, one compiler rule.
- Compiler rule stays: observed facts override semantic claims.

`ObservedOrchestratorV1` contract, to be fixed before implementation:

| Aspect | Specification |
|--------|---------------|
| Content | Per live worker in the project: session id, role id, harness, activity state, branch, and PR facts |
| Source | Sessions come from the session store; **PR facts do not live on the session row** — they are a separate read via `ListPRFactsForSession` (`storage/sqlite/store/pr_facts.go:30`, used at `service/session/service.go:719`). The compiler must take an explicit store port for both, not reach through a session record |
| Ordering | **Deterministic** — sort by session id. Never `ListSessions` order, so the compiled text is stable and diffable across generations |
| Size bound | Hard cap on roster entries; the handoff is injected into a system prompt and must not scale without limit with project size |
| Truncation | **Explicit and visible** — when the cap is hit, the compiled text must say how many workers were omitted. A silently truncated roster is worse than no roster, because the successor cannot tell it is incomplete |
| Provenance | Entirely AO-owned data, so it is legitimately *observed* under DoD invariant 11 and needs no agent attestation |

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
| ~~**2B-0a**~~ | **Landed.** Manager-owned, project-keyed gate; `EnsureOrchestrator` is the single gated ownership command covering lookup → retire notice → retirement → successor spawn. Every public orchestrator mutation self-acquires via private `…UnderOwnership` helpers: `Spawn`, `RetireForReplacement`, `RestoreWithMode`, `Kill`, `ResumeAgentWithMode` (gate **before** the resume fence), `RollbackSpawn`, and `Cleanup` (project-scoped). Retirement now releases the retired row's workspace claim, and `Kill`/`Cleanup` additionally refuse a canonical path held by the active orchestrator (**D6**). Service delegates, keeping authorization, telemetry and presentation outside the gate. **Boot `RestoreAll` is not yet gated** — carried into 2B-0b, where it is needed anyway for duplicate reconciliation | done | — |
| **2B-0b** | **Coordinator uniqueness**: migration 0046 partial unique index, plus the reconciliation spec in §3.2b (deterministic survivor, marker neutralization, probe-authoritative reap, restore preflight). Closes **D1** | 1–2 d | 2B-0a |
| **2B-1** | Orchestrator in-place **fresh conversation**: parameterize the `KindWorker` guards (`switch.go:64`, `:254`, `manager.go:1673`, `service/session/switch.go:59`), `Reconcile` recovery for orchestrators, `ObservedOrchestratorV1` handoff, new ledger kind | 2–3 d | 2B-0b |
| **2B-2** | Replacement **durable recoverability**: persist replacement intent before retirement so a zero-owner interval is always auto-recovered (**D2a**/**D2b**, per DoD 5b). Also owns the **two-write retirement failure window**: `finalizeRetirement` releases the claim and marks terminated as separate writes, so a crash between them leaves a released-but-active row | 1–2 d | 2B-0b |
| **2B-3** | **Cross-harness** orchestrator switch | 1–2 d | 2B-1. non-strict only; **strict blocked on 1-B (Claude RO)** |
| *deferred* | Successor-session handoff (new session id, worker rebind push) | — | 2B-2; needs both a live-worker rebind mechanism and worktree-release sequencing (§2.1b), neither of which exists |

Estimate for 2B-0a..2B-3 ≈ **6–11 working days**, versus the 3–5 in
MASTER_PLAN §8. The delta is the ownership gate, coordinator uniqueness with its
reconciliation path, and replacement recoverability — real gaps that estimate did
not account for, all discovered by mapping rather than assumed. The upper bound
is driven by 2B-0b: migrating databases that may already violate the invariant is
the least predictable work in the phase.

**Sequencing note.** The ownership gate (2B-0a) comes before the constraint
(2B-0b), and both before the saga. The gate serializes the operations; the index
arbitrates the rows. Landing the saga on a project that can still hold two active
orchestrators adopting one worktree would reproduce exactly the ambiguous
generation-ownership failures Phase 2A spent its review budget eliminating.

---

## 5. Definition of done

1. An orchestrator fresh conversation preserves session id, workspace, branch,
   the durable role pin, and `ResolvedPermissions.CanSpawn`. It must **rotate**
   the spawn credential, never preserve it: `relaunchSession` issues a fresh
   token and hash on every relaunch precisely "so a terminated/killed session's
   prior token cannot be reused" (`manager.go:1359-1369`). Preserving
   `SpawnCapabilityHash` across a generation would reintroduce that hole.
2. At most one generation owns orchestrator input at the boundary — all three
   2A fences apply, verified by test.
3. Pre-stop failure leaves the orchestrator usable (probe-confirmed-alive
   rollback), exactly as 2A.
4. Post-stop failure retains the handoff and permits retry; boot `Reconcile`
   recovers an orchestrator saga (regression-guarded, since today it silently
   skips non-workers).
5. Exactly one session answers "who is the orchestrator" for a project, across
   both resolvers — enforced by the database, and verified to hold through
   competing in-process `Restore` / `RestoreAll` / manager operations, or
   through independent store connections. **Not** via a concurrent second
   daemon: `datadirlock` excludes one before `sqlite.Open`, so that scenario is
   unreachable. The existing daemon-lock regression stays where it is and keeps
   covering that separately.
5b. **Replacement is durably recoverable** (D2). "Never zero orchestrators" is
   not achievable under retire-first semantics — the canonical-workspace
   collision (§2.1b) forces retirement to release the worktree before the
   successor can create it, so a spawn failure necessarily leaves a
   zero-owner interval. The achievable guarantee is: **replacement intent is
   persisted before retirement, and a zero-owner state is always automatically
   recovered** (on retry or at next boot reconcile), never terminal.

   The two failure modes are distinct and must be evidenced separately:

   | Failure | State | Required behavior |
   |---------|-------|-------------------|
   | Spawn fails after retirement | Zero orchestrators | Persisted intent drives automatic recovery; project is never stranded |
   | `verifyOrchestratorReplacement` fails | Successor is **already live and serving** | API reports failure; must not imply the successor is absent, and must not destroy a healthy orchestrator as a side effect |
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
