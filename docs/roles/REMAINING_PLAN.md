# Multi-sub roles — status & remaining plan

**Repo:** https://github.com/maplenk/agent-orchestrator-roles
**Branch:** `roles/multi-sub-v1`
**HEAD (canSpawn accepted):** `93fcf1f8`
**Baseline:** Untrivial-ai/agent-orchestrator @ `742c77bc` (see `AO_BASELINE_SHA.txt`)
**Target:** B (~full wishlist)
**Estimate:** ~5–7 **working** weeks for Target B from here (see §3 for assumptions)

This document is the living plan: **what landed**, **what remains**, **order**, and **gates**.
Canonical product design remains `MASTER_PLAN.md`; this file tracks execution status.

**Phase 1 foundation accepted** (Codex RO + registry + CLI roleMap + Option A; Claude RO false by design). **Strict dogfood exit** still open. **Next eng: Phase 2A.**

---

## 1. Completed (accepted)

### Phase 0 (partial) — fork foundation

| Item | Status | Notes |
|------|--------|--------|
| Fork + pin AO baseline | **Done** | `FORK.md`, `AO_BASELINE_SHA.txt` |
| Own migration numbers | **Done** | 0042, 0043 on fork |
| GitHub origin | **Done** | `maplenk/agent-orchestrator-roles` |
| Profiles (orchestrator, implementor, ui-implementor, reviewer) | **Done** | `profiles/*.md` (shipped host templates) |
| Capability matrix doc scaffold | **Partial** | `CAPABILITY_MATRIX.md` is **documentation only** today — not a runtime registry; cells still TBD |

### Phase 1 — closed slices only (not full Phase 1 exit)

| Item | Status | Evidence |
|------|--------|----------|
| RoleMap + RoleBinding + RoleExecutionPolicy + FailoverConfig | **Done** | `domain/rolemap.go` |
| ProjectConfig.roleMap + Validate (domain) | **Done** | `domain/projectconfig.go` — **CLI mirror still omits RoleMap** (see remaining) |
| Host-authoritative `ao spawn --role` | **Done** | CLI + HTTP `roleId`; Resolve rejects free-form harness with role |
| Strict: role required for workers; no harness override | **Done** | `roles/resolve.go` |
| Strict orch auto-bind `orchestratorRole` | **Done** | Fails closed without launch until read-only |
| Role template loader + path containment | **Done** | `roles/templates.go`, `AO_ROLE_PROFILES_DIR` |
| Single applyRoleMap; empty model overwrites project model | **Done** | Phase 1b |
| Role system prompt footer after base (recency) | **Done** | Phase 1c |
| `workspaceWrites:false` fail-closed (all harnesses) until adapter RO | **Done** | Phase 1c — spawn never claims prompt-only RO |
| Migration **0042** durable role columns + `template_artifacts` CAS | **Done** | Accepted |
| Spawn dual-write CAS; restore-by-artifact | **Done** | Fail closed missing/incomplete/conflict |
| Partial pin hydration + incomplete pin fail-closed | **Done** | Store + restore |
| Session-scoped spawn capability (random token, hash only) | **Done** | Migration **0043** |
| `canSpawn:false` / terminated / invalid cap → 403 | **Done** | |
| Operator transport (desktop header, LAN authctx, CLI rules) | **Done** | Accepted @ `93fcf1f8` |
| `AO_MANAGED_SESSION` marker (not `AO_DATA_DIR`) | **Done** | |
| Codex review packs | **Done** | `PHASE1*_CODEX_REVIEW.md`, `PHASE0042_*`, `PHASE_CANSPAWN_*` |

**Session CAS protects restore of existing sessions.** It does **not** by itself satisfy MASTER_PLAN §4.3 template-authority for **new** sessions when template bytes drift (see remaining task 1-E).

### Explicit non-claims (completed with honesty)

- **Same-UID host isolation** is **not** claimed. Agents that fully clear managed markers and read `running.json` remain residual risk.
- **Strict operational dogfood** is **not** recommended until Phase 1 exit criteria below are met.
- **Synara** was researched; **not** copied (no significant logic upgrade over SemanticHandoff plan).
- **`read_only_enforced`:** **true only for Codex** (OS sandbox). **Claude remains false** (`auto` is not deny-by-default; tool allowlists under auto overstated enforcement). Pi and others false.
- Strict RO orch/reviewer roles must use **Codex** until Claude dontAsk/OS-sandbox path lands.

---

## 2. Remaining work (ordered)

### Gate G0 — Do not enable `strictDelegation` as daily driver until Phase 1 exit

1. Executable **read-only contract** + enforcement for harnesses used with `workspaceWrites:false`
2. **Runtime capability registry** (machine-readable); config/launch/restore reject unsupported claims
3. **CLI + API** complete role-map surface (round-trip)
4. **Template-authority** contract chosen and documented
5. Full verification + strict end-to-end dogfood through the supported config surface

canSpawn is accepted; without G0, orchestrator/reviewer roles still fail closed at spawn (or would be unsafe if fail-closed were removed).

---

### Phase 1 remainder (critical path — do not declare Phase 1 complete early)

Recommended order:

```text
1-A read-only contract
  → 1-B minimum Codex/Claude RO enforcement + negative runtime tests
  → 1-C runtime capability registry (spawn + read_only only; switch/limit default false)
  → 1-D CLI/API role-map surface + round-trip tests
  → 1-E template-authority decision
  → 1-F full verification + strict dogfood
  → Phase 2A
```

Desktop role-map UI may stay parallel after 1-D CLI/API is done.

---

#### 1-A — Executable read-only contract (~1 d design, gates 1-B)

**DoD is not “flags/argv present.”** Flags alone do not prove workspace writes are denied.

The contract must specify:

| Topic | Requirement |
|-------|-------------|
| **Protected paths** | Worktree contents; `.git` / index; repository metadata under the project worktree |
| **Permitted writes** | AO state (`AO_DATA_DIR` / session storage), logs, temporary storage outside the protected worktree |
| **Negative runtime tests** | Create / edit / delete under protected paths; shell redirection into protected paths; Git mutation (`git add`/`commit`/`checkout` that changes index or worktree) — all must fail or be blocked under RO launch |
| **Enforcement timing** | On **launch** and on **restore** (defense in depth; not config-save only) |
| **Orchestrator + `ao spawn`** | How a read-only orchestrator may invoke `ao spawn` (session spawn credential / operator path) **without** receiving a general write-capable shell or unrestricted tool surface |
| **Pi** | Stays `read_only_enforced=false` unless an **external** sandbox is implemented; do not claim RO via prompts or missing flags |

Deliverable: short contract section in this file or `docs/roles/READ_ONLY_CONTRACT.md`, referenced by tests.

---

#### 1-B — Adapter read-only launch (minimum Codex + Claude) (~3–5 working days)

| Task | Detail |
|------|--------|
| Implement real enforcement | Sandbox / permission mode / tool denylist — **not** prompt-only |
| Role path | `workspaceWrites:false` → adapter explicit RO; remove blanket `ErrReadOnlyUnsupported` **only** when registry says `read_only_enforced=true` for that harness (and variant) |
| Negative tests | Per contract 1-A (FS + shell + Git); not only argv snapshot tests |
| Restore | RO still enforced after session restore |
| Spawn-from-RO orch | Tests that `ao spawn` works via credentialed path without granting write-capable shell |
| Pi / Zai / Kimi | Leave `read_only_enforced=false`; config must reject RO roles bound to them |
| Codex pack | Accept before enabling orch/reviewer in strict maps |

---

#### 1-C — Runtime capability registry (non-circular sequencing) (~1–2 working days)

**Problem fixed:** Do not dogfood or set `switch_supported` / `limit_detection_supported` to true before Phase 2/3 implement those paths.

| Phase | Registry cells |
|-------|----------------|
| **Phase 1** | Introduce machine-readable registry; populate `spawn_supported` and `read_only_enforced` from real enforcement; **default** `switch_supported` and `limit_detection_supported` to **false** |
| **Phase 2** | Promote `switch_supported` only after switch tests + dogfood |
| **Phase 3** | Promote `limit_detection_supported` only after structured-limit tests |

| Task | Detail |
|------|--------|
| Source of truth | Code registry (e.g. Go package / generated JSON consumed by Validate + launch/restore) |
| `CAPABILITY_MATRIX.md` | **Generated documentation or mirror** of the runtime registry — **not** the source validation reads |
| Dimensions | Harness; Pi **provider/model variants**; platform and/or supported binary version where enforcement differs |
| When validated | Config-save **and** launch **and** restore (defense in depth) |
| Wire | Resolve / applyRoleMap / set-config: unsupported claim → hard reject; no silent degrade |

---

#### 1-D — Role-map configuration surface (critical path, not polish) (~2–3 working days)

**Current gap:** CLI `projectConfig` mirror in `backend/internal/cli/project.go` has **no `RoleMap`**. `project set-config --config-json` **silently drops** a supplied `roleMap`. Frontend has no role-map surface.

Before strict dogfood, require at least:

| Task | Detail |
|------|--------|
| CLI read/write | Complete role map on get-config / set-config (struct fields + `--config-json`) |
| API / OpenAPI | Generated-contract parity with domain `ProjectConfig.roleMap` |
| Round-trip tests | Prove `roleMap` is preserved end-to-end (set → get → spawn resolve) |
| Operator workflow doc | How to author/apply a strict role map via CLI (desktop editor optional) |

Desktop editing UI may remain **parallel** once the CLI path is complete.

---

#### 1-E — Template approval contract (before Phase 1 complete) (~0.5–1 working day decision + implement)

MASTER_PLAN §4.3: changed template bytes must **not** silently become policy for **new** sessions. Session CAS already protects **existing** sessions; this task is about **policy selection for new sessions**.

**Choose and document one contract:**

| Option | Contract |
|--------|----------|
| **A (recommended for v1)** | V1 supports only **shipped / host-approved** profiles. Deployment or explicit `AO_ROLE_PROFILES_DIR` configuration **constitutes** approval. **Repo templates** (branch `.ao/roles/*` as live policy) remain **unsupported**. |
| **B** | Persist **approved template hashes/artifacts** in role configuration; on drift require **explicit re-approval / re-pin** before new sessions use new bytes. |

Deliverable: decision recorded here + tests matching the chosen option (e.g. Option A: load only from approved roots; reject or ignore unapproved repo paths).

---

#### 1-F — Phase 1 exit verification + strict dogfood (~2–3 working days)

Not focused tests alone:

- [ ] `go test ./...`
- [ ] API regeneration / OpenAPI drift checks
- [ ] Frontend typecheck / tests (as applicable to any 1-D API surface)
- [ ] Strict end-to-end dogfood through the **supported** config surface (CLI role map + RO orch + worker `canSpawn:false` denied)
- [ ] Codex review pack for Phase 1 exit

**Phase 1 exit criteria**

- [ ] Executable RO contract documented; negative runtime tests green for Codex and Claude at minimum
- [ ] Runtime registry: `spawn_supported` / `read_only_enforced` accurate; switch/limit default false
- [ ] Config + launch + restore reject `workspaceWrites:false` without `read_only_enforced`
- [ ] CLI/API role-map round-trip; no silent drop
- [ ] Template-authority option A or B implemented and tested
- [ ] Strict project dogfood: orch role starts read-only; workers with `canSpawn:false` cannot spawn; orchestrator can spawn via credentialed path without write-capable shell

---

### Phase 2A — Worker switch + fresh conversation (~5–8 working days)

See also `PHASE2A_PLAN.md`.

| Task | Status | Detail |
|------|--------|--------|
| SemanticHandoffV1 + ObservedWorkspaceV1 | **Done** | `domain/handoff.go` |
| Compiler | **Done** | `handoff/compile.go` — observed overrides semantic |
| Lifecycle ledger migration + store | **Done** | 0044 + `AppendLifecycleLedger` / list |
| Switch saga | **In progress** | Manager path + P1 review fixes; `switch_supported` still **false** |
| Observe workspace | **Done** | `handoff.ObserveWorkspace` |
| Generation / pending / input gate | **Done** | ForceLaunchID, SwitchPending, sessionguard |
| Probe-driven destroy + recovery | **Done** | No dual-launch; ack-only for matching live gen |
| Same-harness fresh conversation | **Done (manager)** | No handoff stacking |
| Crash re-drive from post_stop | **Done** | `RecoverSwitchFromPostStop` + `Reconcile` |
| Promote switch_supported | Open | After live Claude↔Codex dogfood |
| HTTP/CLI + service (role-map targets) | **Done** | Authorize via roleMap + failover; caps still **false** |
| Manager dogfood evidence | **Done** | `PHASE2A_DOGFOOD.md` @ 2d19ad59 |
| Live-agent dogfood + review pack | Open | Controlled local-only cap enablement |

**DoD (from MASTER_PLAN):** pre-stop leaves source usable; post-stop retains handoff; one generation owns input.

*No Synara code port — optional bootstrap-budget ideas only if needed.*

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
| Desktop dogfood | Single daemon (installed AO already removed) |
| Crash recovery | Restore + CAS + switch mid-flight |
| Multi-platform | macOS primary; Windows/Linux as needed |
| DoD checklist | MASTER_PLAN §9 all checked |

---

## 3. Schedule & verification gates

### Units and concurrency

- Estimates are **working days** (engineer-days of focused work), not calendar days.
- **Assumed concurrency:** single primary engineer on the critical path (1-A → 1-F sequential). Desktop UI polish may overlap after 1-D CLI lands.
- **No parallelization assumed** between 1-A…1-F critical path items.

### Slice table (working days)

| Slice | Est. (working d) | Depends on |
|-------|------------------|------------|
| 1-A RO contract | 1 | canSpawn (done) |
| 1-B RO adapters (Codex/Claude) + negative tests | 3–5 | 1-A |
| 1-C Runtime capability registry | 1–2 | 1-B definitions |
| 1-D CLI/API role-map surface | 2–3 | domain RoleMap (done); can parallel late 1-B after contract |
| 1-E Template-authority decision | 0.5–1 | — (can parallel 1-B/1-C) |
| 1-F Full verification + strict dogfood | 2–3 | 1-A…1-E |
| Phase 2A | 5–8 | Phase 1 exit |
| Phase 2B | 3–5 | 2A patterns |
| Phase 3A/B | 7–11 | limit detection (promote in 3A) |
| Integration | 3–5 | prior |

**Critical-path Phase 1 remainder (sequential):** ~9.5–15 working days (~2–3 calendar weeks at 1 FTE).
**Target B total remaining (serial sum of midpoints):** ~30–42 working days ≈ **6–8.5 calendar weeks** at 1 FTE with no overlap; with modest overlap (1-D/1-E under 1-B, desktop later): **~5–7 working weeks**.

Previous “3–5 weeks” undercounted config surface, template authority, full verification, and treated days without unit/concurrency assumptions.

### Sequencing sketch

```text
Now ──► 1-A RO contract
     ──► 1-B Codex/Claude RO + negative runtime tests
     ──► 1-C runtime registry (spawn + RO; switch/limit = false)
     ──► 1-D CLI/API roleMap round-trip   ⎫ may finish in parallel
     ──► 1-E template-authority A or B    ⎭ after 1-A starts
     ──► 1-F go test ./... + OpenAPI + FE + strict dogfood
     ──► 2A switch + fresh + ledger  (then promote switch_supported)
     ──► 2B orch transfer
     ──► 3A pause ──► 3B continue/failover  (then promote limit_detection)
     ──► Integration
```

---

## 4. Definition of done — remaining invariants

Already satisfied (re-verify on regressions):

1. Strict project: no roleless worker / no free-form harness with `--role`
2. `canSpawn:false` cannot successfully spawn (app-level transport)
3. Restore uses pinned template artifact (existing sessions)

Still open:

4. Pre-stop switch failure → source usable
5. Post-stop → handoff retained, target retry
6. One generation owns input at switch boundary
7. Limit → durable pause, zero auto send/restart
8. Failover preserves `role_id`, respects incident bound
9. Unsupported capabilities reject config **and** launch/restore
10. Lifecycle ledger for switch/pause/failover/fresh
11. ObservedWorkspace verified only with AO provenance
12. Failover default manual
13. Read-only roles only on `read_only_enforced` harnesses (negative runtime proof)
14. New-session template authority (shipped/host-approved **or** explicit re-pin on drift)
15. Role map fully writable via supported config surface (no silent drop)

---

## 5. Coding checklist (MASTER_PLAN §10, status)

1. [x] Fork + pin AO baseline; own migrations
2. [ ] Capability matrix as **runtime registry** + validation at config/launch/restore (doc scaffold only)
3. [x] Session-scoped spawn credential
4. [x] Role map schema (version + sha256) in domain
5. [x] Template CAS by sha256 (session restore)
6. [ ] Template **new-session** authority (Option A or B)
7. [x] Durable session role fields (**switch-history still open**)
8. [x] `ao spawn --role`; strict reject
9. [ ] CLI/API RoleMap round-trip (CLI mirror incomplete)
10. [x] RoleExecutionPolicy — canSpawn yes; workspaceWrites **fail-closed pending RO**
11. [x] Strict orch prompt builder
12. [ ] Executable RO contract + Codex/Claude enforcement + negative tests
13. [ ] SemanticHandoffV1 + ObservedWorkspaceV1 + compiler
14. [ ] Worker switch saga + fresh-conversation
15. [ ] Lifecycle ledger
16. [ ] Orchestrator switch protocol
17. [ ] Limit pause
18. [ ] Manual continue + opt-in auto-failover
19. [ ] Dogfood against all DoD invariants

---

## 6. Process notes

- **After each slice:** focused tests for the slice; at **Phase 1 exit** and later major gates: full verification (§1-F).
- **Codex review packs** (`docs/roles/PHASE*_CODEX_REVIEW.md`) + your accept before promoting gates.
- **Migrations:** never edit merged SQL; next numbers **0044+**.
- **Upstream:** keep `upstream` remote; rebase/merge carefully; avoid colliding migration IDs.
- **Dogfood:** single daemon from this fork (`~/.ao` or isolated `~/.ao/dev`); use **CLI role-map surface** once 1-D lands.

---

## 7. Immediate next action

**Phase 2A — worker switch + fresh conversation + lifecycle ledger** (see §2 Phase 2A).

Phase 1 foundation is accepted; strict end-to-end dogfood remains a separate exit gate and can proceed in parallel with early 2A design when desired.
