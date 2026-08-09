# Master plan: Multi-sub harness orchestration on AO

**Status:** canonical Target B design; implementation is active and tracked in
[`REMAINING_PLAN.md`](REMAINING_PLAN.md). Desktop role-map editing is complete;
the dormant host engine for opt-in automatic failover has landed, but automatic
failover is still only partial and is not a product-active capability. The
current MVP boundary and integration acceptance are tracked in
[`MVP_FINAL_SPEC.md`](MVP_FINAL_SPEC.md).
If this long-range design conflicts with the final MVP boundary or current
execution state, `MVP_FINAL_SPEC.md` and then `REMAINING_PLAN.md` control.
**Base:** fork **Agent Orchestrator (AO)** — Electron UI + Go daemon + worktrees
**Not base:** Intent asar; harness-orchestration as daily UI
**Sources:** Intent RE, AO code/PRs, harness-orchestration PLAN, deep-research-report, Codex plan reviews

**Execution checkpoint (2026-08-08):** Phase 1 foundation and Phase 2A are
accepted. Phase 2B-0/1/2 landed and the final-MVP 2B-3 in-place Codex↔Claude
orchestrator switch is implemented under the amended strict policy. Phase 3A's durable pause, operator surface, desktop pause/role
composer and detector boundary landed, but no vendor detector is promoted.
Sync 2 is closed and merged; its accepted evidence head was
`roles/upstream-sync-2` @ `dd06d31a`. All eight steps are done, every required
CI job is green, and the two live Step 5 dogfood records
(orchestrator fresh conversation, and a genuine post-stop recovery on the
original generation) are captured in `UPSTREAM_SYNC2_DOGFOOD_STEP5.md`. Phase
3B manual worker Continue and the final-MVP 2B-3 core, service/read model, API,
CLI and desktop surface are complete at immutable code/runner SHA `166e9e63`.
Independent review is closed and the complete worker/orchestrator live matrix
is promoted by evidence commit `322f9c18`. The Chat rollback failure was
classified as test-only asynchronous projection and fixed at integration head
`f8883529`; SQLite exact checks passed 5/5, its race package passed in 622.846s,
and race validation found zero data races. Target B adapter test hygiene at
`66d65e4e` replaces the known fake/Kilocode/OpenCode real-time assertions with
structural or pure-classifier coverage while preserving production deadlines
and behavior. Focused normal/race checks each pass 105/105 and the ordinary
full backend run passes 4,724 tests across 132 packages. Target B vendor-fixture
research at `11d12414` then captures two sanitized real Claude Code 2.1.224
structured 429 refusal projections and two real Codex 0.146/0.147 quota-state
frames in adapter-local testdata. A subsequent research-only, byte-complete
public Claude Code 2.1.159 `rate_limit_event` fixture proves that the structured
`rateLimitType` + `resetsAt` pair can supply a stable reset-cycle identity for a
rejected limit. That print-stream-json surface is not AO's accepted interactive
Claude surface: no interactive ingress carries the event together with its AO
runtime generation, and no production Router caller or detector composition was
added. Codex still proves structured state but not a reached/refused event.
`limit_detection_supported` remains false everywhere, and the `11d12414`
fixture-era ordinary full backend run passed 4,737 tests across 132 packages. On exact integration head
`f8883529`, gofmt, vet, cold-cache golangci-lint v2.12.2, and typecheck pass;
before the test fix, full Vitest was 2039/2040. Its sole `SessionFilesView`
failure is deterministic (0/20 exact-test passes, full file 27/28) and
reproduces on pre-MVP baseline `be4321d1` with the relevant files unchanged, so
it is not an MVP regression. Its test fix `56638949`, integrated as `6473b134`,
passes 20/20 exact and 28/28 full-file runs while retaining the negative
timeout. API drift passes two identical regenerations with a clean diff. The
authoritative unsandboxed full Vitest run on exact `6473b134` passes 151/151
files and 2040/2040 tests in 312.49s.
The installed-app compatibility close-out at `762ae160` persists a non-strict
starter role catalog for new and existing unconfigured projects. It preserves
legacy worker behavior while making desktop-created orchestrators role-pinned
and allowing an exact provider-default legacy orchestrator to adopt the role at
an explicit Switch boundary; the real app completed Claude→Codex→Claude on the
existing `qbapi` session.
Target B durability hardening A is complete at `8bb1e3af`: unreadable or
forward-versioned stored project config is contained per project, remains
byte-preserved and visible as degraded, and is mutation-fenced without taking
healthy projects offline. Focused normal/race gates and native Electron
dogfood are recorded in `TARGET_B_DURABILITY_HARDENING_20260809.md`. Hardening
B is complete at `586d1156`: a pre-write block classifier preserves a genuine
lone Muse 53, retains complete stale 53–60 cleanup, refuses ambiguous histories
loudly, and proves transactional rollback. Neither slice added a migration or
promoted a capability cell. The fake/Kilocode/OpenCode wall-clock slice is
complete at `66d65e4e`, and non-promoting vendor fixture research is complete
at `11d12414`. The desktop role-map editor is complete across `54cdbdd8` and
`c7c1f565`, with its review and acceptance record in
[`TARGET_B_ROLE_MAP_EDITOR_20260809.md`](TARGET_B_ROLE_MAP_EDITOR_20260809.md).
It uses a role-map-only compare-and-swap mutation, sends the loaded role-map
SHA with full-config mutations, preserves deterministic ordered failover
targets, and rejects duplicate current or earlier effective targets. It added
no migration and promoted no capability cell. The dormant automatic-failover
host engine then landed at source commit `1c97c55e`, content-equivalent roles-
trunk commit `29becc6d`: it reuses the accepted Continue saga for
one capability-gated action, converges pause-before-attempt and
requested/post-stop/acked crash windows on the same durable generation, keeps
manual as the default, rejects unpromoted automatic configuration, and gives
legacy desktop maps an explicit conversion to manual. Promotion-blocker
correction `72bca3e4` now persists the detector-observed runtime generation in
the internal structured pause and requires an exact current-generation match
before the first automatic attempt. Legacy unbound pins remain manual-only,
while requested/post-stop/acked recovery remains governed by durable attempt
identity. The correction passes 4,835/4,835 full backend normal and race,
2,060/2,060 full frontend, typecheck, build, vet, format, arm64 Forge package,
and pinned lint with zero issues. Its isolated native Electron restart/boot
regression preserves generation B and generation A's pause with zero automatic
attempts, failover ledger rows, or runtime replacement. See
[`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md).
This slice added no migration or public API, changed no host prompt contract,
and promoted no capability cell; `limit_detection_supported` remains false for
every harness, Claude `read_only_enforced` remains false, and **9009+** remains
the next fork migration.

**Next required action:** establish a reviewed AO interactive Claude ingress
that delivers the structured limit window together with the exact observing
runtime generation, compose its production Router caller, then land capability
promotion in a separate final change only after review and positive live
acceptance. Until those gates close, the host engine remains dormant, every
production `limit_detection_supported` cell remains false, and checklist item
22 remains partial. Claude technical read-only,
successor-orchestrator/live-worker rebinding, and Pi/Muse switching remain
clearly optional later work; none gates the required Target B detector close-out.
The estimates below are original planning estimates, not a claim
about remaining duration.

---

## 0. Locked product decisions

| Decision | Choice |
|----------|--------|
| Daily product | Fork **AO** |
| v1 outcomes | Multi-sub **role routing** + mid-task **switch** (workers **and** orchestrator) |
| Orch decides *whether* to delegate | No host `auto_on_task` in v1 |
| Host decides *how* routing works | **`ao spawn --role` only** on strict projects; legacy spawn **rejected** |
| Zai/Kimi/cheap | **Pi** (gated by capability matrix) |
| Switch model | Durable saga (not transcript import) |
| Timeline | **Target B: ~4–6 weeks** full wishlist |
| Auto failover | **Opt-in**; default **manual** even in Target B |

---

## 1. Problems → solutions

| # | Problem | Solution |
|---|---------|----------|
| **P1** | Long-session memory | Switch / **same-harness fresh conversation** with compiled handoff; required lifecycle ledger (switch/pause/failover only); no full-turn ledger in v1 |
| **P2** | Mid-task provider switch | #3548-class worker saga; separate orch ownership protocol; SemanticHandoff + ObservedWorkspace |
| **P3** | Limit burn | Structured detect → durable pause → zero auto send/restart → manual continue → optional bounded auto-failover |
| **P4** | Multi-sub roles | Templates + daemon role map + durable role identity + `spawn --role` |
| **P5** | Intent feel | Strict templates + single strict prompt builder |

---

## 2. Host-authoritative spawn (Phase 1 — no bypass)

### 2.1 Supported contract (orchestrator ordinary path)

```bash
ao spawn --project <id> --role <role_id> --name "<≤20>" --prompt "…"
ao spawn --project <id> --role <role_id> --name "<≤20>" --prompt-file <path>
```

Daemon:

1. Authenticate **calling session** (session-scoped credential; not spoofable bare `AO_SESSION_ID` alone).
2. Enforce caller `canSpawn` (orchestrator true; workers false).
3. Require `role_id` when `strictDelegation` on project.
4. Resolve role → template artifact + harness + model + permissions.
5. **Forbid** harness/model overrides on the ordinary spawn path.
6. Render system prompt from **pinned template artifact**.
7. Persist durable role fields.
8. Launch harness.

### 2.2 Strict project reject rules (Phase 1 — required)

```text
if project.strictDelegation && kind == worker:
  REQUIRE role_id
  REJECT legacy ao spawn --agent / --harness without --role
  REJECT any harness/model override flags on ordinary path
```

Non-strict projects may keep legacy `ao spawn --agent` for compatibility.

**Privileged human override** (if ever needed): separate path (e.g. `ao spawn --privileged-override` + explicit human confirmation / non-orch API), **never** the orchestrator’s ordinary command surface.

### 2.3 Why this preserves “strict prompts only”

Model still decides **to** spawn and types `--role ui`.
Model **cannot** invent Codex vs Pi routing or free-form models.

---

## 3. RoleExecutionPolicy (Phase 1)

Booleans are **not** documentation — they bind host checks.

```go
type RoleExecutionPolicy struct {
    WorkspaceWrites bool // false => harness must enforce read-only / no write tools
    CanSpawn        bool // false => daemon rejects spawn API for this session
}
```

| Policy | Enforcement |
|--------|-------------|
| `canSpawn: false` | Daemon identifies **caller session** via **session-scoped credential** (token/HMAC issued at spawn, not spoofable env alone). Spawn from that session → hard error. |
| `workspaceWrites: false` | Require adapter capability `enforces_read_only` (sandbox / tool denylist / reviewer subsystem). **Reject role binding** at config time if harness cannot enforce. |
| `workspaceWrites: true` | Normal implementor path. |

**Pi:** not eligible for read-only **reviewer** (or any `workspaceWrites: false` role) until Phase 0 marks `read_only_enforced`. Configuration **rejects** rather than silently degrading.

**Orchestrator strict:** `canSpawn: true`; `workspaceWrites` is explicit policy,
not implied by strictness. The default MVP role is writable so Codex and Claude
Code can switch in place, while the single no-edit/delegate rule remains
instruction-enforced. Configuring `workspaceWrites:false` still requires a
read-only-enforced harness.

**Reviewer:** prefer AO reviewer subsystem; else `workspaceWrites: false` + enforceable harness only.

Phase 1 includes **capability validation** when saving role map:

```text
for each role:
  require harness.spawn_supported
  if !workspaceWrites: require harness.read_only_enforced
  if role has a non-empty failover ladder:
    require harness.switch_supported          # primary is the switch source
    require rung.harness.switch_supported     # every rung is a switch target
```

---

## 4. Role templates + config identity

### 4.1 Content locations

```text
profiles/*.md              # shipped defaults (fork)
# Repo templates are privileged policy, NOT ordinary branch content:
# Load only from approved base revision (e.g. default branch tip at pin time)
# or after explicit role-config approval when template hash changes.
# NEVER load a worker-branch .ao/roles/*.md as system authority.
```

### 4.2 Frontmatter = initialization hints only

Daemon/JSON bindings always win. Model field: `null` / empty = provider default; **never** literal `"default"`.

### 4.3 Template artifact (restore-safe)

A **hash alone is insufficient**. Persist:

| Field | Meaning |
|-------|---------|
| `template_sha256` | Content hash |
| `template_artifact_id` | Immutable CAS / DB blob of **exact template bytes** or **compiled system prompt** at session start |
| Restore | Load artifact by id; **ignore** later file changes |

On hash drift of repo templates: detect; **do not** auto-apply to live sessions; require explicit re-approval to re-pin new config revision.

### 4.4 Schema version vs content revision (distinct)

```text
role_map_schema_version     # e.g. 1 — wire format
role_map_sha256             # hash of full role map document
role_config_revision        # monotonic content revision (optional integer)
template_artifact_id
template_sha256
role_id
resolved_harness            # CURRENT effective
resolved_model              # CURRENT effective
resolved_permissions
```

On switch: write **immutable switch-history record** with prior harness/model + new harness/model + generation ids. Current session fields update to new resolved values only after target ack.

### 4.5 Role map schema (failover deterministic)

```json
{
  "role_map_schema_version": 1,
  "strictDelegation": true,
  "orchestratorRole": "orchestrator",
  "roles": {
    "orchestrator": {
      "template": "orchestrator",
      "harness": "claude-code",
      "model": null,
      "permissions": { "workspaceWrites": true, "canSpawn": true },
      "_comment": "Strict coordination is instruction-enforced. Set workspaceWrites=false only when technical write denial is required."
    },
    "implementor": {
      "template": "implementor",
      "harness": "claude-code",
      "model": "…",
      "permissions": { "workspaceWrites": true, "canSpawn": false },
      "when": ["implementation", "bugfix", "tests"]
    },
    "ui": {
      "template": "ui-implementor",
      "harness": "pi",
      "model": "…",
      "permissions": { "workspaceWrites": true, "canSpawn": false },
      "when": ["frontend", "ui"]
    },
    "reviewer": {
      "template": "reviewer",
      "harness": "codex",
      "model": null,
      "permissions": { "workspaceWrites": false, "canSpawn": false },
      "when": ["review"]
    }
  },
  "failover": {
    "mode": "manual",
    "roles": {
      "orchestrator": [
        { "harness": "codex", "model": null }
      ],
      "implementor": [
        { "harness": "codex", "model": null }
      ]
    }
  },
  "limits": {
    "onUsageLimit": "pause",
    "maxFailoversPerIncident": 2
  }
}
```

**Validity note (post Phase 2A promotion).** The ladder above is constrained by the
capability matrix: every rung **and** the primary binding of any role that has a
non-empty ladder must advertise `switch_supported` — the primary is the switch
*source*. Today that is `claude-code` and `codex` only, so `pi`/`grok` rungs, and a
ladder on the Pi-backed `ui` role, are rejected at config-save until those cells are
promoted. Pi remains valid as a **spawn-only** primary with no ladder, which is why
`ui` has none here.

`limits` is **Phase 3A design intent and is not on `domain.RoleMap` yet**; sending it
today fails strict decode. For a currently valid, literally pasteable map see
[`examples/role-map.strict.example.json`](examples/role-map.strict.example.json).

### 4.6 Failover rules (fully deterministic)

| Rule | Spec |
|------|------|
| Mode default | **`manual`** (even Target B); `automatic` only if explicitly set |
| Ladder meaning | Rungs are **alternatives after the current target** — first incident does **not** re-select same harness/model as “current” |
| Role preserved | Failover never changes `role_id` / template semantic role |
| Cursor | Persist `failover_cursor` per incident; advance only on successful target ack |
| Cap | `maxFailoversPerIncident`; then stay paused |
| Validation | Every rung **and the primary binding of any role with a non-empty ladder** must pass the capability matrix (`spawn_supported`; `switch_supported` — the primary is the switch source, the rungs are targets; `limit_detection_supported` for auto) |
| Reject | Invalid / unsupported rungs at config save time |

---

## 5. Hand-off, switch, P1 refresh

### 5.1 Two artifacts

**SemanticHandoffV1** (agent-authored, untrusted for Git/tests):
objective, open items, claimed decisions, rejected approaches, uncertainty, latest user intent, source generation, native session pointer.

**ObservedWorkspaceV1** (AO deterministic):
branch, HEAD, worktree, porcelain, SHAs, timestamps, generation id, event cursor, **verified** results only when AO itself captured command + exit status + artifact provenance. Otherwise agent-reported tests stay **attributed claims** requiring re-verification.

Compiler: observed Git/test facts **override** semantic claims.

### 5.2 Worker switch

#3548-class saga: durable states, generation fencing, input gates, idempotency, pre/post-stop failures, crash recovery, native probe, target ack.
Initial matrix: Claude↔Codex; expand by capability gate.

**Pre-stop failure:** source remains usable.
**Post-stop failure:** handoff retained; target retry allowed.
**At most one generation** owns session input at a switch boundary.

### 5.3 Same-harness “fresh conversation” (P1 refinement)

Same `role_id`, worktree, harness; **new native session** + compiled handoff via switch saga. Manual context refresh without provider change. Addresses long-session P1 without periodic compaction.

### 5.4 Orchestrator switch (separate workstream)

Coordinator lease, nudge/routing rebind, pending message transfer, generation fencing, target ack, recovery if source dead / successor fails.

### 5.5 Lifecycle ledger (required, not optional)

Append-only records for: switch, pause, resume, failover, fresh-conversation.
**Not** full per-turn chat ledger. Required for audit and restore of switch-history.

---

## 6. Pi / harness capability matrix (Phase 0)

| Capability | Required for |
|------------|----------------|
| `spawn_supported` | Role binding |
| `switch_supported` | Switch / failover **source and target** (primary of a laddered role, and every rung) |
| `limit_detection_supported` | Auto pause/failover reliance |
| `read_only_enforced` | `workspaceWrites: false` roles |

Unsupported → **config reject**, never silent degrade.
Zai and Kimi validated **separately** on Pi.

---

## 7. Limits order

1. Structured / reviewed envelopes only (never free-text “I hit a limit”).
2. Durable pause; **zero** automatic send/restart.
3. Manual continue on next ladder rung.
4. Automatic mode only if `failover.mode=automatic` (opt-in).

---

## 8. Phases (Target B)

| Phase | Scope | Est. |
|-------|--------|------|
| **0** | Pin AO SHA; migrations; capability matrix; config ownership | 2–3 d |
| **1** | Role map; template CAS; durable fields; `spawn --role`; strict reject roleless; RoleExecutionPolicy; session credentials; strict prompt builder; tests | 4–6 d |
| **2A** | Worker switch saga Claude↔Codex + fresh-conversation + lifecycle ledger | 5–8 d |
| **2B** | Orchestrator ownership transfer | 3–5 d |
| **3A** | Limit detect + durable pause + zero re-nudge | 4–6 d |
| **3B** | Manual continue (complete); dormant opt-in auto-failover host engine landed, detector/promotion/live acceptance remain | 3–5 d |
| **Integration** | UI, dogfood, crash, multi-platform | 3–5 d |

**Total: ~4–6 weeks**

---

## 9. Definition-of-done invariants (must hold)

1. A **strict** project cannot create a **roleless** worker or override resolved harness/model on the ordinary path.
2. A session with **`canSpawn: false`** cannot successfully invoke daemon spawning.
3. **Restore** uses the **pinned template artifact**, even if repo templates changed.
4. A **pre-stop** switch failure leaves the **source usable**.
5. A **post-stop** failure **retains the hand-off** and permits **target retry**.
6. At most **one generation** owns session input at any switch boundary.
7. A **limit** produces **durable pause** with **zero automatic send/restart**.
8. **Failover preserves `role_id`** and cannot exceed its **incident bound**.
9. **Unsupported Pi (or any harness) capabilities reject configuration** rather than degrading silently.
10. **Lifecycle ledger** records every switch/pause/failover/fresh-conversation.
11. **ObservedWorkspaceV1** marks tests **verified** only when AO captured command + exit + provenance.
12. **Failover default mode is manual**; automatic is opt-in.
13. **Read-only roles** only bind to harnesses with `read_only_enforced`.

---

## 10. Coding checklist

1. [x] Fork + pin AO baseline; own migrations
2. [x] Capability matrix + config validation
3. [x] Session-scoped spawn credential
4. [x] Role map schema (schema_version + sha256 + revision)
5. [x] Template CAS artifact by sha256
6. [x] Durable session role fields + switch-history
7. [x] `ao spawn --role`; strict reject roleless / overrides
8. [x] RoleExecutionPolicy enforcement — Codex read-only is enforced; Claude remains correctly unsupported until it has an enforceable sandbox
9. [x] Strict orch prompt builder
10. [x] SemanticHandoffV1 + ObservedWorkspaceV1 + compiler
11. [x] Worker switch saga + fresh-conversation
12. [x] Lifecycle ledger
13. [x] Orchestrator switch protocol — in-place fresh, Codex↔Claude switch,
    gated same-generation recovery, replacement recovery, and final live
    acceptance are complete
14. [~] Limit pause — durable pause, API/CLI, desktop UX and detector boundary landed; no vendor detector is promoted
15. [x] Manual Continue implemented and live-accepted
16. [~] Dogfood against all DoD invariants — the final MVP worker/orchestrator
    matrix is promoted at `322f9c18`; vendor limit detection remains outside
    this MVP and unpromoted. The repository-wide gate is still open on the
    full-suite distinction named above
17. [x] Contain unreadable stored project config per project; preserve raw
    bytes, keep healthy projects listable, surface the broken row as degraded,
    and reject every mutation/import targeting it (`8bb1e3af`)
18. [x] Repair mixed migration histories with a block-level on-entry
    discriminator that retains a genuine lone Muse 53, preserves complete
    stale 53–60 cleanup, and refuses ambiguous upstream schema loudly
    (`586d1156`)
19. [x] Remove fake/Kilocode/OpenCode wall-clock dependencies with structural
    cadence and pure classifier tests, leaving production timeouts unchanged
    (`66d65e4e`)
20. [x] Capture adapter-local real vendor limit research fixtures without
    production detector wiring or capability promotion. The original Claude
    refusal projections and Codex negative quota-state frames landed at
    `11d12414`; the later byte-complete public Claude `rate_limit_event` fixture
    proves a stable `rateLimitType` + `resetsAt` reset-cycle candidate on the
    print-stream-json surface. Codex still has no reached/refused frame, and the
    Claude fixture does not prove an AO interactive ingress or runtime-generation
    channel
21. [x] Ship the desktop role-map editor with deterministic target validation,
    role-map-only CAS, stale-revision conflict/reload behavior, unreadable-row
    mutation fencing, and full-config role-map SHA protection (`54cdbdd8`,
    `c7c1f565`). See
    [`TARGET_B_ROLE_MAP_EDITOR_20260809.md`](TARGET_B_ROLE_MAP_EDITOR_20260809.md).
    This slice added no migration and promoted no capability cell
22. [~] Implement opt-in automatic failover; manual remains the default. The
    dormant one-shot host engine, exact-incident crash convergence, fail-closed
    automatic-config rejection, and explicit legacy-map conversion landed at
    source `1c97c55e` / roles-trunk `29becc6d`, with runtime-generation
    binding correction `72bca3e4`; see
    [`TARGET_B_AUTOMATIC_FAILOVER_20260809.md`](TARGET_B_AUTOMATIC_FAILOVER_20260809.md).
    The stable reset-cycle candidate is now proven by an exact positive research
    fixture, but product completion still requires AO interactive ingress with
    the exact observing runtime generation, a production Router caller, and a
    separate reviewed capability-promotion commit plus positive live acceptance.
    This implementation slice added no migration/API/prompt change or capability
    promotion; every production `limit_detection_supported` cell remains false,
    its native correction acceptance is negative/dormancy proof rather than
    positive automatic-switch acceptance, and **9009+** remains the next fork
    migration

---

## 11. Non-goals (v1)

- Host auto-spawn of workers
- Free-form `--agent`/`--model` as supported orch path on strict projects
- OpenCode
- Full per-turn event ledger
- Silent capability degradation
- Auto-failover as default

---

## 12. One-line definition

**AO fork: Intent-grade role templates, daemon-resolved `spawn --role` with no roleless bypass on strict projects, enforceable RoleExecutionPolicy, immutable template artifacts, durable switch/pause/failover ledger, #3548-class worker switching (orch separate), manual-first failover, Pi only where capability gates pass.**

---

## 13. Approval state

| Item | Status |
|------|--------|
| Product direction | Approved |
| Codex round 1 amendments | Incorporated |
| Codex round 2 five contracts + DoD | **Incorporated** |
| Target B 4–6 weeks | Locked |
| Ready for implementation | **Yes — mark approved; start on user “go”** |
