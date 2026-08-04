# Multi-sub roles — status & remaining plan

**Repo:** https://github.com/maplenk/agent-orchestrator-roles  
**Branch:** `roles/multi-sub-v1`  
**HEAD:** (see latest commit; update on each land)  
**Baseline:** Untrivial-ai/agent-orchestrator @ `742c77bc` (see `AO_BASELINE_SHA.txt`)  
**Target:** B (~full wishlist)  
**Estimate:** ~4–6 **working** weeks for Target B from here (Phase 2A nearly closed; 2B/3A/3B + integration remain)

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
| Phase 2A live dogfood | **Evidence** — footer + clean crash ledger after data-dir lease |
| Target-authoritative switch prompt | **Done** @ `a3bc32be` (live footer `Harness: codex`) |
| Concurrent daemon ownership lease | **Done** — `datadirlock` on `AO_DATA_DIR` before store/reconcile |
| `switch_supported` production | **false** — promote only after this close-out is accepted |
| Phase 2B / 3A / 3B | **Not started** |

**Next eng (critical path):** accept 2A close-out (lease + clean crash ledger) → **promote `SwitchSupported`** in a separate CL, then Phase 2B.

---

## 1. Completed (accepted)

### Phase 0 (partial) — fork foundation

| Item | Status | Notes |
|------|--------|--------|
| Fork + pin AO baseline | **Done** | `FORK.md`, `AO_BASELINE_SHA.txt` |
| Own migration numbers | **Done** | 0042–0045 on fork |
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
| Runtime capability registry (spawn + RO; switch/limit false) | **Done** | `roles/capabilities`; switch/limit stay false until promote |
| Codex `read_only_enforced` | **Done** | OS sandbox; Claude RO still **false** by design |
| Migration **0042** durable role columns + `template_artifacts` CAS | **Done** | Accepted |
| Spawn dual-write CAS; restore-by-artifact | **Done** | Fail closed missing/incomplete/conflict |
| Partial pin hydration + incomplete pin fail-closed | **Done** | Store + restore |
| Session-scoped spawn capability (random token, hash only) | **Done** | Migration **0043** |
| `canSpawn:false` / terminated / invalid cap → 403 | **Done** | |
| Operator transport (desktop header, LAN authctx, CLI rules) | **Done** | Accepted @ `93fcf1f8` |
| `AO_MANAGED_SESSION` marker (not `AO_DATA_DIR`) | **Done** | |
| Template authority Option A | **Done** | Host/shipped profiles; `AO_ROLE_PROFILES_DIR` |
| Codex review packs (Phase 1 slices) | **Done** | `PHASE1*_CODEX_REVIEW.md`, `PHASE0042_*`, `PHASE_CANSPAWN_*` |

### Phase 2A — landed (caps still false)

| Item | Status | Evidence / HEAD |
|------|--------|-----------------|
| SemanticHandoffV1 + ObservedWorkspaceV1 | **Done** | `domain/handoff.go` |
| Compiler (observed overrides semantic) | **Done** | `handoff/compile.go` |
| Lifecycle ledger 0044 + store | **Done** | `AppendLifecycleLedger` / list |
| SwitchPending migration 0045 | **Done** | `switch_pending_json` |
| Manager SwitchWorker / Fresh / Recover | **Done** | `session_manager/switch.go` |
| Generation = ForceLaunchID | **Done** | ledger gen == RuntimeLaunchID |
| Probe-driven destroy + confirmed-alive rollback | **Done** | pre-stop source usable |
| Input gates (sessionguard + terminal InputGate) | **Done** | Send / terminal / host DeliverHost |
| Indexed terminal handle lookups | **Done** | `GetSessionByRuntimeHandleID` / pending source |
| Service + exact model auth | **Done** | `ResolveAuthorizedSwitchModel` |
| HTTP + OpenAPI + FE schema | **Done** | `/switch`, `/fresh-conversation` |
| CLI `ao session switch` / `fresh` | **Done** | `spawnCallerHeaders()` no-upgrade |
| Switch auth (operator / LAN / canSpawn + project scope) | **Accepted** | @ `83f7abfb` |
| Failover config-save spawn + RO | **Done** | `ValidateRoleMap`; switch_supported staged for promotion |
| Ephemeral target role footer on switch launch | **Done** | @ `a3bc32be` |
| Manager dogfood checklist | **Done** | `PHASE2A_DOGFOOD.md` @ `2d19ad59` |
| Live dogfood evidence | **Recorded** | `PHASE2A_LIVE_DOGFOOD.md` @ `a3bc32be` / docs `9d30c627` |

Detail trackers: `PHASE2A_PLAN.md`, `PHASE2A_DOGFOOD.md`, `PHASE2A_LIVE_DOGFOOD.md`.

### Explicit non-claims (honesty)

- **Same-UID host isolation** is **not** claimed.
- **Phase 1 strict operational dogfood** is **not** complete (Claude RO, full 1-F matrix).
- **`read_only_enforced`:** **true only for Codex**. Claude/Pi/others false.
- **`switch_supported`:** production **false** for Claude/Codex until promotion CL after live gate.
- Live crash ledger may still show residual `failed` before `target_ack` (~5 ms, no recovery-failed log) — **not closed for promotion**.

---

## 2. Remaining work (ordered)

### Phase 2A close-out (do before promote)

| Task | Status | Detail |
|------|--------|--------|
| Accept target-authoritative prompt fix | **Done** | Live footer + unit tests @ `a3bc32be` |
| Concurrent data-dir ownership lease | **Done** | `datadirlock.Acquire` before store open/reconcile; ephemeral port no longer confers second-owner mutation |
| Concurrent-start regression | **Done** | `datadirlock` exclusive + subprocess one-reconcile-marker tests; dual-daemon smoke: one exits `ErrLocked` |
| Clean crash ledger (no incidental `failed`) | **Done (re-dogfood)** | Offline inject after lease fix: `requested>pre_stop>post_stop>target_ack` only (`lease-crash-gen-1`) |
| Promote `switch_supported` (Claude/Codex) | **Open** | **Separate final CL only** after accept; flips `capabilities.For` + activates failover switch_supported validation |

**DoD (MASTER_PLAN):** pre-stop source usable; post-stop handoff retained; one generation owns input — **implemented**; re-verify after promotion.

---

### Gate G0 / Phase 1 remainder (parallel; not blocking 2A code)

Strict `strictDelegation` as daily driver still wants full Phase 1 exit:

| Slice | Status | Notes |
|-------|--------|--------|
| 1-A RO contract doc | **Partial** | `READ_ONLY_CONTRACT.md` exists; tighten if needed |
| 1-B Claude RO (+ negative runtime) | **Open** | Codex done; Claude stays false |
| 1-C registry | **Done** for Phase 1 cells | Switch/limit promote later |
| 1-D role-map surface | **Done** | CLI/API round-trip used in live dogfood |
| 1-E template authority | **Done** | Option A |
| 1-F full verification + strict dogfood | **Open** | Full `go test ./...`, strict orch RO dogfood, Claude RO when ready |

---

### Phase 2B — Orchestrator ownership transfer (~3–5 working days)

| Task | Detail |
|------|--------|
| Coordinator lease | |
| Nudge/routing rebind | |
| Pending message transfer | |
| Generation fencing + target ack | |
| Recovery | Source dead / successor fails |

---

### Phase 3A — Limits + durable pause (~4–6 working days)

| Task | Detail |
|------|--------|
| Structured/reviewed limit envelopes only | Never free-text “I hit a limit” |
| Durable pause | Zero automatic send/restart |
| Ledger events | pause / resume |
| Registry | Promote `limit_detection_supported` only after structured-limit tests |

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
| Crash recovery | Restore + CAS + switch mid-flight (close residual 2A ledger if still open) |
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
| 2A promotion gate (failed-ledger + flip caps) | 1–2 | Accept live residual |
| 1-B Claude RO (optional parallel) | 3–5 | 1-A |
| 1-F Phase 1 strict exit | 2–3 | 1-B if Claude RO required for strict maps |
| Phase 2B | 3–5 | 2A patterns |
| Phase 3A/B | 7–11 | limit detection (promote in 3A) |
| Integration | 3–5 | prior |

### Sequencing sketch

```text
Now ──► 2A close: failed→ack provenance ──► promote switch_supported (separate CL)
     ──► 2B orch transfer
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
9. Unsupported capabilities reject config **and** launch/restore (spawn/RO; switch until promote)
10. Lifecycle ledger for switch/fresh (pause/failover later)
11. ObservedWorkspace verified only with AO provenance
12. Failover default manual
14. New-session template authority Option A
15. Role map writable via CLI/API (no silent drop)

Still open:

7. Limit → durable pause, zero auto send/restart
8. Failover preserves `role_id`, respects incident bound (runtime path)
13. Read-only roles only on `read_only_enforced` harnesses with **Claude** negative proof
16. ~~Crash-recover ledger free of unexplained `failed`~~ — root cause was dual daemon ownership; fixed with datadirlock
17. Production `switch_supported` true only after accepted 2A close-out

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
16. [ ] Orchestrator switch protocol
17. [ ] Limit pause
18. [ ] Manual continue + opt-in auto-failover
19. [~] Dogfood against switch DoD — manager + live evidence; promotion gate open

---

## 6. Process notes

- **After each slice / phase land:** update **this file** (status, HEAD, next action, checklist).
- **Codex review packs** + accept before promoting capability cells.
- **Migrations:** never edit merged SQL; next numbers **0046+**.
- **Upstream:** keep `upstream` remote; avoid colliding migration IDs.
- **Dogfood:** isolated `AO_DATA_DIR`; local-only cap enablement must stay **non-committable** until promote CL.
- **Promotion:** never flip `switch_supported` / `limit_detection_supported` in the same change as large feature work when possible — separate final CL.

---

## 7. Immediate next action

1. **Accept** data-dir ownership lease + clean crash ledger re-dogfood (this land).
2. On accept → **promote `SwitchSupported`** for Claude/Codex in a dedicated commit.
3. Then **Phase 2B** (orch ownership) or **Phase 1-F / Claude RO** per product priority.
