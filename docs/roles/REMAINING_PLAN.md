# Multi-sub roles — status & remaining plan

**Repo:** https://github.com/maplenk/agent-orchestrator-roles  
**Branch:** `roles/multi-sub-v1`  
**HEAD:** (see latest commit; update on each land)  
**Baseline:** Untrivial-ai/agent-orchestrator @ `742c77bc` (see `AO_BASELINE_SHA.txt`)  
**Target:** B (~full wishlist)  
**Estimate:** ~4–6 **working** weeks for Target B from here (Phase 2A closed; 2B/3A/3B + integration remain)

This document is the living plan: **what landed**, **what remains**, **order**, and **gates**.  
Canonical product design remains `MASTER_PLAN.md`; this file tracks execution status.

**Keep this file in sync whenever a phase or major slice lands** (status tables, HEAD, next action, DoD checkboxes).

---

## Snapshot (now)

| Area | Status |
|------|--------|
| Phase 1 foundation | **Accepted** (Codex RO + registry + CLI roleMap + Option A; Claude RO false by design) |
| Phase 1 **strict exit** (1-F full dogfood + Claude RO etc.) | **Still open** — can proceed in parallel |
| Phase 2A manager saga | **Done** (switch / fresh / recover / ledger / fences) |
| Phase 2A Service/API/CLI + auth | **Accepted** @ `83f7abfb` |
| Phase 2A manager dogfood | **Done** — `PHASE2A_DOGFOOD.md` @ `2d19ad59` |
| Phase 2A live dogfood | **Accepted** — footer + clean crash ledger after data-dir lease |
| Target-authoritative switch prompt | **Done** @ `a3bc32be` (live footer `Harness: codex`) |
| Concurrent daemon ownership lease | **Done** — `datadirlock` on `AO_DATA_DIR` before store/reconcile |
| `switch_supported` production | **true** for Claude/Codex (promoted after 2A close-out accept) |
| Phase 2B-0a/0b (ownership + uniqueness) | **Complete** — project gate, migration 0057, fail-closed boot chain, constraint mapping, launch-cleanup hardening, **and boot restore now gated with deterministic survivor selection + marker neutralization + a single ownership resolver** |
| Phase 2B-1 (orchestrator in-place fresh) | **Landed + live-dogfooded** (`PHASE2B1_LIVE_DOGFOOD.md`) — first *product-visible* 2B behaviour. `FreshOrchestratorConversation` gates the project **before** the switch fence, keeps session id/worktree/branch, and compiles `ObservedOrchestratorV1` (the project's live+terminated worker fleet, read from AO's session table) into the handoff. Cross-harness explicitly refused (2B-3) |
| Phase 2B-2 (replacement recoverability) | **Landed** — migration 0058 persists replacement intent **before** retirement, so a zero-owner interval is never terminal; boot recovery re-drives stranded projects under the project gate. `finalizeRetirement` reordered so a crash leaves the recoverable residue, and both residues are repaired at boot |
| Phase 2B-3 (cross-harness orchestrator switch) | **BLOCKED / DEFERRED on 1-F (Claude RO)** — not merely unstarted. A strict orchestrator must be `workspaceWrites:false`, which requires `read_only_enforced`, which only Codex advertises. Until Claude RO lands this slice cannot be built for strict projects, and it is deliberately refused for non-strict ones too rather than ship a capability strict projects can never have. **Phase 2B is therefore NOT complete** |
| Phase 3A-1 (durable pause primitive) | **Landed** @ `506467f5`, **hardened after review**. Migration 0060 pins `domain.SessionPause`. Three enforcement points, not one: (a) `sessionguard` fences AO-initiated pane writes; (b) **boot** skips paused sessions in post-stop recovery, the live pass's save-and-teardown, and `RestoreAll`'s worker loop + orchestrator election — a pause that let boot relaunch the agent would only have been quiet until the next restart; (c) `LimitEnvelopeV1` — versioned, size-bounded, object-only, unknown fields rejected — so `"I hit a limit"` cannot masquerade as structured evidence. Persistence is **column-owned** (`SetSessionPauseIfAbsent` / `ClearSessionPauseIfIncident`), never a read-modify-write, so a stale full-row writer cannot clear the pin. Incident ids are **caller-supplied and required**, which is what makes a retry after a failed pin write idempotent. **No scheduler, no timer, no auto-resume**; `RetryAfter` is recorded but never scheduled against. *(At the time this landed there was no caller; 3A-2a added the operator endpoints and 3A-2b the detection seam — see those rows.)* |
| Phase 3A-2a (pause/resume surface) | **Backend/API: landed + accepted.** Operator-owned endpoints (LAN or operator credential; agent principals refused), contract in `PHASE3A_PAUSE_CONTRACT.md`, read-model pause view, four sentinels mapped. **Desktop UX: NOT started** — see the completion-axis note below |
| **Upstream integration** | **BLOCKING 3A-2b** — 51 behind, 4 migration collisions, 9 hand-merge files (13 conflicts, 4 of them generated). `UPSTREAM_SYNC_PLAN.md`. Renumbering alone is **not sufficient**: goose keys on version number, so on any database that ran this branch upstream's 0042/0043/0044/0047 would be silently SKIPPED |
| Phase 3A-2b (structured limit detection) | **Seam landed; every harness unsupported.** `internal/limits` is the only way a limit can enter: a versioned `Event` from a harness adapter, a `Detector` the adapter implements, a capability gate checked BEFORE the detector, re-validation of the detector's envelope, and a stable incident id derived from the envelope (never receipt time) so duplicate delivery converges. Routes into the existing idempotent `PauseSession` — no second pause path. **No text parsing, no regex, no scheduler, no retry.** The registry ships EMPTY and `limit_detection_supported` stays false; a harness needs captured sanitized vendor fixtures plus a separate promotion |
| Phase 3B | **Not started** |
| **CI merge gate — `gofmt`** | **CLEARED** @ `57067f4c`. `go.yml`'s build-test job runs `gofmt -l .` and fails on any output; nine files had never been gofmt'd (struct-tag alignment only), so that step failed *before* the tests ran. Fixed mechanically; also removed all 9 goimports lint findings |
| **CI merge gate — `golangci-lint`** | ~~**NOT clean**~~ **CLEARED @ `b2059594`: 0 issues.** The dupl findings were the five duplicated row adapters — the exact place `pause_json` was half-wired in 3A-1 — so they were removed by switching the queries to `sqlc.embed(sessions)` (580 lines deleted) rather than suppressed. Historical note: **31 findings** (was 40 before the gofmt pass). `go.yml` blocks on the full ruleset at zero findings ("any new issue fails CI rather than being grandfathered"), so the branch is unmergeable until this is cleared. **Pre-existing, not roles-slice debt** — measured 42 at `f091af2e` versus 40 at `e38ae32d`; both the 2B delta and 3A-1 *reduced* the count and added none. Concentrated in fork-only files (`session_manager/switch.go` 12, `manager.go` 4, `roles/*` 4, `role_resolve*.go` 3). Remainder is not mechanical: errorlint 18, dupl 5, errcheck 2, revive 2, wastedassign 2, nilerr 1, staticcheck 1 |
| **CI merge gate — tests** | `npm run lint` runs `go test ./...` first, which has intermittently failed before reaching lint on adapter auth tests (`fake`, `kilocode`, `opencode` — context deadlines). **Flaky, not consistently failing**: those three packages passed cleanly on a targeted re-run, so treat them as timing-sensitive under full-suite load, not broken. Frontend `vitest` has 6 **reproducible** pre-existing failures (5 in `src/landing/scripts/generate-markdown-twins.test.mjs`, 1 in `src/renderer/lib/api-client.test.ts`), confirmed on a stashed tree — unrelated to the roles work, and blocking |

> **Two completion axes, tracked separately.** A slice can be complete on the
> **backend/API/CLI** axis and untouched on the **desktop UX** axis; "landed"
> below means the former unless a row says otherwise. Everything in Phase 3A is
> currently backend/API-complete and desktop-absent: pause, resume, switch,
> fresh conversation and the whole role map are reachable only by API or CLI.
> An external UI review confirmed that, and it is the next slice — not a
> reporting gap.

**Next eng (critical path):** **upstream integration first** — see
`UPSTREAM_SYNC_PLAN.md`. The branch is 51 commits behind `upstream/main` with
four migration-number collisions (0042/0043/0044/0047) and upstream already at
0052, so a direct merge is unsafe. Building 3A-2b against the pre-sync
architecture would create a second event path alongside upstream's event-driven
usage plumbing. **Phase 3A-2b resumes after the sync and a re-run of the 2A/2B
live dogfood.** **2B-3** (cross-harness orchestrator switch) stays blocked on **Phase 1-F / Claude RO**, which remains parallel; 2B-1 deliberately refuses cross-harness today.

---

## 1. Completed (accepted)

### Phase 0 (partial) — fork foundation

| Item | Status | Notes |
|------|--------|--------|
| Fork + pin AO baseline | **Done** | `FORK.md`, `AO_BASELINE_SHA.txt` |
| Own migration numbers | **Done** | originally 0042–0049; **renumbered to 0053–0060** for the upstream sync (see `UPSTREAM_SYNC_PLAN.md`) |
| GitHub origin | **Done** | `maplenk/agent-orchestrator-roles` |
| Profiles (orchestrator, implementor, ui-implementor, reviewer) | **Done** | `profiles/*.md` (shipped host templates) |
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
| Migration **0053** durable role columns + `template_artifacts` CAS | **Done** | Accepted |
| Spawn dual-write CAS; restore-by-artifact | **Done** | Fail closed missing/incomplete/conflict |
| Partial pin hydration + incomplete pin fail-closed | **Done** | Store + restore |
| Session-scoped spawn capability (random token, hash only) | **Done** | Migration **0054** |
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
| Lifecycle ledger 0055 + store | **Done** | `AppendLifecycleLedger` / list |
| SwitchPending migration 0056 | **Done** | `switch_pending_json` |
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
| 2B-0b Coordinator uniqueness | **Landed.** Migration 0057 partial unique index + reconciliation, capturing each loser's execution identity into `orchestrator_reap_queue` before clearing it; fail-closed boot reaper draining that queue ahead of every surface; unique-constraint errors mapped to `ErrActiveOrchestratorExists` (409) instead of an opaque 500; `MarkSpawned` launch-cleanup window hardened so a failed launch leaves neither an untracked runtime nor a phantom-live row, with `ErrLaunchCleanupUnresolved` propagated through restore *and* post_stop recovery to a fatal boot gate. **Boot restore closed the last ungated path:** `RestoreAll` now restores at most one orchestrator per project under that project's gate, held across *both* the survivor decision and the restore, because `workspace.Restore` adopts the shared canonical worktree before any row flips — so the index alone never sees the damage. Losing candidates and candidates displaced by an already-live owner have their markers neutralized (rows only; the preserved ref survives), mirroring 0057 — and neutralization is a **durable precondition** of restoring the winner, not best-effort: a surviving loser marker does not stay a loser, so once the winner is killed that stale row becomes the only restorable orchestrator and a later boot resurrects the session this election superseded. A marker **read** failure is likewise not an absence: it abandons the whole project's election rather than letting an older candidate be promoted on incomplete evidence into the shared canonical worktree. All three failures — undeletable loser marker, unreadable marker, unreadable project session list — are boot-fatal via `ErrBootUnsafe`, the shared marker the daemon gate keys on, so a new fail-closed condition becomes fatal by wrapping it rather than by editing `daemon.go`. Not having *looked* leaves the identical durable hazard as having failed to *delete*: an unexamined marker is still eligible, so killing the current owner would let a predecessor return on a later boot. Each leaf's membership is pinned by a table test, since the gate keys only on the parent and an unwrapped leaf would silently stop being fatal. `activeOrchestratorSessionID` now applies `newestOrchestratorRecord` too: first-match-in-list-order returned the *oldest* active orchestrator, so workers spawned while two were briefly active were told to report to the one being superseded |
| 2B-1 In-place orchestrator fresh conversation | **Landed.** `KindWorker` guards parameterized across manager saga, recovery and service; `SwitchWorker` stays worker-only as an entry point while `FreshOrchestratorConversation` takes the **project gate before `beginSwitch`** (lock order `projectOwnership -> beginSwitch`, never inverted — otherwise `EnsureOrchestrator` could retire the session mid-saga). New ledger kind `orchestrator_fresh_conversation` so recovery and audit can tell the sagas apart. `Reconcile` post_stop recovery now includes orchestrators, which previously left a crashed mid-switch project with no coordinator. `ObservedOrchestratorV1` compiles the project's fleet — live **and** terminated workers, read from the session table rather than the outgoing agent's recollection — into the handoff; an unreadable fleet degrades rather than aborts, since it is context and the switch is remedying context loss. In-place keeps the session id, so live workers (whose prompts embed it at spawn/restore only) never need rebinding |
| 2B-2 Replacement durable recoverability | **Landed.** Migration 0058 `orchestrator_replacement_intent`, one row per project, written **before** the first destructive step — a failure to record it aborts before anything is destroyed, since retiring without it is the one ordering that strands a project silently. Retained when spawn fails (that IS the state recovery exists for), discharged only once a successor is live. Boot recovery re-drives stranded projects under the project gate, after `Reconcile` so an adopted crash-survivor counts as the owner rather than being spawned over; it is logged rather than boot-fatal because a project without a coordinator is inert and stopping an otherwise-healthy daemon is the worse outcome. `finalizeRetirement` now terminates **before** releasing the claim: the two writes cannot be one, so the ordering picks the residue, and a terminated row with a stale path is guarded and repairable while an ACTIVE row owning no workspace occupies the project's only slot and is handed out by `EnsureOrchestrator`'s idempotent path. `reconcileOrchestratorRetirement` repairs both residues at boot |
| 2B-3 Cross-harness orchestrator switch | Non-strict only — **strict is blocked on 1-B (Claude RO)**, since a strict orchestrator must be `workspaceWrites:false` and only Codex enforces RO |
| Deferred | Successor-session handoff (needs live-worker rebind + worktree release sequencing) |

**Estimate correction:** MASTER_PLAN §8's 3–5 d did not account for coordinator
uniqueness or replacement recovery.

**Roadmap correction:** 1-B (Claude RO) is *not* fully parallel — it blocks
cross-harness orchestrator switch on strict projects (2B-3).

---

### Phase 3A — Limits + durable pause (~4–6 working days)

| Task | Detail | Status |
|------|--------|--------|
| Structured/reviewed limit envelopes only | Never free-text “I hit a limit” | **Rule enforced** (3A-1): closed reason set, `usage_limit` requires `detectedBy=structured_envelope` + a non-empty envelope, checked on encode and decode. **Producing** the envelopes per harness is 3A-2 |
| Durable pause | Zero automatic send/restart | **Done** (3A-1): migration 0060 pin, fenced in `sessionguard` by write origin. No scheduler/timer/auto-resume exists; `RetryAfter` is advisory only |
| Ledger events | pause / resume | **Done** (3A-1): idempotent per incident, written *before* the pin |
| Operator/service surface | pause + resume through service/API/CLI | **3A-2** — the manager methods have no caller yet |
| Registry | Promote `limit_detection_supported` only after structured-limit tests | **Not started; stays false** |

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

**Required before 3A-2 exposes `resume` (review condition, not optional):**
define the UX for an **active-but-dead paused row** — the state the boot skips
deliberately create. Clearing the pin does **not** relaunch the agent, and it
must not: manual continue (3B) or an explicit agent restore is a *second*,
separate operation the human chooses. The surface therefore has to show three
distinct things — paused / agent alive?, resume, restart — rather than one
"resume" button whose behaviour silently depends on whether the runtime
happens to still be there. `ResumeSession` also requires the caller to name the
incident it is answering, so the surface must carry that id through from
whatever displayed the pause, not re-read it at submit time.

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
Now ──► 3A pause ──► 3B continue/failover   ║   2B-3 cross-harness (needs Claude RO)
     ──► 3A pause ──► 3B continue/failover  (then promote limit_detection)
     ──► Integration
     ║
     ╚═ parallel: Phase 1-F strict dogfood / Claude RO when prioritized
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
10. Lifecycle ledger for switch/fresh (pause/failover later)
11. ObservedWorkspace verified only with AO provenance
12. Failover default manual
14. New-session template authority Option A
15. Role map writable via CLI/API (no silent drop)
16. Crash-recover ledger free of unexplained `failed` (root cause was dual daemon ownership; fixed with datadirlock)
17. Production `switch_supported` true for Claude/Codex after accepted 2A close-out

Still open:

7. Limit → durable pause, zero auto send/restart
8. Failover preserves `role_id`, respects incident bound (runtime path)
13. Read-only roles only on `read_only_enforced` harnesses with **Claude** negative proof

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
17. [~] Limit pause — **backend/API done, desktop UX not started.** Durable pin + boot fencing (3A-1), operator pause/resume endpoints (3A-2a), and the structured detection seam with every harness unsupported (3A-2b). No renderer surface, and no harness detector
18. [ ] Manual continue + opt-in auto-failover
19. [x] Dogfood against switch DoD — manager + live evidence; Claude/Codex `switch_supported` promoted

---

## 6. Process notes

- **After each slice / phase land:** update **this file** (status, HEAD, next action, checklist).
- **Codex review packs** + accept before promoting capability cells.
- **Migrations:** never edit merged SQL; next number **0061+** (0053–0060 are the fork's, renumbered above upstream's 0052).
- **Upstream:** keep `upstream` remote; avoid colliding migration IDs.
- **Dogfood:** isolated `AO_DATA_DIR` for risky runs.
- **Promotion:** never flip `switch_supported` / `limit_detection_supported` in the same change as large feature work when possible — separate final CL. (2A switch promote followed this rule.)

---

## 7. Immediate next action

1. ~~Accept 2A close-out + promote `SwitchSupported` for Claude/Codex~~ — **done**.
2. ~~Start **Phase 2B**~~ — **2B-0a, 2B-0b, 2B-1 and 2B-2 complete**. Next: **Phase 3A** (limits → durable pause). **Phase 1-F / Claude RO** stays parallel and gates the remaining 2B-3.
3. Keep `limit_detection_supported` false until Phase 3 structured-limit evidence.
