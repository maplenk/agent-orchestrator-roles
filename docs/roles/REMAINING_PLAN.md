# Multi-sub roles — status & remaining plan

**Repo:** https://github.com/maplenk/agent-orchestrator-roles  
**Active integration branch:** `roles/upstream-sync-2` @ `dd06d31a` (PR #1, draft — **accepted 2026-08-07; all required CI green**)
**Roles trunk awaiting merge:** `roles/multi-sub-v1` @ `1f80bdb5`
**Baseline:** Untrivial-ai/agent-orchestrator @ `fa799a7a58e2f9ec13d174567aff436ba890ff6a` (see `AO_BASELINE_SHA.txt`)
**Target:** B (~full wishlist)  
**Current gate:** merge the accepted Sync 2 branch to the roles trunk, then resume Phase 3A-2b vendor detection

This document is the living plan: **what landed**, **what remains**, **order**, and **gates**.  
Canonical product design remains `MASTER_PLAN.md`; this file tracks execution status.

**Keep this file in sync whenever a phase or major slice lands** (status tables, HEAD, next action, DoD checkboxes).

---

## Snapshot (now)

| Area | Status |
|------|--------|
| Phase 1 foundation | **Accepted.** Codex read-only, capability registry, CLI/API role map, template CAS and host-authoritative role resolution are in production paths. |
| Phase 1-B / strict exit | **Open.** Claude still advertises `read_only_enforced=false`; this blocks a Claude strict orchestrator and Phase 2B-3. |
| Phase 2A | **Accepted and promoted.** Worker switch/fresh/recovery, ledger, input fences, API/CLI/auth and live dogfood are complete; Claude/Codex `switch_supported=true`. |
| Phase 2B-0/1/2 | **Landed and dogfooded.** Coordinator uniqueness, fail-closed boot, in-place orchestrator fresh conversation and replacement recoverability are present. |
| Phase 2B-3 | **Blocked/deferred on Claude read-only.** Cross-harness orchestrator switching remains refused. Phase 2B as a whole is therefore not complete. |
| Phase 3A pause core and operator surface | **Landed and accepted.** Durable pause, ledger-before-pin, zero automatic restart/send, pause/resume API+CLI, ownership CAS and pause-aware lifecycle are present. |
| Phase 3A desktop | **Landed and live-dogfooded.** The inspector distinguishes paused-live from paused-dead and Resume from Restart agent; the strict composer sends role-only requests. A desktop role-map editor is still absent. |
| Phase 3A-2b detector boundary | **Landed; no harness promoted.** `internal/limits` is the only ingress, but the detector registry is empty and `limit_detection_supported=false` everywhere pending captured vendor fixtures. |
| Phase 3B | **Not started.** Manual continue and bounded opt-in automatic failover remain. |
| Upstream Sync 2 | **Accepted (2026-08-07) on `roles/upstream-sync-2` @ `dd06d31a`; not yet merged to the roles trunk.** Pinned to `fa799a7a`; fork migrations are 9000–9007. All eight steps are done and **every required GitHub Actions job is green**; Step 5's two live records are in `UPSTREAM_SYNC2_DOGFOOD_STEP5.md`. The merge is the only remaining action. See `UPSTREAM_SYNC2_PLAN.md`. |
| CI | **Green, including the required GitHub Actions jobs** on `7165c942` (PR #1, draft): Go — build-test with `go test -race ./...`, lint, api-drift — plus Frontend, CLI E2E, e2e-gate, gitleaks, Mobile and React Doctor. Locally: gofmt, build, vet, golangci-lint v2.12.2 (0 issues), backend 4486 pass, frontend 1992 pass / 0 fail, typecheck clean, zero data races. The `-race` timing failures seen locally do not reproduce on the Ubuntu runner. |

> **Claude-only installs cannot use a strict role map.** The strict
> orchestrator role must be `workspaceWrites:false`, and only Codex currently
> advertises enforceable read-only. This is the Phase 1-B blocker itself;
> Phase 2B-3 is downstream.

**Next engineering actions, in order:** merge `roles/upstream-sync-2` into
`roles/multi-sub-v1` and record the merge SHA here and in the Sync 2 tracker.
Every Sync 2 acceptance gate — review, the two live Step 5 records and the
required CI jobs — is closed, so the merge is the only thing left. Then resume
vendor-backed Phase 3A-2b detection, which is still blocked on captured vendor
fixtures. Claude read-only may proceed in parallel and remains the gate for
2B-3.

---

## 1. Completed (accepted)

### Phase 0 (partial) — fork foundation

| Item | Status | Notes |
|------|--------|--------|
| Fork + pin AO baseline | **Done** | `FORK.md`, `AO_BASELINE_SHA.txt` |
| Own migration numbers | **Done** | Fork history is now `9000`–`9007`; the fingerprinted pre-goose repair upgrades old 0053–0060 fork databases without stealing upstream's Muse migration 0053. See `UPSTREAM_SYNC2_PLAN.md` |
| GitHub origin | **Done** | `maplenk/agent-orchestrator-roles` |
| Profiles (orchestrator, implementor, ui-implementor, reviewer, verifier) | **Done** | `profiles/*.md` (shipped host templates) |
| Capability matrix | **Partial → runtime exists** | `capabilities.For` is source of truth; `CAPABILITY_MATRIX.md` docs mirror |

### Phase 1 — closed slices (not full Phase 1 exit)

| Item | Status | Evidence |
|------|--------|----------|
| RoleMap + RoleBinding + RoleExecutionPolicy + FailoverConfig | **Done** | `domain/rolemap.go` |
| ProjectConfig.roleMap + Validate (domain) | **Done** | `domain/projectconfig.go` |
| CLI / API roleMap round-trip | **Done** | CLI `roleMap` mirror + set-config; live dogfood set roleMap via CLI |
| Host-authoritative `ao spawn --role` | **Done** | CLI + HTTP `roleId`; Resolve rejects free-form harness with role |
| Strict: role required for workers; no harness override | **Done** | `roles/resolve.go` |
| Strict orch auto-bind `orchestratorRole` | **Done** | Fails closed without launch until read-only |
| Role template loader + path containment | **Done** | `roles/templates.go`, `AO_ROLE_PROFILES_DIR` |
| Single applyRoleMap; empty model overwrites project model | **Done** | Phase 1b |
| Role system prompt footer after base (recency) | **Done** | Phase 1c |
| Runtime capability registry (spawn + RO; switch promoted for Claude/Codex) | **Done** | `roles/capabilities`; limit stays false until Phase 3 |
| Codex `read_only_enforced` | **Done** | OS sandbox; Claude RO still **false** by design |
| Migration **9000** durable role columns + `template_artifacts` CAS | **Done** | Accepted; renumbered during Sync 2 |
| Spawn dual-write CAS; restore-by-artifact | **Done** | Fail closed missing/incomplete/conflict |
| Partial pin hydration + incomplete pin fail-closed | **Done** | Store + restore |
| Session-scoped spawn capability (random token, hash only) | **Done** | Migration **9001** |
| `canSpawn:false` / terminated / invalid cap → 403 | **Done** | |
| Operator transport (desktop header, LAN authctx, CLI rules) | **Done** | Accepted @ `93fcf1f8` |
| `AO_MANAGED_SESSION` marker (not `AO_DATA_DIR`) | **Done** | |
| Template authority Option A | **Done** | Host/shipped profiles; `AO_ROLE_PROFILES_DIR` |
| Codex review packs (Phase 1 slices) | **Done** | `PHASE1*_CODEX_REVIEW.md`, `PHASE0042_*`, `PHASE_CANSPAWN_*` |

### Phase 2A — closed (Claude/Codex switch promoted)

| Item | Status | Evidence / HEAD |
|------|--------|-----------------|
| SemanticHandoffV1 + ObservedWorkspaceV1 | **Done** | `domain/handoff.go` |
| Compiler (observed overrides semantic) | **Done** | `handoff/compile.go` |
| Lifecycle ledger 9002 + store | **Done** | `AppendLifecycleLedger` / list |
| SwitchPending migration 9003 | **Done** | `switch_pending_json` |
| Manager SwitchWorker / Fresh / Recover | **Done** | `session_manager/switch.go` |
| Generation = ForceLaunchID | **Done** | ledger gen == RuntimeLaunchID |
| Probe-driven destroy + confirmed-alive rollback | **Done** | pre-stop source usable |
| Input gates (sessionguard + terminal InputGate) | **Done** | Send / terminal / host DeliverHost |
| Indexed terminal handle lookups | **Done** | `GetSessionByRuntimeHandleID` / pending source |
| Service + exact model auth | **Done** | `ResolveAuthorizedSwitchModel` |
| HTTP + OpenAPI + FE schema | **Done** | `/switch`, `/fresh-conversation` |
| CLI `ao session switch` / `fresh` | **Done** | `spawnCallerHeaders()` no-upgrade |
| Switch auth (operator / LAN / canSpawn + project scope) | **Accepted** | @ `83f7abfb` |
| Failover config-save spawn + RO + switch | **Done** | `ValidateRoleMap`; switch_supported enforced after promotion |
| Failover **source** requires switch (P2 close-out) | **Done** | Primary binding of a role with a non-empty ladder must advertise `switch_supported`; Pi-primary + Codex ladder now rejects at config-save instead of at runtime |
| Shipped example role map valid + strict-decodable | **Done** | `role-map.strict.example.json` was rejected at config-save (pi/grok rungs) **and** failed `DisallowUnknownFields` on its `_comment` keys; annotations moved to `examples/README.md`, guarded by `TestValidateRoleMap_ShippedStrictExampleValidates` |
| Ephemeral target role footer on switch launch | **Done** | @ `a3bc32be` |
| Manager dogfood checklist | **Done** | `PHASE2A_DOGFOOD.md` @ `2d19ad59` |
| Live dogfood evidence | **Accepted** | `PHASE2A_LIVE_DOGFOOD.md` (footer + clean crash) |
| Concurrent data-dir ownership lease | **Done** | `datadirlock` @ `9480bdc7` |
| Promote `switch_supported` (Claude/Codex) | **Done** | separate promotion CL after close-out accept |

Detail trackers: `PHASE2A_PLAN.md`, `PHASE2A_DOGFOOD.md`, `PHASE2A_LIVE_DOGFOOD.md`.

### Explicit non-claims (honesty)

- **Same-UID host isolation** is **not** claimed.
- **Phase 1 strict operational dogfood** is **not** complete (Claude RO, full 1-F matrix).
- **`read_only_enforced`:** **true only for Codex**. Claude/Pi/others false.
- **`switch_supported`:** **true** for Claude/Codex only; other production harnesses remain false until dedicated promotes.
- **`limit_detection_supported`:** still **false** for all production harnesses (Phase 3).

---

## 2. Remaining work (ordered)

### Phase 2A — complete

| Task | Status | Detail |
|------|--------|--------|
| Accept target-authoritative prompt fix | **Done** | Live footer + unit tests @ `a3bc32be` |
| Concurrent data-dir ownership lease | **Done** | `datadirlock.Acquire` before store open/reconcile |
| Concurrent-start regression | **Done** | exclusive + subprocess one-reconcile-marker; dual-daemon smoke |
| Clean crash ledger (no incidental `failed`) | **Done** | `requested>pre_stop>post_stop>target_ack` only (`lease-crash-gen-1`) |
| Promote `switch_supported` (Claude/Codex) | **Done** | `capabilities.For` + failover switch_supported validation active |

**DoD (MASTER_PLAN):** pre-stop source usable; post-stop handoff retained; one generation owns input — **implemented and promoted**.

---

### Gate G0 / Phase 1 remainder (parallel; not blocking 2B)

Strict `strictDelegation` as daily driver still wants full Phase 1 exit:

| Slice | Status | Notes |
|-------|--------|--------|
| 1-A RO contract doc | **Partial** | `READ_ONLY_CONTRACT.md` exists; tighten if needed |
| 1-B Claude RO (+ negative runtime) | **Open** | Codex done; Claude stays false |
| 1-C registry | **Done** for Phase 1 + 2A switch cells | Limit promote later |
| 1-D role-map surface | **Done** | CLI/API round-trip used in live dogfood |
| 1-E template authority | **Done** | Option A |
| 1-F full verification + strict dogfood | **Open** | Full `go test ./...`, strict orch RO dogfood, Claude RO when ready |

---

### Phase 2B — Orchestrator ownership transfer (~6–11 working days)

**Plan:** `PHASE2B_PLAN.md` (**2B-0a, 2B-0b, 2B-1 and 2B-2 landed**; **2B-3
blocked/deferred on 1-B Claude RO**, so Phase 2B is *not* complete).
Scope decided: **in-place switch now, successor-session handoff deferred**;
**fence only, no new durable inbox** (upstream shipped and reverted durable
orchestrator coordination twice — `0025`→`0037`, `0038`→`0039`).

| Slice | Detail |
|-------|--------|
| 2B-0a Project ownership gate | **Landed.** Manager-owned, project-keyed exclusion; `EnsureOrchestrator` is the single gated ownership command. All public orchestrator mutations self-acquire (`Spawn`, `Retire`, `Restore`, `Kill`, `Resume`, `Rollback`, `Cleanup`). Also fixed the canonical-workspace alias: a retired row kept naming the path its successor owned, so `Kill`/`Cleanup` on the predecessor destroyed the live orchestrator's worktree. Service delegates and keeps auth/telemetry/presentation outside the gate. `RestoreAll` was carried into 2B-0b and is gated there |
| 2B-0b Coordinator uniqueness | **Landed.** Migration 9004 partial unique index + reconciliation, capturing each loser's execution identity into `orchestrator_reap_queue` before clearing it; fail-closed boot reaper draining that queue ahead of every surface; unique-constraint errors mapped to `ErrActiveOrchestratorExists` (409) instead of an opaque 500; `MarkSpawned` launch-cleanup window hardened so a failed launch leaves neither an untracked runtime nor a phantom-live row, with `ErrLaunchCleanupUnresolved` propagated through restore *and* post_stop recovery to a fatal boot gate. **Boot restore closed the last ungated path:** `RestoreAll` now restores at most one orchestrator per project under that project's gate, held across *both* the survivor decision and the restore, because `workspace.Restore` adopts the shared canonical worktree before any row flips — so the index alone never sees the damage. Losing candidates and candidates displaced by an already-live owner have their markers neutralized (rows only; the preserved ref survives), mirroring 9004 — and neutralization is a **durable precondition** of restoring the winner, not best-effort: a surviving loser marker does not stay a loser, so once the winner is killed that stale row becomes the only restorable orchestrator and a later boot resurrects the session this election superseded. A marker **read** failure is likewise not an absence: it abandons the whole project's election rather than letting an older candidate be promoted on incomplete evidence into the shared canonical worktree. All three failures — undeletable loser marker, unreadable marker, unreadable project session list — are boot-fatal via `ErrBootUnsafe`, the shared marker the daemon gate keys on, so a new fail-closed condition becomes fatal by wrapping it rather than by editing `daemon.go`. Not having *looked* leaves the identical durable hazard as having failed to *delete*: an unexamined marker is still eligible, so killing the current owner would let a predecessor return on a later boot. Each leaf's membership is pinned by a table test, since the gate keys only on the parent and an unwrapped leaf would silently stop being fatal. `activeOrchestratorSessionID` now applies `newestOrchestratorRecord` too: first-match-in-list-order returned the *oldest* active orchestrator, so workers spawned while two were briefly active were told to report to the one being superseded |
| 2B-1 In-place orchestrator fresh conversation | **Landed.** `KindWorker` guards parameterized across manager saga, recovery and service; `SwitchWorker` stays worker-only as an entry point while `FreshOrchestratorConversation` takes the **project gate before `beginSwitch`** (lock order `projectOwnership -> beginSwitch`, never inverted — otherwise `EnsureOrchestrator` could retire the session mid-saga). New ledger kind `orchestrator_fresh_conversation` so recovery and audit can tell the sagas apart. `Reconcile` post_stop recovery now includes orchestrators, which previously left a crashed mid-switch project with no coordinator. `ObservedOrchestratorV1` compiles the project's fleet — live **and** terminated workers, read from the session table rather than the outgoing agent's recollection — into the handoff; an unreadable fleet degrades rather than aborts, since it is context and the switch is remedying context loss. In-place keeps the session id, so live workers (whose prompts embed it at spawn/restore only) never need rebinding |
| 2B-2 Replacement durable recoverability | **Landed.** Migration 9005 `orchestrator_replacement_intent`, one row per project, written **before** the first destructive step — a failure to record it aborts before anything is destroyed, since retiring without it is the one ordering that strands a project silently. Retained when spawn fails (that IS the state recovery exists for), discharged only once a successor is live. Boot recovery re-drives stranded projects under the project gate, after `Reconcile` so an adopted crash-survivor counts as the owner rather than being spawned over; it is logged rather than boot-fatal because a project without a coordinator is inert and stopping an otherwise-healthy daemon is the worse outcome. `finalizeRetirement` now terminates **before** releasing the claim: the two writes cannot be one, so the ordering picks the residue, and a terminated row with a stale path is guarded and repairable while an ACTIVE row owning no workspace occupies the project's only slot and is handed out by `EnsureOrchestrator`'s idempotent path. `reconcileOrchestratorRetirement` repairs both residues at boot |
| 2B-3 Cross-harness orchestrator switch | **Blocked on 1-B (Claude RO) and refused for all projects today.** Shipping it only for non-strict projects would create a capability that the strict product path can never use |
| Deferred | Successor-session handoff (needs live-worker rebind + worktree release sequencing) |

**Estimate correction:** MASTER_PLAN §8's 3–5 d did not account for coordinator
uniqueness or replacement recovery.

**Roadmap correction:** 1-B (Claude RO) is *not* fully parallel — it blocks
cross-harness orchestrator switch on strict projects (2B-3).

---

### Phase 3A — Limits + durable pause (~4–6 working days)

| Task | Detail | Status |
|------|--------|--------|
| Structured/reviewed limit envelopes only | Never free-text “I hit a limit” | **Boundary done; vendor adapters open.** Closed envelope and event discriminators, size bounds, ownership guard and stable `SourceKey` incident identity are enforced. No detector is registered without captured vendor evidence |
| Durable pause | Zero automatic send/restart | **Done** (3A-1): migration 9007 pin; TUI and Chat automatic sends plus boot relaunch are fenced. No scheduler/timer/auto-resume exists; `RetryAfter` is advisory only |
| Ledger events | pause / resume | **Done** (3A-1): idempotent per incident, written *before* the pin |
| Operator/service surface | pause + resume through service/API/CLI | **Done** (3A-2a): operator/LAN only; agent principals explicitly refused; expected incident required |
| Desktop surface | paused/alive state + Resume + Restart agent + strict role composer | **Done and live-dogfooded.** Resume clears only the displayed incident; Restart is a separate operation. Role-map editing remains API/CLI-only |
| Detector registry | Promote `limit_detection_supported` only after structured-limit tests | **Boundary landed; registry empty and every harness remains false** |

**3A-1 design decisions worth keeping:** the fence keys on write *origin*, not
method, because the send-confirm Enter re-send borrows `Deliver`'s activity
policy while being AO-initiated. AO-initiated writes are refused; the **user's**
sends are not (they can type into the pane anyway, so fencing adds friction, not
safety), and **host-owned launch injection is not** (a 3B failover must be able
to deliver its prompt, or pause blocks its own remedy).

**Consequences of the boot skips, stated deliberately:**

- A paused session whose agent died stays **active with a dead runtime** rather
  than being torn down. That is the ordinary "agent exited" state the guard
  already handles; the alternative mints a restore marker, and `RestoreAll`
  consumes it in the *same* boot.
- A paused orchestrator is excluded from the survivor election as **neither
  winner nor loser**. Losers get their markers neutralized, and neutralizing a
  paused orchestrator's marker would destroy the restorability a later resume
  depends on. It therefore holds the project's single active-orchestrator slot
  while paused, which is correct: replacing it is a user decision.
- `RecoverOrchestratorReplacements` is **not** skipped for paused projects, and
  needs no change — it treats a paused active orchestrator as an owner and
  discharges the intent. It only spawns when a project has zero orchestrators,
  fulfilling an obligation an explicit `EnsureOrchestrator(clean)` created; that
  is a crashed user action being completed, not an automatic restart.
- A paused **terminated** session can still be resumed (resume has no liveness
  precondition); it becomes restore-eligible on the next boot.

**Implemented UX contract:** an active-but-dead paused row is presented as two
facts: the pause pin and the agent's liveness. Resume clears only the incident
displayed by the panel and never relaunches the agent. Restart agent is a
separate explicit operation. The renderer carries the displayed incident id to
submission rather than re-reading it and accidentally clearing a newer pin.

---

### Phase 3B — Manual continue + opt-in failover (~3–5 working days)

| Task | Detail |
|------|--------|
| Manual continue on next ladder rung | Default mode **manual** |
| Failover preserves `role_id` | Only harness/model change |
| `maxFailoversPerIncident` | Cap then stay paused |
| `failover.mode=automatic` | Opt-in only; matrix-gated |

---

### Integration (~3–5 working days)

| Task | Detail |
|------|--------|
| Desktop dogfood | Real Electron + isolated or explicit data dir |
| Crash recovery | Restore + CAS + switch mid-flight (2A path accepted; re-verify under product load) |
| Multi-platform | macOS primary; Windows/Linux as needed |
| DoD checklist | MASTER_PLAN §9 all checked |

---

## 3. Schedule & verification gates

### Units and concurrency

- Estimates are **working days** (engineer-days of focused work), not calendar days.
- **Assumed concurrency:** single primary engineer on the critical path unless noted.

### Slice table (working days remaining)

| Slice | Est. (working d) | Depends on |
|-------|------------------|------------|
| ~~2A promotion gate~~ | **Done** | Close-out accepted; caps flipped |
| 1-B Claude RO (optional parallel) | 3–5 | 1-A |
| 1-F Phase 1 strict exit | 2–3 | 1-B if Claude RO required for strict maps |
| ~~Phase 2B-0a/0b/1/2~~ | **Done** | 2A patterns; ≈6–11 d actual, not the 3–5 first estimated |
| Phase 2B-3 (cross-harness orch) | 1–2 | **blocked on 1-B Claude RO** for strict projects |
| Phase 3A/B | 7–11 | limit detection (promote in 3A) |
| Integration | 3–5 | prior |

### Sequencing sketch

```text
2B-0a/0b ownership + uniqueness ──► LANDED (safety only)
2B-1 orch in-place fresh      ──► LANDED (first user-facing 2B behaviour)
2B-2 replacement recoverability ──► LANDED (intent + crash-consistent retirement)
Sync 2 acceptance ──► DONE (2026-08-07)
Now ──► merge Sync 2 to roles trunk
     ──► vendor-backed 3A-2b detector ──► promote one harness at a time
     ──► 3B manual continue ──► bounded opt-in auto-failover
     ║
     ╚═ parallel: Claude RO / Phase 1-F ──► 2B-3 cross-harness orchestrator
```

---

## 4. Definition of done — remaining invariants

Already satisfied (re-verify on regressions):

1. Strict project: no roleless worker / no free-form harness with `--role`
2. `canSpawn:false` cannot successfully spawn (app-level transport)
3. Restore uses pinned template artifact (existing sessions)
4. Pre-stop switch failure → source usable
5. Post-stop → handoff retained, target retry
6. One generation owns input at switch boundary
9. Unsupported capabilities reject config **and** launch/restore (spawn/RO/switch for promoted cells), on **both** sides of a failover ladder — source primary and every rung
10. Lifecycle ledger for switch/fresh/pause (failover later)
11. ObservedWorkspace verified only with AO provenance
12. Failover default manual
13. Read-only roles bind only to `read_only_enforced` harnesses; Claude has negative rejection coverage and remains unsupported
14. New-session template authority Option A
15. Role map writable via CLI/API (no silent drop)
16. Crash-recover ledger free of unexplained `failed` (root cause was dual daemon ownership; fixed with datadirlock)
17. Production `switch_supported` true for Claude/Codex after accepted 2A close-out

Still open or partial:

7. **Partial:** durable pause and zero automatic send/restart are enforced; no harness yet produces a promoted structured limit event
8. Failover preserves `role_id`, respects incident bound (runtime path)

---

## 5. Coding checklist (MASTER_PLAN §10, status)

1. [x] Fork + pin AO baseline; own migrations
2. [x] Capability matrix as **runtime registry** + validation at config/launch/restore
3. [x] Session-scoped spawn credential
4. [x] Role map schema (version + sha256) in domain
5. [x] Template CAS by sha256 (session restore)
6. [x] Template **new-session** authority (Option A)
7. [x] Durable session role fields + switch pending / ledger
8. [x] `ao spawn --role`; strict reject
9. [x] CLI/API RoleMap round-trip
10. [x] RoleExecutionPolicy — canSpawn; workspaceWrites fail-closed except Codex RO
11. [x] Strict orch prompt builder
12. [~] Executable RO contract — Codex yes; Claude RO deferred
13. [x] SemanticHandoffV1 + ObservedWorkspaceV1 + compiler
14. [x] Worker switch saga + fresh-conversation (manager + service/API/CLI)
15. [x] Lifecycle ledger (switch/fresh)
16. [~] Orchestrator switch protocol — in-place fresh conversation (2B-1) + durable replacement recoverability (2B-2) landed; **cross-harness switch (2B-3) blocked on Claude RO**; successor-session handoff and live-worker rebind deferred
17. [~] Limit pause — **backend/API and the desktop surface landed; no harness detector.** Durable pin + boot fencing (3A-1), operator pause/resume endpoints (3A-2a), the structured detection seam with every harness unsupported (3A-2b), and the renderer paused panel + strict delegation composer (3A-2 UI, live-dogfooded in `PHASE3A2_UI_DOGFOOD.md`). Still missing: a harness detector (needs captured vendor fixtures) and any **desktop role-map editor** — the composer *consumes* a role map, but adding or editing roles remains API/CLI-only
18. [ ] Manual continue + opt-in auto-failover
19. [x] Dogfood against switch DoD — manager + live evidence; Claude/Codex `switch_supported` promoted

---

## 6. Process notes

- **After each slice / phase land:** update **this file** (status, HEAD, next action, checklist).
- **Codex review packs** + accept before promoting capability cells.
- **Migrations:** never edit merged SQL; fork migrations use the reserved `9000+` range and the next number is **9008+**. Do not re-enter upstream's sequential range.
- **Upstream:** keep `upstream` remote; avoid colliding migration IDs.
- **Dogfood:** isolated `AO_DATA_DIR` for risky runs.
- **Promotion:** never flip `switch_supported` / `limit_detection_supported` in the same change as large feature work when possible — separate final CL. (2A switch promote followed this rule.)

---

## 7. Immediate next action

1. Merge `roles/upstream-sync-2` into `roles/multi-sub-v1`; record the merge SHA here and in the Sync 2 tracker. The ordered review, live-dogfood and required-CI blockers in `UPSTREAM_SYNC2_PLAN.md` are all closed.
2. Resume Phase 3A-2b only with captured, sanitized vendor fixtures. Keep `limit_detection_supported=false` until a detector has structural tests and live evidence, then promote it separately.
3. Start Phase 3B after one detector is accepted. Claude read-only remains parallel and gates 2B-3.
