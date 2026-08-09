# Multi-sub roles — status & remaining plan

**Repo:** https://github.com/maplenk/agent-orchestrator-roles  
**Accepted implementation/runner:** `166e9e63`  
**Promoted live evidence:** `322f9c18`  
**Post-evidence review integration:** `72f274a7`
**Installed-app role-pipeline close-out:** `f4b28012`
**Existing-project starter-role close-out:** `762ae160`
**Target B config-containment hardening:** `8bb1e3af`
**Target B migration-history hardening:** `586d1156`
**Target B adapter test hygiene:** `66d65e4e`
**Target B vendor-limit fixture research:** `11d12414`
**Target B atomic role-map CAS API:** `54cdbdd8`
**Target B desktop role-map editor:** `c7c1f565`
**Target B dormant automatic-failover engine:** `1c97c55e`

**Evidence integration branch:** `codex/mvp-integration`  
**Target roles trunk:** `roles/multi-sub-v1` (merge not yet claimed)
**Baseline:** Untrivial-ai/agent-orchestrator @ `fa799a7a58e2f9ec13d174567aff436ba890ff6a` (see `AO_BASELINE_SHA.txt`)
**Target:** B (~full wishlist)  
**Current gate:** the promoted `166e9e63` live matrix and the post-evidence
default-data-dir runtime replay on `3c3aef51` are complete; independent combined
review approves integration `72f274a7` with no remaining P1/P2. Target B test
hygiene removes the fake/Kilocode/OpenCode wall-clock trio without changing
production deadlines or classifier behavior. Vendor research at `11d12414`
captures positive sanitized Claude refusal projections and negative Codex quota
snapshots without adding a production detector or promoting a capability; the
ordinary full backend run now passes 4,737 tests across 132 packages. Static
checks, typecheck, API drift, and the authoritative full frontend gate also pass.
The desktop role-map editor is implemented at `c7c1f565` on the atomic role-map
CAS boundary from `54cdbdd8`, independently reviewed, fully gated, and accepted
in the real native Forge Electron app; see `TARGET_B_ROLE_MAP_EDITOR_20260809.md`.
The one-shot automatic-failover engine, fail-closed config gate, exact-incident
concurrency fence, and boot recovery are implemented and independently reviewed
at `1c97c55e`; see
[`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md).
This is dormant infrastructure, not an operational vendor feature: every
production `limit_detection_supported` cell is false, the detector registry is
empty, `limits.Router` has no production caller, and no real positive detector
with a stable incident key has been accepted.

This document is the living plan: **what landed**, **what remains**, **order**, and **gates**.  
The current MVP boundary is [`MVP_FINAL_SPEC.md`](MVP_FINAL_SPEC.md); canonical
long-range design remains `MASTER_PLAN.md`. This file tracks execution status.

**Keep this file in sync whenever a phase or major slice lands** (status tables, HEAD, next action, DoD checkboxes).

---

## Snapshot (now)

| Area | Status |
|------|--------|
| Phase 1 foundation | **Accepted.** Codex read-only, capability registry, CLI/API role map, template CAS and host-authoritative role resolution are in production paths. `762ae160` also persists a non-strict five-role starter catalog for new and existing projects with no authored map. |
| Phase 1-B / strict exit | **Open for explicitly read-only Claude roles.** Claude remains `read_only_enforced=false`; writable strict orchestration no longer depends on that capability. |
| Phase 2A | **Accepted and promoted.** Worker switch/fresh/recovery, ledger, input fences, API/CLI/auth and live dogfood are complete; Claude/Codex `switch_supported=true`. |
| Phase 2B-0/1/2 | **Landed and dogfooded.** Coordinator uniqueness, fail-closed boot, in-place orchestrator fresh conversation and replacement recoverability are present. |
| Phase 2B-3 | **Implemented and live-accepted on `166e9e63`.** Gated in-place Codex↔Claude orchestrator switching uses exact role-map targets and same-generation recovery. Post-review, Chat orchestrators expose no switch/fresh target or desktop control; the service also rejects direct calls before authorization/manager dispatch with `SWITCH_CHAT_UNSUPPORTED`. |
| Phase 3A pause core and operator surface | **Landed and accepted.** Durable pause, ledger-before-pin, zero automatic restart/send, pause/resume API+CLI, ownership CAS and pause-aware lifecycle are present. |
| Phase 3A desktop | **Landed and live-dogfooded.** The inspector distinguishes paused-live from paused-dead and Resume from Restart agent; the strict composer sends role-only requests. The desktop role-map editor is implemented, independently reviewed, fully gated, and accepted in the real native Forge Electron app at `c7c1f565`. |
| Phase 3A-2b detector boundary | **Landed; fixture research captured; no harness promoted.** `internal/limits` remains the only production ingress and the detector registry is empty. Claude has real structured 429 refusal projections, but those records expose only a per-request ID—not a stable quota-window occurrence key—so no safe durable `SourceKey` is proven. Codex has real structured quota-state snapshots but no reached/refused frame. `limit_detection_supported=false` remains universal. |
| Phase 3B | **Manual Continue remains implemented, reviewed, and live-accepted; dormant automatic engine implemented and reviewed.** All 12 final manual records passed on `166e9e63`. `1c97c55e` reuses that exact transaction for one opt-in automatic attempt and same-generation recovery without changing manual Continue or pause/switch ownership. Full product automatic failover remains partial because no production detector/caller/capability exists. |
| Installed strict role pipeline | **Accepted.** The replaced real app completed Claude orchestrator → Grok implementor → read-only Codex verifier, native Browser play, and final orchestration. `f4b28012` then proved terminal-only verifier retrieval with no host nudge. `762ae160` additionally upgraded an existing unconfigured repo in place and completed Claude→Codex→Claude from the native Switch menu. See `ROLE_PIPELINE_LIVE_TEST_20260808_FINAL.md` and `ROLE_PIPELINE_LIVE_TEST_20260809.md`. |
| Upstream Sync 2 | **Accepted and MERGED to the roles trunk (2026-08-07) as `5dc2fcfb`.** Pinned to `fa799a7a`; fork migrations are 9000–9007. All eight steps are done and **every required GitHub Actions job is green**; Step 5's two live records are in `UPSTREAM_SYNC2_DOGFOOD_STEP5.md`. See `UPSTREAM_SYNC2_PLAN.md`. |
| Target B durability hardening A | **Implemented, independently reviewed, gated, and isolated-dogfooded at `8bb1e3af`.** Malformed, empty, null, unknown-field, and forward-role-schema project config is contained to one degraded row; other projects remain listable; raw bytes are preserved; all row mutations and dev import are fenced. See `TARGET_B_DURABILITY_HARDENING_20260809.md`. |
| Target B durability hardening B | **Implemented, independently reviewed, gated, and isolated-daemon dogfooded at `586d1156`.** A complete on-entry ledger/schema snapshot retains a genuine lone Muse 53, still clears the complete stale 53–60 block, preserves already-repaired rows, refuses partial/incomplete ambiguity before writes, and rolls back mid-rewrite failures exactly. No migration was added or edited. See `TARGET_B_DURABILITY_HARDENING_20260809.md`. |
| Target B adapter test hygiene | **Implemented, independently reviewed, and gated at `66d65e4e`.** Fake lifecycle cadence is asserted structurally; Kilocode interactive-shell avoidance and CLI parsing are tested independently; OpenCode CLI classification is a pure, behavior-preserving helper with a load-bearing command-error case. Production three-second deadlines are unchanged. See `TARGET_B_TEST_HYGIENE_20260809.md`. |
| Target B vendor-limit fixtures | **Captured, sanitized, independently reviewed, and gated at `11d12414`; research-only.** Two real Claude Code 2.1.224 structured 429 refusal projections and two real Codex 0.146/0.147 quota frames are adapter-local, byte-bound, and replayed by test-only classifiers/normalizers. Claude proves refusal but not stable incident identity; Codex proves structured state but not refusal. See `TARGET_B_VENDOR_LIMIT_FIXTURES_20260809.md`. |
| Target B desktop role-map editor | **Implemented, independently reviewed, fully gated, and native-Electron accepted at `54cdbdd8` + `c7c1f565`.** Healthy project reads carry a required `roleMapSha256`. The role-only atomic patch compares that revision while preserving the latest unrelated config, and full-settings saves use the inverse CAS so a stale whole-config writer cannot restore an older role map. Degraded projects remain identity-only and read-only in the desktop. The editor covers strict mode, orchestrator role, role bindings/policy, and failover ladders without a migration or capability promotion. See `TARGET_B_ROLE_MAP_EDITOR_20260809.md`. |
| Target B automatic failover | **Dormant engine implemented and independently reviewed at `1c97c55e`; full product remains partial.** A structured limit is durably paused before the manager makes one exact-incident automatic decision; the accepted Continue transaction owns the attempt ledger, target selection, switch generation, failure accounting, pin clear, and boot convergence. Duplicate delivery cannot spend a second rung, and boot recovery reuses the same generation only after the board-wide safety gate. Config save fails closed while limit detection is unpromoted; legacy automatic maps must be explicitly **Convert to manual** before desktop save. No migration, API, prompt, or capability cell changed, and manual Continue/ownership semantics remain intact. The registry is empty, every production `limit_detection_supported` cell is false, `limits.Router` has no production caller, and no real positive detector is accepted, so this is not operational vendor automatic failover. See [`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md). |
| CI | Exact race found zero data races; SQLite exact checks passed 5/5 and its package race run passed in 622.846s; Chat's test-only projector race is fixed at `f8883529`. Fixture-focused normal and race checks each pass 302/302, and the ordinary full backend run passes 4,737 tests across 132 packages on its first run. |

> **Strict does not mean read-only.** A writable strict orchestrator may use
> Claude Code because strictness enforces role/routing/delegation policy, not a
> filesystem sandbox. Claude-only installs still cannot configure an explicit
> `workspaceWrites:false` role; Claude remains `read_only_enforced=false`.

**Next engineering actions:** none gate the MVP. Both Target B durability
hardening slices and the isolated adapter test-hygiene slice are complete.
Vendor fixture capture is complete as a non-promoting research slice, and the
desktop role-map editor is implemented and native-Electron accepted. The
dormant automatic engine is also implemented and reviewed, but the Master Plan's
full automatic product trigger remains partial until a real positive detector,
production Router caller, and separate capability promotion are accepted. The
next optional engineering slice is Claude technical read-only; successor-
orchestrator/live-worker rebinding and Pi/Muse switching remain later options.
Do not rewrite or rerun the accepted MVP matrices merely to replace historical
evidence.

---

## 1. Completed (accepted)

### Phase 0 (partial) — fork foundation

| Item | Status | Notes |
|------|--------|--------|
| Fork + pin AO baseline | **Done** | `FORK.md`, `AO_BASELINE_SHA.txt` |
| Own migration numbers | **Done for unambiguous histories** | Fork history is now `9000`–`9007`; `a62b2ee7` repairs databases carrying both abandoned fork ranges by preserving 42–49, freeing 53–60, and recording 9000–9007 once. A full 42–49 range plus a lone upstream Muse 53 remains an explicitly deferred ambiguous-ledger case below. See `UPSTREAM_SYNC2_PLAN.md` |
| GitHub origin | **Done** | `maplenk/agent-orchestrator-roles` |
| Profiles (orchestrator, implementor, ui-implementor, reviewer, verifier) | **Done** | `profiles/*.md` (shipped host templates) |
| Capability matrix | **Partial → runtime exists** | `capabilities.For` is source of truth; `CAPABILITY_MATRIX.md` docs mirror |

### Phase 1 — closed slices (not full Phase 1 exit)

| Item | Status | Evidence |
|------|--------|----------|
| RoleMap + RoleBinding + RoleExecutionPolicy + FailoverConfig | **Done** | `domain/rolemap.go` |
| Default starter role catalog | **Done** | `762ae160`: new projects and boot-time existing projects with zero role map receive non-strict orchestrator/implementor/ui/reviewer/verifier bindings; configured project harness/model preferences are preserved; Claude↔Codex is the default orchestrator alternate when applicable |
| ProjectConfig.roleMap + Validate (domain) | **Done** | `domain/projectconfig.go` |
| CLI / API roleMap round-trip | **Done** | CLI `roleMap` mirror + set-config; live dogfood set roleMap via CLI |
| Strict role JSON + durable read safety | **Done** | HTTP/CLI require both permission booleans and reject unknown binding fields; `663f9339` makes malformed persisted bindings fail reads without rewriting their bytes while valid semantics/SHA/bytes remain stable |
| Per-project unreadable-config containment | **Done (`8bb1e3af`)** | Reads now contain malformed or forward-versioned config to one degraded project entry while preserving its raw SQLite bytes. Healthy projects remain listable. Strict one-row reads and every project-row mutation, workspace import, and dev-import path refuse the unreadable row with a typed error / stable `PROJECT_CONFIG_UNREADABLE` 409. Boot seeding and tracker intake skip the bad config without treating it as zero. Native Electron dogfood proved the visible disabled mutation surface. |
| Mixed-history migration ambiguity | **Done (`586d1156`)** | Repair snapshots the latest applied truth for 42–49, 53–60, and 9000–9007 plus all physical fork fingerprints and the Muse CHECK before any write. Complete original + genuine lone Muse preserves the exact 53 row; complete stale 53–60 cleanup still wins even with Muse text; corresponding 900x-on-entry rows stay protected. Partial masks, incomplete schema capable of deleting an old row, and lone 53 without Muse fail loudly with exact rollback. Burned upstream 42–53 histories with no fork effects retain the established reconciliation path. |
| Host-authoritative `ao spawn --role` | **Done** | CLI + HTTP `roleId`; Resolve rejects free-form harness with role |
| Strict: role required for workers; no harness override | **Done** | `roles/resolve.go` |
| Strict orch auto-bind `orchestratorRole` | **Done** | Pins routing/delegation policy; explicit read-only remains capability-gated |
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
- **Final writable-strict operational dogfood** passed on immutable SHA
  `166e9e63`; the later default-data-dir probe correction also passed its
  separate targeted replay on `3c3aef51`. Claude RO remains an optional exit for
  explicitly read-only Claude roles.
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

Explicit Claude read-only roles still want full Phase 1 exit; writable strict
orchestration is independently usable:

| Slice | Status | Notes |
|-------|--------|--------|
| 1-A RO contract doc | **Partial** | `READ_ONLY_CONTRACT.md` exists; tighten if needed |
| 1-B Claude RO (+ negative runtime) | **Open** | Codex done; Claude stays false |
| 1-C registry | **Done** for Phase 1 + 2A switch cells | Limit promote later |
| 1-D role-map surface | **Done** | CLI/API round-trip used in live dogfood; atomic role-only CAS API at `54cdbdd8` and installed desktop editor at `c7c1f565` |
| 1-E template authority | **Done** | Option A |
| 1-F full verification + strict dogfood | **Open** | Full `go test ./...`; writable strict orchestrator plus explicit RO-role coverage; Claude RO when ready |

---

### Phase 2B — Orchestrator ownership transfer (~6–11 working days)

**Plan:** `PHASE2B_PLAN.md` (**2B-0a, 2B-0b, 2B-1, 2B-2 and the final-MVP
2B-3 in-place switch are implemented**).
Scope decided: **in-place switch now, successor-session handoff deferred**;
**fence only, no new durable inbox** (upstream shipped and reverted durable
orchestrator coordination twice — `0025`→`0037`, `0038`→`0039`).

| Slice | Detail |
|-------|--------|
| 2B-0a Project ownership gate | **Landed.** Manager-owned, project-keyed exclusion; `EnsureOrchestrator` is the single gated ownership command. All public orchestrator mutations self-acquire (`Spawn`, `Retire`, `Restore`, `Kill`, `Resume`, `Rollback`, `Cleanup`). Also fixed the canonical-workspace alias: a retired row kept naming the path its successor owned, so `Kill`/`Cleanup` on the predecessor destroyed the live orchestrator's worktree. Service delegates and keeps auth/telemetry/presentation outside the gate. `RestoreAll` was carried into 2B-0b and is gated there |
| 2B-0b Coordinator uniqueness | **Landed.** Migration 9004 partial unique index + reconciliation, capturing each loser's execution identity into `orchestrator_reap_queue` before clearing it; fail-closed boot reaper draining that queue ahead of every surface; unique-constraint errors mapped to `ErrActiveOrchestratorExists` (409) instead of an opaque 500; `MarkSpawned` launch-cleanup window hardened so a failed launch leaves neither an untracked runtime nor a phantom-live row, with `ErrLaunchCleanupUnresolved` propagated through restore *and* post_stop recovery to a fatal boot gate. **Boot restore closed the last ungated path:** `RestoreAll` now restores at most one orchestrator per project under that project's gate, held across *both* the survivor decision and the restore, because `workspace.Restore` adopts the shared canonical worktree before any row flips — so the index alone never sees the damage. Losing candidates and candidates displaced by an already-live owner have their markers neutralized (rows only; the preserved ref survives), mirroring 9004 — and neutralization is a **durable precondition** of restoring the winner, not best-effort: a surviving loser marker does not stay a loser, so once the winner is killed that stale row becomes the only restorable orchestrator and a later boot resurrects the session this election superseded. A marker **read** failure is likewise not an absence: it abandons the whole project's election rather than letting an older candidate be promoted on incomplete evidence into the shared canonical worktree. All three failures — undeletable loser marker, unreadable marker, unreadable project session list — are boot-fatal via `ErrBootUnsafe`, the shared marker the daemon gate keys on, so a new fail-closed condition becomes fatal by wrapping it rather than by editing `daemon.go`. Not having *looked* leaves the identical durable hazard as having failed to *delete*: an unexamined marker is still eligible, so killing the current owner would let a predecessor return on a later boot. Each leaf's membership is pinned by a table test, since the gate keys only on the parent and an unwrapped leaf would silently stop being fatal. `activeOrchestratorSessionID` now applies `newestOrchestratorRecord` too: first-match-in-list-order returned the *oldest* active orchestrator, so workers spawned while two were briefly active were told to report to the one being superseded |
| 2B-1 In-place orchestrator fresh conversation | **Landed.** `KindWorker` guards parameterized across manager saga, recovery and service; `SwitchWorker` stays worker-only as an entry point while `FreshOrchestratorConversation` takes the **project gate before `beginSwitch`** (lock order `projectOwnership -> beginSwitch`, never inverted — otherwise `EnsureOrchestrator` could retire the session mid-saga). New ledger kind `orchestrator_fresh_conversation` so recovery and audit can tell the sagas apart. `Reconcile` post_stop recovery now includes orchestrators, which previously left a crashed mid-switch project with no coordinator. `ObservedOrchestratorV1` compiles the project's fleet — live **and** terminated workers, read from the session table rather than the outgoing agent's recollection — into the handoff; an unreadable fleet degrades rather than aborts, since it is context and the switch is remedying context loss. In-place keeps the session id, so live workers (whose prompts embed it at spawn/restore only) never need rebinding |
| 2B-2 Replacement durable recoverability | **Landed.** Migration 9005 `orchestrator_replacement_intent`, one row per project, written **before** the first destructive step — a failure to record it aborts before anything is destroyed, since retiring without it is the one ordering that strands a project silently. Retained when spawn fails (that IS the state recovery exists for), discharged only once a successor is live. Boot recovery re-drives stranded projects under the project gate, after `Reconcile` so an adopted crash-survivor counts as the owner rather than being spawned over; it is logged rather than boot-fatal because a project without a coordinator is inert and stopping an otherwise-healthy daemon is the worse outcome. `finalizeRetirement` now terminates **before** releasing the claim: the two writes cannot be one, so the ordering picks the residue, and a terminated row with a stale path is guarded and repairable while an ACTIVE row owning no workspace occupies the project's only slot and is handed out by `EnsureOrchestrator`'s idempotent path. `reconcileOrchestratorRetirement` repairs both residues at boot |
| 2B-3 Cross-harness orchestrator switch | **Implemented and live-accepted on `166e9e63`; default-role compatibility accepted on `762ae160`.** `SwitchOrchestrator` holds project ownership before the switch fence, reloads session/project under the gate, resolves the exact primary+ladder target through the shared domain resolver, preserves durable identity/permissions, rotates the credential, and recovers the same generation. Cross-harness uses the existing `switch` ledger kind; same-harness fresh keeps `orchestrator_fresh_conversation`. `31b6d7ef` makes the read model truthful for Chat. `762ae160` ensures an unconfigured existing repo gets authorized targets and lets an exact provider-default legacy orchestrator adopt the durable role only at the explicit switch boundary |
| Deferred | Successor-session handoff (needs live-worker rebind + worktree release sequencing) |

**Estimate correction:** MASTER_PLAN §8's 3–5 d did not account for coordinator
uniqueness or replacement recovery.

**Roadmap correction:** the final MVP separates strict delegation from
technical read-only. Phase 1-B remains valuable for explicit read-only Claude
roles but no longer blocks cross-harness orchestrator switch.

---

### Phase 3A — Limits + durable pause (~4–6 working days)

| Task | Detail | Status |
|------|--------|--------|
| Structured/reviewed limit envelopes only | Never free-text “I hit a limit” | **Boundary done; research fixtures captured; vendor adapters still open.** Claude's structured positive refusal is proven, but its request ID cannot be treated as a stable incident key. Codex's structured state is proven, but not a reached/refused event. No production detector is registered. |
| Durable pause | Zero automatic send/restart | **Done** (3A-1): migration 9007 pin; TUI and Chat automatic sends plus boot relaunch are fenced. No scheduler/timer/auto-resume exists; `RetryAfter` is advisory only |
| Ledger events | pause / resume | **Done** (3A-1): idempotent per incident, written *before* the pin |
| Operator/service surface | pause + resume through service/API/CLI | **Done** (3A-2a): operator/LAN only; agent principals explicitly refused; expected incident required |
| Desktop surface | paused/alive state + Resume + Restart agent + strict role composer | **Done and live-dogfooded.** Resume clears only the displayed incident; Restart is a separate operation. Role-map editing remains API/CLI-only |
| Detector registry | Promote `limit_detection_supported` only after structured-limit tests | **Boundary landed; registry empty and every harness remains false.** Fixture capture alone is explicitly non-promoting. |

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

### Phase 3B — Manual Continue accepted; dormant automatic engine implemented

| Task | Detail |
|------|--------|
| Manual continue on next ladder rung | **Implemented, reviewed, and live-accepted.** Default mode is manual; all 12 final records passed on `166e9e63` |
| Failover preserves `role_id` | **Implemented.** Only harness/model/generation and rotated credential change |
| `maxFailoversPerIncident` | **Implemented** as the frozen host bound; exhaustion stays paused |
| `failover.mode=automatic` | **Dormant engine implemented and reviewed at `1c97c55e`; full product partial.** It performs at most one immediate, exact-incident handoff through the accepted Continue transaction and recovers only the same durable generation. It adds no scheduler, retry loop, timer, or automatic resume. Config save refuses automatic mode while production limit detection is unpromoted; legacy stored automatic maps require an explicit desktop **Convert to manual** before save. With every production capability false, an empty detector registry, no production `limits.Router` caller, and no accepted real positive detector, no vendor can trigger it in production. See [`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md). |

---

### Final integration and acceptance

| Task | Detail |
|------|--------|
| Clean full gate | **Green for the current backend:** zero races; SQLite exact 5/5 and package race pass; Chat test race fixed; fixture-focused normal/race each 302/302; ordinary full passes 4,737 tests across 132 packages. Historical accepted evidence remains unchanged. |
| Worker dogfood | **Complete:** all 12 records passed on `166e9e63`; evidence `322f9c18` |
| Orchestrator dogfood | **Complete:** Codex→Claude→Codex, fencing, unauthorized refusal, Fresh and same-generation recovery passed |
| Post-review default-path replay | **Complete on `3c3aef51`:** actual default tmux socket; Restart, boot live reconcile, and boot reap/restore each converged to one active row/runtime; focused tmux/session-manager/reaper race tests passed |
| Promotion | **None.** Limit detection and Claude read-only stay false |

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
| ~~Phase 2B-3 (cross-harness orch)~~ | **Implemented and live-accepted** | Final MVP strict-policy amendment |
| ~~Phase 3B manual Continue~~ | **Implemented and live-accepted** | Final probe review |
| Final repository gate | current backend and authoritative frontend green | Static/typecheck/API drift pass; SessionFilesView fix passes 20/20 exact and 28/28 full file; authoritative full Vitest passes 151/151 files and 2040/2040 tests; current full backend passes 4,737 tests across 132 packages |

### Sequencing sketch

```text
2B-0a/0b ownership + uniqueness ──► LANDED (safety only)
2B-1 orch in-place fresh      ──► LANDED (first user-facing 2B behaviour)
2B-2 replacement recoverability ──► LANDED (intent + crash-consistent retirement)
Sync 2 acceptance ──► DONE (2026-08-07)
3B manual Continue ──► IMPLEMENTED + LIVE-ACCEPTED
Final MVP core + surface ──► IMPLEMENTED / REVIEW FIXES INTEGRATED
Live matrix @ 166e9e63 ──► ACCEPTED (evidence 322f9c18)
Race close-out ──► Chat test fixed; SQLite exact 5/5 + package PASS; zero races
Ordinary backend ──► PASS, 4,737 tests / 132 packages after vendor-fixture research
Static/typecheck ──► PASS on exact head f8883529
API drift ──► PASS (two identical regenerations; clean diff)
Frontend classification ──► deterministic pre-MVP failure (0/20 isolated; full file 27/28)
Frontend fix @ 6473b134 ──► 20/20 exact + 28/28 file PASS; negative mutation retained
Vendor fixture research ──► DONE, Claude positive / Codex negative, no promotion
Desktop role-map editor ──► IMPLEMENTED + REVIEWED + NATIVE-ELECTRON ACCEPTED
Dormant automatic engine @ 1c97c55e ──► IMPLEMENTED + REVIEWED; PRODUCT TRIGGER PARTIAL
Now ──► Claude technical RO / Phase 1-F (optional, explicit read-only roles only)
     ║
     ╠═ later: successor-orchestrator / live-worker rebinding
     ╚═ later: Pi / Muse switching
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

7. **Partial:** durable pause and zero unowned automatic send/restart are
   enforced, and the opt-in one-shot engine plus exact-generation recovery are
   implemented at `1c97c55e`; no harness yet produces a promoted structured
   limit event, the registry is empty, and `limits.Router` has no production
   caller
8. **Satisfied for the final MVP:** failover preserves `role_id`, respects the
   incident bound, and passed the promoted live matrix. **Partial for the full
   Master Plan:** dormant automatic execution is implemented, but the accepted
   positive detector/product trigger and capability promotion do not exist

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
16. [x] Orchestrator in-place switch protocol — fresh conversation,
    Codex↔Claude switch, gated same-generation recovery, and durable replacement
    recovery are implemented and live-accepted.
    Successor-session handoff and live-worker rebind remain deferred
17. [~] Limit pause — **backend/API and the desktop surface landed; no harness detector.** Durable pin + boot fencing (3A-1), operator pause/resume endpoints (3A-2a), the structured detection seam with every harness unsupported (3A-2b), and the renderer paused panel + strict delegation composer (3A-2 UI, live-dogfooded in `PHASE3A2_UI_DOGFOOD.md`). Real Claude/Codex research fixtures are captured at `11d12414`, but neither proves all inputs for a stable production incident key, so capabilities remain false. The desktop now authors and atomically saves the project role map at `54cdbdd8` + `c7c1f565`; installed-app evidence is in `TARGET_B_ROLE_MAP_EDITOR_20260809.md`
18. [~] Manual Continue is implemented and live-accepted. The dormant opt-in
    automatic engine, fail-closed config/runtime gates, one-attempt incident
    accounting, and boot convergence are implemented and reviewed at
    `1c97c55e`, with no migration/API/prompt/capability promotion. Operational
    automatic failover remains partial until a real positive detector,
    production Router caller, and separate capability promotion are accepted
19. [x] Dogfood against switch DoD — manager + live evidence; Claude/Codex `switch_supported` promoted

---

## 6. Process notes

- **After each slice / phase land:** update **this file** (status, HEAD, next action, checklist).
- **Codex review packs** + accept before promoting capability cells.
- **Migrations:** never edit merged SQL; fork migrations use the reserved `9000+` range and the next number is **9009+**. Do not re-enter upstream's sequential range.
- **Upstream:** keep `upstream` remote; avoid colliding migration IDs.
- **Dogfood:** isolated `AO_DATA_DIR` for risky runs.
- **Promotion:** never flip `switch_supported` / `limit_detection_supported` in the same change as large feature work when possible — separate final CL. (2A switch promote followed this rule.)

---

## 7. Immediate next action

The MVP has no remaining implementation or live-acceptance action. Preserve
the distinct evidence sets (`166e9e63`/`322f9c18`, `3c3aef51`, and installed
role-pipeline `f4b28012`) and promote no capability. Per-project config
containment is complete at `8bb1e3af`, mixed-history migration repair at
`586d1156`, adapter wall-clock hygiene at `66d65e4e`, and non-promoting vendor
fixture research at `11d12414`. The atomic role-map CAS API at `54cdbdd8` and
desktop editor at `c7c1f565` are implemented, independently reviewed, fully
gated, and accepted in the installed native Electron app without a migration or
capability promotion. The dormant automatic engine at `1c97c55e` is also
implemented and independently reviewed: it reuses manual Continue, preserves
ownership, and adds no migration, API, prompt, or capability promotion. It does
not complete the Master Plan's product trigger while the detector registry is
empty, every production `limit_detection_supported` cell is false,
`limits.Router` has no production caller, and no real positive detector is
accepted. See
[`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md).

The next optional engineering slice is Claude technical read-only for explicit
read-only roles. Successor-orchestrator/live-worker rebinding and Pi/Muse
switching remain later optional slices. No fork migration is needed for the
automatic engine; **9009+ remains the next available fork migration number**.
