# Agent handoff — multi-sub roles fork (`roles/multi-sub-v1`)

**Purpose:** Everything a successor agent needs to continue the plan without re-discovering history.  
**Written:** 2026-08-04 (after Phase 2A close-out accept + `SwitchSupported` promotion).  
**Audience:** Implementation agent (Claude/Codex/other) picking up on a clean checkout.

---

## 0. TL;DR — where you are

| Item | Value |
|------|--------|
| **Repo** | https://github.com/maplenk/agent-orchestrator-roles |
| **Local tree (this machine)** | `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles` |
| **Branch** | `roles/multi-sub-v1` (tracks `origin/roles/multi-sub-v1`) |
| **HEAD at handoff** | `7545304da8689248a243cf5bed0a20ba4d0f81ff` |
| **Latest land** | `feat: promote switch_supported for Claude Code and Codex` |
| **Upstream baseline** | `Untrivial-ai/agent-orchestrator` @ pin in `docs/roles/AO_BASELINE_SHA.txt` (~`742c77bc`; verify file) |
| **Product target** | **Target B** full wishlist (~4–6 working weeks remaining for 2B+3+integration) |
| **Phase 2A** | **CLOSED and ACCEPTED** — worker switch Claude↔Codex production-capable |
| **`switch_supported`** | **true** for `claude-code` and `codex` only |
| **`limit_detection_supported`** | **false** everywhere production — do not flip |
| **`read_only_enforced`** | **true only for Codex**; Claude/Pi false by design for now |
| **Critical path next** | **Phase 2B — Orchestrator ownership transfer** |
| **Parallel optional** | Phase 1-F strict dogfood / Claude RO (1-B) — does not block 2B |

**Do not re-open Phase 2A promotion debates.** Close-out was explicitly accepted by the human; promotion landed in a dedicated CL.

---

## 1. Product / architecture in one page

### What this fork is

Fork of **Agent Orchestrator (AO)** (Electron + Go daemon + worktrees) adding Intent-grade:

1. **Host-authoritative multi-sub roles** (`ao spawn --role`, role map, template CAS)
2. **Mid-task worker switch** (Claude↔Codex saga, not transcript import)
3. Later: **orchestrator ownership transfer**, **limit pause**, **manual/opt-in failover**

Canonical design: `docs/roles/MASTER_PLAN.md`  
Living execution status: `docs/roles/REMAINING_PLAN.md` (**keep in sync on every land**)

### Locked product decisions (MASTER_PLAN §0)

- Daily product = **AO fork**, not Intent asar UI
- Host decides routing via **`ao spawn --role` only** on strict projects; free-form harness/model on ordinary path **rejected**
- Orch decides *whether* to delegate; host decides *how*
- Switch model = **durable saga** + lifecycle ledger
- Failover default **`manual`**; automatic opt-in only
- Capability matrix is **runtime** (`capabilities.For`) — markdown is mirror only
- Unsupported capabilities → **config/launch reject**, never silent degrade

### Role profiles (shipped templates)

`profiles/`:

- `orchestrator.md`
- `implementor.md`
- `ui-implementor.md`
- `reviewer.md`

Template authority **Option A**: host/shipped profiles + optional `AO_ROLE_PROFILES_DIR` override. See `docs/roles/TEMPLATE_AUTHORITY.md`.

---

## 2. Repo layout that matters

```text
docs/roles/
  MASTER_PLAN.md          # product design DoD (canonical)
  REMAINING_PLAN.md       # execution status — UPDATE ON EVERY LAND
  FORK.md                 # fork baseline notes
  AO_BASELINE_SHA.txt
  CAPABILITY_MATRIX.md    # docs mirror of capabilities.For
  READ_ONLY_CONTRACT.md
  TEMPLATE_AUTHORITY.md
  PHASE2A_PLAN.md
  PHASE2A_DOGFOOD.md      # manager-level evidence @ 2d19ad59
  PHASE2A_LIVE_DOGFOOD.md # live Claude/Codex + crash + lease close-out
  PHASE1*_CODEX_REVIEW.md # historical review packs
  AGENT_HANDOFF.md        # this file

profiles/*.md

backend/internal/
  roles/
    capabilities/         # SOURCE OF TRUTH for harness caps
    resolve.go, templates.go, readonly/
  domain/
    rolemap.go, handoff.go, lifecycle_ledger.go, session.go (SwitchPending), switch_targets.go
  session_manager/
    switch.go             # SwitchWorker / Fresh / RecoverSwitchFromPostStop
    dogfood_phase2a_test.go
    manager.go            # ForceLaunchID, ownershipMu, etc.
  service/session/
    switch.go             # role-map auth + service facade
  httpd/controllers/
    sessions.go           # POST …/switch, …/fresh-conversation
    switch_auth_test.go
    dto.go
  cli/session.go          # ao session switch | fresh
  datadirlock/            # exclusive AO_DATA_DIR lease (Unix flock / Win LockFileEx)
  daemon/daemon.go        # Acquire lease BEFORE sqlite open / reconcile
  storage/sqlite/migrations/
    0042_session_role_fields.sql
    0043_session_spawn_capability_hash.sql
    0044_lifecycle_ledger.sql
    0045_session_switch_pending.sql
    # next free: 0046+
```

Go module path remains `github.com/aoagents/agent-orchestrator/...` (fork does not re-module for product name).

---

## 3. Current capability matrix (runtime)

Source: `backend/internal/roles/capabilities/capabilities.go`

| Harness | spawn | switch | limit | RO enforced | Notes |
|---------|-------|--------|-------|-------------|--------|
| claude-code | true | **true** | false | **false** | RO deferred (dontAsk/OS sandbox) |
| codex | true | **true** | false | **true** | `--sandbox read-only` |
| pi | true | false | false | false | |
| other AllHarnesses | true | false | false | false | |
| fake (tests) | true | true | false | true | test only |

### Promotion rules (process)

1. Never flip `switch_supported` or `limit_detection_supported` in the same CL as large feature work when avoidable — **separate final promote CL**.
2. Before promote: dogfood + review accept of gates.
3. After Claude/Codex switch promote, `switchSupportedPromoted()` is **true** → `ValidateRoleMap` enforces `switch_supported` on **failover rungs**.
4. Failover to Pi (or any non-switch harness) now **rejects at config-save**.
5. Do **not** promote `limit_detection_supported` until Phase 3 structured-limit evidence.

---

## 4. What is fully done (accepted)

### Phase 0–1 foundation (accepted; Phase 1 *strict exit* still open)

- RoleMap / RoleBinding / RoleExecutionPolicy / FailoverConfig in domain
- ProjectConfig.roleMap validate + CLI/API round-trip
- Host-authoritative `ao spawn --role`; strict reject roleless workers / free-form harness with role
- Template loader + path containment; CAS template artifacts (0042)
- Session-scoped spawn capability token (0043); canSpawn false → 403
- Operator transport: desktop headers, LAN authctx, CLI rules; `AO_MANAGED_SESSION` (not bare `AO_DATA_DIR` spoof)
- Codex `read_only_enforced`; Claude intentionally false
- Capability registry for spawn/RO (switch later promoted)

### Phase 2A worker switch — complete

| Layer | Status | Key refs |
|-------|--------|----------|
| Manager saga | Done | `session_manager/switch.go` |
| SemanticHandoffV1 + ObservedWorkspaceV1 + compiler | Done | `domain/handoff.go`, `handoff/compile.go` |
| Lifecycle ledger 0044 | Done | phases: requested → pre_stop → post_stop → target_ack (+ failed) |
| SwitchPending 0045 | Done | promote durable harness only after `target_ack` |
| Generation ownership | Done | ledger gen == `RuntimeLaunchID` via `ForceLaunchID` |
| Input fences | Done | sessionguard + terminal `InputGate`; handle-indexed lookups |
| Recover incomplete post_stop | Done | boot reconcile path |
| Service/API/CLI | Accepted @ `83f7abfb` | exact model auth; role-map targets |
| Switch auth | Accepted | operator/LAN global; session principals: role pin + canSpawn + **same project** |
| Target-authoritative prompt footer | Done @ `a3bc32be` | ephemeral role copy for prompt only until ack |
| Manager dogfood | Done @ `2d19ad59` | `PHASE2A_DOGFOOD.md` |
| Live dogfood | Accepted | `PHASE2A_LIVE_DOGFOOD.md` |
| Concurrent daemon ownership | Done @ `9480bdc7` | `datadirlock` before store/reconcile |
| **Production SwitchSupported** | **Done @ `7545304d`** | Claude + Codex |

### Key commit chain (recent → older, branch tip first)

```text
7545304d feat: promote switch_supported for Claude Code and Codex
9480bdc7 fix: exclusive data-dir lease before daemon reconcile
ef9952a3 docs: sync REMAINING_PLAN with Phase 2A landed status
9d30c627 docs: live dogfood after target-prompt fix; note residual failed ledger
a3bc32be fix: build switch target system prompt with ephemeral role identity
b5fe6b15 docs: Phase 2A live dogfood evidence at 83f7abfb (caps still false)
83f7abfb fix: scope session switch principals to role pin, canSpawn, and project
960122e6 fix: authenticate switch/fresh, exact model auth, schema + tests
49341ad8 feat: service/API/CLI worker switch with role-map authorized targets
ae08850c docs+test: Phase 2A dogfood evidence at 2d19ad59
2d19ad59 fix: dogfood path — indexed terminal gate, uncertain rollback, adversarial tests
e8634e92 fix: pre-stop usability, handle-based terminal gate, post_stop ordering
d511dfa2 fix: durable switch fence before destroy and complete input gates
7fdbf1aa fix: Phase 2A switch ownership, generation, and recovery P1s
c4e9d622 feat: recover incomplete post_stop worker switches on boot
```

---

## 5. Hard lessons from Phase 2A reviews (do not regress)

These were real P1s that landed fixes. Treat as permanent design constraints.

### 5.1 Generation & durable promote

- **Single** `ForceLaunchID` through supervise/relaunch; ledger generation **must equal** `RuntimeLaunchID`.
- Do **not** promote durable session harness/model before durable **`target_ack`**.
- Pending: `switch_pending_json` (0045) holds target + payload until ack.
- Stable ledger IDs: `{sessionID}:{generationID}:{phase}` with skip-if-exists.

### 5.2 Pre-stop vs post-stop

- **Pre-stop failure:** source must remain usable (rollback pending + restore metadata).
- **Confirmed-alive after Destroy attempt:** treat as pre-stop rollback, not “destroyed.”
- **Rollback persist fail** → `ErrSwitchUncertain` (pending retained).
- **Post-stop failure:** handoff retained; target retry / recover; **do not** ack.
- **post_stop append fail** must **block** launch and ack (`ErrSwitchPostStop`).

### 5.3 Recovery

- Live wrong-gen process → `ErrSwitchUncertain` (no second target launch).
- Matching live gen → ack only (no double launch).
- Recovery must `ensurePostStopLedger` before launch/ack.
- Recover path must still enforce RO on target harness.

### 5.4 Input ownership fence

- At most **one generation** owns input at switch boundary.
- Pending fences: session id, live `RuntimeHandleID`, **and** pending `SourceRuntimeHandleID`.
- Terminal mux Write suppressed via `InputGate`.
- Host inject uses `DeliverHost` path that is allowed for host only.
- Send success must not lie when suppressed (`SuppressedSwitchPending` → `ErrSwitchInProgress`).

### 5.5 Target system prompt (authoritative footer)

**Bug that bit live dogfood:** target launch used **source** harness in AUTHORITATIVE ROLE FOOTER.

**Fix (`a3bc32be`):** `relaunchSession` builds **ephemeral** role copy with `ResolvedHarness` / `ResolvedModel` = pending target for system prompt + agent config only. Durable identity stays source until ack.

Tests: `TestSwitchWorker_SystemPromptTargetHarnessFooter*` and recover variant.

### 5.6 Switch auth & model authorization (Service/API)

P1s fixed before live dogfood:

- No headerless switch (must authenticate)
- Exact `(harness, model)` authorization via `ResolveAuthorizedSwitchModel`
- Empty model is not a wildcard; ambiguous omitted model → `TARGET_MODEL_REQUIRED`
- Roleless / canSpawn false / **cross-project** session principals rejected
- Failover config-save validates spawn + inherited RO; after promote also **switch_supported**
- FE OpenAPI/schema must stay in sync (`npm run api` / schema regen when API changes)

### 5.7 Concurrent daemon ownership (the “failed → target_ack in ~5ms” bug)

**Symptom:** crash inject showed ledger `failed` then `target_ack` milliseconds later; no recovery-failed log.

**Root cause:** two daemons could pass runfile checks; one bound configured port, other bound **ephemeral** port fallback (`httpd/server.go`), **both reconciled the same SQLite**. One launch collides (`failed`); other succeeds (`target_ack`).

**Fix (`9480bdc7`):** package `datadirlock`

- Exclusive lease on `{AO_DATA_DIR}/daemon.lock`
- Unix: flock; Windows: `LockFileEx`
- Acquired in `daemon.Run` **before** `sqlite.Open` / reconcile
- Second process exits `ErrLocked` / “data directory already owned”
- Ephemeral port fallback remains **only for the lease holder** when a non-AO process owns the port

**Re-dogfood after lease:** gen `lease-crash-gen-1` → clean  
`requested → pre_stop → post_stop → target_ack` with **no failed**.

### 5.8 Dogfood discipline

- Prefer **isolated** `AO_DATA_DIR` for live experiments
- Pre-promotion local-only enablement was non-committable patch + env; **no longer needed** for Claude/Codex after `7545304d`
- Record SHA, patch hash if patching, SQLite ledger inspection, process executable + launch id, footer text
- Manager checklist: `TestDogfood_Phase2AChecklist` (now runs on production caps without override)

---

## 6. Worker switch API surface (production)

### HTTP

```text
POST /api/v1/sessions/{sessionId}/switch
POST /api/v1/sessions/{sessionId}/fresh-conversation
```

Auth summary:

| Principal | Access |
|-----------|--------|
| Operator (desktop headers) | Global |
| LAN authctx | Global |
| Session principal | Must be role-pinned, `canSpawn`, **same project** as target session |

Error codes (service mapping):  
`SWITCH_NOT_SUPPORTED`, `SWITCH_IN_PROGRESS`, `SWITCH_POST_STOP`, `SWITCH_UNCERTAIN`, `SWITCH_AUTH_REQUIRED`, `SWITCH_TARGET_UNAUTHORIZED`, `TARGET_MODEL_REQUIRED`, …

### CLI

```bash
ao session switch --session <id> --harness <target> [--model …] [--objective …]
ao session fresh  --session <id> [--objective …]
```

Uses `spawnCallerHeaders()` (managed-session no-upgrade).

### Manager API

```go
SwitchWorker / FreshConversation / RecoverSwitchFromPostStop
```

Caps via `capabilities.For` (optional test `switchCapsOverride`).

### Saga phases (ledger)

```text
requested → pre_stop → post_stop → target_ack
                 \→ failed (terminal context)
```

Kinds include switch, fresh_conversation (pause/failover later).

---

## 7. Explicit non-claims (honesty board)

Do not claim these in docs, PRs, or dogfood:

1. **Same-UID host isolation** beyond `datadirlock` (not a multi-tenant security boundary)
2. **Phase 1 full strict operational dogfood** (1-F open)
3. **Claude RO** (`read_only_enforced=false`)
4. **Pi switch** or other harness switch
5. **Limit detection / durable pause / auto-failover runtime**
6. **Orchestrator ownership transfer** (Phase 2B not started)
7. Full `go test ./...` green as a formal gate (prefer focused packages; expand when doing 1-F)

---

## 8. Remaining work (ordered)

### 8.1 Critical path: Phase 2B — Orchestrator ownership transfer (~3–5 working days)

MASTER_PLAN §5.4 / REMAINING_PLAN §2:

| Task | Intent |
|------|--------|
| Coordinator lease | Who owns orch routing/coordination |
| Nudge/routing rebind | Redirect host routing after orch switch |
| Pending message transfer | In-flight user/system messages move to successor |
| Generation fencing + target ack | Same generation/input ownership patterns as 2A |
| Recovery | Source dead / successor fails |

**Design note for implementor:** Reuse 2A patterns (pending, ledger phases, ForceLaunchID, input gates, datadirlock already protects store). Orch switch is a **separate protocol** from worker switch — do not over-merge without reading MASTER_PLAN and existing orch session paths.

**DoD checklist item 16** in REMAINING_PLAN is still open: “Orchestrator switch protocol.”

Suggested first steps for 2B:

1. Read MASTER_PLAN §5.4 + search codebase for orchestrator session kind, nudge, routing, coordinator
2. Sketch state machine vs worker switch (document in `docs/roles/PHASE2B_PLAN.md` before large code)
3. Prefer small lands: domain pending/ledger kinds → manager → service/API → dogfood
4. Keep `limit_detection` false; do not re-touch Claude/Codex switch cells unless bugfix
5. Update `REMAINING_PLAN.md` on each land

### 8.2 Parallel: Phase 1 remainder (not blocking 2B)

| Slice | Status | Notes |
|-------|--------|--------|
| 1-A RO contract | Partial | `READ_ONLY_CONTRACT.md` exists |
| 1-B Claude RO | **Open** | Needs dontAsk + no write Bash + non-shell spawn **or** OS sandbox; negative runtime test |
| 1-C registry | Done for current cells | |
| 1-D role-map surface | Done | |
| 1-E template authority | Done Option A | |
| 1-F full verification + strict dogfood | **Open** | Full test suite, strict orch RO as daily driver |

### 8.3 Phase 3A — Limits + durable pause (~4–6 d)

- Structured/reviewed limit envelopes only (never free-text “I hit a limit”)
- Durable pause; **zero** automatic send/restart
- Ledger pause/resume
- Promote `limit_detection_supported` **only after** structured-limit tests

### 8.4 Phase 3B — Manual continue + opt-in failover (~3–5 d)

- Manual continue default; advance failover ladder
- Preserve `role_id`; only harness/model change
- `maxFailoversPerIncident` then stay paused
- `failover.mode=automatic` opt-in + matrix-gated

### 8.5 Integration (~3–5 d)

- Desktop Electron dogfood with isolated/explicit data dir
- Crash recovery under product load
- Multi-platform as needed
- MASTER_PLAN §9 DoD all checked

---

## 9. Definition-of-done invariants

### Already satisfied (re-verify on regressions)

1. Strict: no roleless worker / no free-form harness with `--role`
2. canSpawn false cannot spawn
3. Restore uses pinned template artifact
4. Pre-stop switch failure → source usable
5. Post-stop → handoff retained, target retry
6. One generation owns input at switch boundary
9. Unsupported caps reject config/launch (spawn/RO/switch for promoted cells)
10. Lifecycle ledger for switch/fresh
11. ObservedWorkspace verified only with AO provenance
12. Failover default manual
14. Template Option A
15. Role map CLI/API no silent drop
16. Crash ledger free of unexplained failed (datadirlock)
17. Claude/Codex `switch_supported` true after accepted close-out

### Still open

7. Limit → durable pause, zero auto send/restart  
8. Failover preserves role_id + incident bound (runtime)  
13. Claude RO negative proof for read-only roles  

---

## 10. Engineering process conventions (this project)

1. **Branch:** work on `roles/multi-sub-v1`; push to `origin` after accepted lands (human often expects push).
2. **Commits:** conventional-ish prefixes used historically: `feat:`, `fix:`, `docs:`. Prefer **small, reviewable** CLs over mega-commits.
3. **Docs sync:** update `REMAINING_PLAN.md` (snapshot, tables, next action, checklist) on every phase/slice land.
4. **Migrations:** never edit merged SQL; next numbers **0046+**. Avoid colliding with upstream migration IDs (`upstream` remote).
5. **Capability promote:** separate CL; dogfood first.
6. **Review severity:** human/Codex P1s block; P2 often block; P3 doc cleanup can ride with next related land (as with promotion).
7. **Tests before claim:** run focused packages, not only compile:

```bash
cd backend
go test ./internal/roles/capabilities/ ./internal/session_manager/ \
  ./internal/datadirlock/ ./internal/domain/ ./internal/service/session/ -count=1
go test -race ./internal/datadirlock/ ./internal/roles/capabilities/ -count=1
# dogfood checklist
go test ./internal/session_manager/ -run 'TestDogfood_Phase2AChecklist' -count=1 -v
# Windows cross-compile when touching lock/daemon
GOOS=windows GOARCH=amd64 go build -o /dev/null ./internal/datadirlock/ ./internal/daemon/
```

8. **API/schema:** if OpenAPI changes, regenerate FE schema (project has `npm run api` / schema scripts — follow existing package scripts).
9. **Live dogfood:** isolated `AO_DATA_DIR`; kill leftover daemons; never dual-start same data dir (lease should prevent mutation races).
10. **Honesty:** if something is partial, document non-claims; do not mark Phase complete without human accept on dogfood gates when capability promote is involved.

---

## 11. How to re-run known green checks

### Unit / manager

```bash
cd /Users/tagtaste/Documents/QBApps/agent-orchestrator-roles/backend
go test ./internal/roles/capabilities/ ./internal/session_manager/ \
  ./internal/datadirlock/ ./internal/domain/ ./internal/service/session/ -count=1
```

### Live switch (post-promotion — no cap patch)

High-level protocol used successfully:

1. Isolated data dir + free port
2. Start daemon (single process; lease held)
3. Project with roleMap: implementor Claude primary, Codex on failover ladder (or authorized pair)
4. Spawn implementor worker (Claude)
5. `POST …/switch` with `targetHarness=codex` (operator headers)
6. Assert: process is codex binary, launch id = generation, footer `Harness: codex.`, pending cleared, ledger order clean

### Clean crash recovery protocol (validated)

1. Live source → kill agent (tmux/process) → **confirm DEAD**
2. SIGKILL daemon → offline
3. Inject pending + ledger `requested/pre_stop/post_stop` only (no failed/ack) while offline
4. **Single** daemon start → one recover
5. Expect: target harness, same generation, pending 0, `target_ack`, **no failed**

---

## 12. Important file cheat sheet

| Concern | Path |
|---------|------|
| Cap registry | `backend/internal/roles/capabilities/capabilities.go` |
| Cap tests | `…/capabilities_test.go` |
| Worker switch saga | `backend/internal/session_manager/switch.go` |
| Switch unit tests | `…/switch_test.go` |
| Dogfood harness | `…/dogfood_phase2a_test.go` |
| Service switch auth | `backend/internal/service/session/switch.go` |
| Error mapping | `backend/internal/service/session/service.go` |
| HTTP routes | `backend/internal/httpd/controllers/sessions.go` |
| Switch auth tests | `…/switch_auth_test.go` |
| CLI | `backend/internal/cli/session.go` |
| Data dir lease | `backend/internal/datadirlock/*` |
| Lease wiring | `backend/internal/daemon/daemon.go` (~Acquire before open) |
| Switch pending encode | `backend/internal/storage/sqlite/store/session_store.go` |
| Role map domain | `backend/internal/domain/rolemap.go` |
| Switch targets | `backend/internal/domain/switch_targets.go` |
| Handoff types | `backend/internal/domain/handoff.go` |
| Lifecycle phases | `backend/internal/domain/lifecycle_ledger.go` |
| Migrations | `backend/internal/storage/sqlite/migrations/0042–0045_*.sql` |

---

## 13. Environment / machine notes (last known)

- OS: macOS (human machine)
- Real Claude + Codex binaries available for live dogfood (Codex path seen: `/Users/tagtaste/.local/bin/codex`)
- Workspace for this handoff session may be Intent app path; **code lives under** `/Users/tagtaste/Documents/QBApps/agent-orchestrator-roles`
- Branch clean and pushed at `7545304d` when this was written

---

## 14. Immediate next action for the successor agent

1. `git fetch && git checkout roles/multi-sub-v1 && git pull` — confirm HEAD ≥ `7545304d`
2. Read (in order):
   - this file
   - `REMAINING_PLAN.md` (snapshot + §2 remaining)
   - `MASTER_PLAN.md` §5.4 (orch switch) + §8–10
   - skim `PHASE2A_LIVE_DOGFOOD.md` (lessons 5.5–5.7)
3. **Start Phase 2B** unless human prioritizes Claude RO / 1-F:
   - Explore orch session, routing, nudge, coordinator code paths
   - Draft `PHASE2B_PLAN.md` with state machine + reuse of 2A primitives
   - Implement in thin vertical slices with tests
4. On each land: update `REMAINING_PLAN.md`, run focused tests, prefer push after human accept of risky slices
5. **Never** promote `limit_detection_supported` without Phase 3 evidence
6. **Do not** reopen 2A switch promotion without a production regression

---

## 15. Suggested first message to the human

Confirm priority:

- **A (default):** Phase 2B orchestrator ownership transfer  
- **B:** Phase 1-B Claude RO  
- **C:** Phase 1-F full suite + strict dogfood  

Then proceed without re-litigating 2A.

---

## 16. Appendix — error / conflict semantics (worker switch)

| Condition | Error / code |
|-----------|----------------|
| Cap missing | `ErrSwitchNotSupported` → `SWITCH_NOT_SUPPORTED` |
| Switch already pending / in progress | `ErrSwitchInProgress` → `SWITCH_IN_PROGRESS` |
| Source stopped, target incomplete | `ErrSwitchPostStop` → `SWITCH_POST_STOP` |
| Runtime ambiguous (wrong gen live, rollback disk fail, etc.) | `ErrSwitchUncertain` → `SWITCH_UNCERTAIN` |
| Not a worker | `ErrNotWorker` → `NOT_A_WORKER` |
| Nothing to recover | `ErrSwitchNothingToRecover` |
| Unauthorized target | service → `SWITCH_TARGET_UNAUTHORIZED` |
| Auth missing | `SWITCH_AUTH_REQUIRED` |
| Ambiguous model | `TARGET_MODEL_REQUIRED` |

---

## 17. Appendix — what “accepted” meant for 2A close-out

Human acceptance text (paraphrased conditions, all met):

- Lease acquired **before** store access/reconcile; held for daemon lifetime  
- Unix locking + Windows `LockFileEx` sound  
- Concurrent-process regression: one mutation owner  
- SQLite confirms `requested → pre_stop → post_stop → target_ack`, no `failed`  
- Final session Codex, generation matches, pending cleared  
- (Pre-promote) production capability cells remained false until dedicated promote CL  
- P3 only: clean stale “still open” docs language — done with promote in `7545304d`

---

*End of handoff. Prefer updating this file or `REMAINING_PLAN.md` rather than inventing parallel status docs.*
