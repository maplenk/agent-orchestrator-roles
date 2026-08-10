# Upstream contribution ledger

**Status:** active extraction ledger; no row is authorization to open a pull
request unless its status permits it.

**Created:** 2026-08-10.

**Downstream source:** `roles/multi-sub-v1` at `619bed7cb`.

**Measured program baseline:** local `upstream/main` at `3e2dbf424`.

**Current contribution-branch base:** `upstream/main` at `c17b6c3f5`.

**Accepted Sync 3 pin and merge base:** `6e9dbb105`.

**Source inventories:**
[`UPSTREAM_SYNC3_EXECUTION.md`](UPSTREAM_SYNC3_EXECUTION.md) and
[`UPSTREAM_SYNC3_PLAN.md`](UPSTREAM_SYNC3_PLAN.md).

**Umbrella design review:**
[#3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802).

This ledger is the operational source of truth for extracting focused changes
from the roles product line into
[`maplenk/agent-orchestrator`](https://github.com/maplenk/agent-orchestrator).
It runs downstream to upstream; the Sync 3 documents remain authoritative for
the opposite upstream-to-downstream integration.

At the measured program baseline, the fork is 240 commits ahead and 3 behind, with 420 changed
files and a `+68,315/-2,525` aggregate delta. Upstream PR
[#3548](https://github.com/Untrivial-ai/agent-orchestrator/pull/3548) is already
in the fork's ancestry as `01cb67fe1`, so this program extracts only behavior
that remains on top of durable worker switching. The working forecast is
approximately 30–36 pull requests. A feature PR targets 1,500–2,000
handwritten added lines and may not exceed 2,500 without prior maintainer
approval.

## How to read and update this ledger

### Coverage classification

Every row receives exactly one of these values after its current-upstream
audit:

- `covered` — current upstream implements the entire invariant with equivalent
  boundary and regression coverage. The row produces no PR.
- `partial` — current upstream implements part of the invariant, but a named,
  tested gap remains. Only that gap is eligible for extraction.
- `absent` — no current-upstream implementation of the invariant was found.
- `downstream-only` — the behavior is intentionally excluded from upstream,
  regardless of overlap.
- `unclassified` — temporary state permitted only with status
  `audit-required`. No issue or PR may be opened from it.

The classification is based on behavior and tests, not matching filenames or
patch IDs. A row moves from `unclassified` only after recording the upstream
symbol/file evidence and the downstream test that proves the gap. Only
`partial` and `absent` rows are upstream candidates.

### Workflow status

- `open` — an upstream PR exists; its next external action is recorded.
- `awaiting-assignment` — the focused issue exists, but CONTRIBUTING.md's
  claim/assignment or non-trivial-work thumbs-up gate is still pending. Do not
  build or open an implementation PR from this row.
- `ready-for-issue` — coverage, dependency, size, and migration gates are
  complete.
- `design-approval` — wait for approval on the umbrella design issue before an
  implementation issue or branch.
- `audit-required` — finish the current-upstream behavior audit first.
- `size-audit` — isolate the upstream-shaped diff and confirm the PR ceiling.
- `split-required` — the seed delta exceeds the ceiling; replace the family row
  with independently reviewable child rows before opening an issue.
- `blocked:<row>` — a named dependency has not merged.
- `downstream-only` or `covered` — terminal non-contribution states.

Only one PR may be open per dependency chain. Independent chains may proceed
in parallel. Every implementation branch starts at the latest
`upstream/main` in `maplenk/agent-orchestrator`, never at a roles-fork commit.
Every non-trivial row retains one upstream issue and one PR. Before building,
comment to claim the focused issue and wait for maintainer assignment; also
obtain a maintainer thumbs-up for non-trivial work. Before opening a PR, record
that authorization in the row. Existing draft PRs opened before assignment are
not review-ready: leave them draft and ask maintainers whether to assign and
continue or close them.

### File and LOC measurements

“Post-Sync-3 files” names the current downstream surfaces to re-audit after the
Sync 3 semantic merge. “Handwritten LOC” is the seed source-patch churn for Go,
TypeScript/JavaScript, tests, and SQL migrations, excluding documentation,
generated sqlc, generated OpenAPI, and generated `schema.ts`. It is not the
eventual PR diff and may count a line more than once across corrective commits.
Before an implementation issue opens, replace the seed with the isolated
`upstream/main...branch` handwritten file count and LOC. Generated artifacts
ship in the same PR but never count toward the ceiling.

## Contribution rows

### Chain 1 — independent reliability

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| Missing tmux server is authoritative absence, while genuine probe failures remain unknown. | `R1 tmux absence` | `be4321d1`, `66d4ceb3`, `6a07d5d6`, `24906d35`; upstream extraction `976dc41ca` | 5 files: tmux runtime, outbound port, manager, tests | extraction `+159/-35` | `absent` | [#3800](https://github.com/Untrivial-ai/agent-orchestrator/issues/3800) / [#3801](https://github.com/Untrivial-ai/agent-orchestrator/pull/3801) | None | n/a | n/a | Focused tmux and manager absence/error tests; upstream CI | `open` — mergeable with no reviews or review requests. Go, CLI E2E, and gitleaks workflow runs concluded `action_required`; at the 22:00 IST Discord sync ask maintainers to approve and run first-contributor workflows for #3801, #3804, and #3806. Request review only after checks run. Blocks only overlapping tmux-classification work. |
| Exactly one daemon owns and reconciles an AO data directory. | `R2 data-dir lease` | `9480bdc7`; upstream extraction `3db79ad0` | 6 files: `backend/internal/datadirlock/`, daemon wiring, HTTP fallback comments | extraction `+391/-8` | `absent` | [#3805](https://github.com/Untrivial-ai/agent-orchestrator/issues/3805) / [draft #3806](https://github.com/Untrivial-ai/agent-orchestrator/pull/3806) | None | n/a | n/a | Repeated and race lock tests, daemon/HTTP suites, serial full backend suite, build/vet/lint, Linux/Windows cross-compilation | `open` — draft opened before issue assignment; do not request review. At the 22:00 IST sync, disclose the ordering error and ask whether maintainers want to assign #3805 and retain #3806 or close it. If retained, request workflow approval, address checks, and only then request review. |
| Replacing a live xterm handle cannot race queued viewport work against disposed terminal state. | `R3 xterm disposal` | `f8d31046`; upstream extraction `1d13dd72` | 2 files: `XtermTerminal.tsx`, real-xterm E2E | extraction `+33/-6`; component change `+14/-6` | `absent` | [#3803](https://github.com/Untrivial-ai/agent-orchestrator/issues/3803) / [draft #3804](https://github.com/Untrivial-ai/agent-orchestrator/pull/3804) | None | n/a | n/a | Frontend/E2E typecheck, 49 focused Vitest tests, exact real-xterm Playwright replacement regression | `open` — draft opened before issue assignment; do not request review. At the 22:00 IST sync, disclose the ordering error and ask whether maintainers want to assign #3803 and retain #3804 or close it. If retained, request workflow approval, address checks, and only then request review. |
| Tmux servers are namespaced by resolved AO data directory so isolated installations cannot share processes. | `R4 tmux namespace` | `9f687d79` | 9 files across runtime selection, tmux socket/config, daemon, ports, service, tests | seed `+247/-8` | `absent` | pending issue / PR | None | n/a | n/a | Socket-name determinism/collision tests and daemon wiring tests | `blocked:R1` — rebase after #3801 because both touch tmux classification/runtime boundaries. |
| Every new Claude native conversation receives a caller-reserved stable UUID and cannot replay another session's identity. | `R5 Claude identity` | `15a4e6f0` | 7 code/test files across Claude adapter, agent port, manager, switching | seed `+220/-20` | `absent` | pending issue / PR | None | n/a | n/a | Concurrent reservation, replay, fresh-switch, and adapter invocation tests | `ready-for-issue` |

### Chain 2 — roles and delegation

This chain requires umbrella-design approval before its first implementation
issue. Rows remain sequential unless a row is explicitly split after the
upstream-shaped diff is measured.

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| Agent capabilities are daemon-authoritative, and a resolved read-only role is enforced at every AO-managed write boundary. | `RL1 capability contract` | `9fcd095c`, `4ab636fe` | 20 code/test files; core packages `roles/capabilities` and `roles/readonly` plus adapter/manager integration | seed `+750/-56` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Registry matrix, read-only launch configuration, negative write/delegation paths | `design-approval` |
| A strict, versioned role map decodes and validates role, harness, model, permissions, and failover references without permissive fallback. | `RL2 role-map schema` | `31ca4e27`, `3c3aef51`, `663f9339`, `8bb1e3af` | 5 core schema/resolve files plus containment tests | seed floor `+739/-0` from `31ca4e27`; remeasure fixes | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | No database migration; project config remains durable JSON | n/a | n/a | Strict decode, referential validation, unreadable-config containment, legacy compatibility | `blocked:RL1` |
| Session role identity and immutable template content are pinned durably so later project edits cannot rewrite a running session's authority. | `RL3 template CAS` | `31ca4e27`, `1f80bdb5` | 18 source/test/migration files after separating RL2 | seed floor `+1,521/-97` from `31ca4e27`; remeasure host-ownership fix | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork `9000_session_role_fields.sql`: session role/template fields and `template_artifacts` | Required: historical repair row remains separate; add inert `9000 ↔ upstream TBD` pairing before PR opens | `TBD / TBD / TBD` — currently fork-exclusive | Fresh and upgrade migration matrix, template hash/CAS, role pin immutability, host-owned execution fields | `blocked:RL2` |
| Only a session holding a scoped, hashed spawn capability and valid operator/session principal can invoke `ao spawn --role`. | `RL4 authenticated role spawn` | `3269f389`, `132f94d6`, `75f5132c`, `81dfdb4d`, `93fcf1f8` | 32 code/test files across CLI, auth, daemon, controller, credential authority, runfile, desktop client | seed `+1,262/-347` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork `9001_session_spawn_capability_hash.sql`: `sessions.spawn_capability_hash` | Required: historical repair row remains separate; add inert `9001 ↔ upstream TBD` pairing before PR opens | `TBD / TBD / TBD` — currently fork-exclusive | Principal scoping, token secrecy, default-port and external CLI transport, replay and misuse errors | `blocked:RL3` |
| Strict delegation carries a role through service and HTTP/CLI boundaries and never accepts a free-form execution target in its place. | `RL5 delegation surface` | `5de2df0c`, `2198a521` | 5 primary code/test files plus read-only spawn regression | seed floor `+144/-5` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Happy path, missing/unknown role, read-only gate, typed daemon envelope, DTO drift | `blocked:RL4` |
| The desktop composer selects an authorized role for strict projects and omits free-form agent/model fields. | `RL6 desktop role selection` | `2a46fde8`, `1f80bdb5`, `4703071f` | 12 TS/TSX and supporting backend test files before Sync 3 reconciliation | seed `+742/-67` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Composer payload, defaults, unavailable role, project reload, real Electron flow | `blocked:RL5` |
| Role-map updates use one daemon-authoritative compare-and-swap revision and return a conflict instead of losing concurrent edits. | `RL7 atomic role-map API` | `e65a1100` | 15 handwritten code/test files across domain, service, controller/spec source, SQL query/store | seed `+617/-12` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | No new table/column; updates existing project configuration and revision | n/a | n/a | Expected-revision success/conflict, invalid map, store atomicity, generated-contract zero diff | `blocked:RL2` |
| Desktop role-map editing preserves strict validation, degraded unreadable state, and CAS conflict recovery. | `RL8 role-map editor` | `a9fa4e0c`, `4703071f` | 7 TS/TSX source/test files plus eight locale catalogs | seed `+1,463/-31` excluding locale JSON | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Field validation, reorder/edit/delete, degraded state, stale revision, Settings integration, Electron flow | `blocked:RL7` |
| Shipped starter roles resolve to installed immutable profile artifacts and expose switchable, authorized defaults without granting capabilities implicitly. | `RL9 starter catalogue` | `13fc6acd`, `8071d4b1`, `e38ae32d`, `762ae160` | 19 Go/TS/JS source/test files plus profile Markdown assets | seed `+842/-74` excluding profile Markdown | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Packaging/staging, profile hash, starter-map validation, switch target resolution, installed-app smoke | `blocked:RL3` |

### Chain 3 — post-#3548 worker-switch audit

These are audited gaps, not eight promised PRs. On 2026-08-10 each invariant,
downstream symbol/test family, and post-Sync-3 file set was compared with
`upstream/main` at `c17b6c3f`. All eight are partial rather than wholesale
absences: #3548 supplies the durable worker-switch base, while the named
hardening remains downstream-only. No issue opens until the row's isolation,
split, pairing, and dependency status permits it.

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| The exact encoded target launch command is proven to fit before source interaction, with ConPTY parity. | `SW1 launch capacity` | `f55cd351` | `runtime/conpty/runtime_test.go`; `runtime/tmux/tmux.go`, `tmux_test.go` | seed `+293/-69` | `partial` — #3548 constructs the supervised command and ConPTY already rejects oversized commands, but current upstream has no pure `PreflightCreate`/`PreflightRestart` or exact encoded tmux-size boundary | none | None | n/a | n/a | Missing upstream symbols and downstream-only boundary/source-alive tests verified at `c17b6c3f` | `size-audit` — isolate on current upstream and confirm the shared `tmux.go` file does not overlap #3801's classification hunk before opening an issue. |
| Claude and Codex continuation probes preserve `available` / `unavailable` / `unknown`; probe failure never means conversation absence. | `SW2 provider probes` | `7a533f53` | Claude `activity_test.go`, `claudecode.go`, `claudecode_test.go`, `continuation.go`; Codex equivalents; `ports/agent_continuation_test.go` | seed `+334/-91` | `partial` — #3548 supplies tri-state probes, but upstream lacks invalid-ID-as-unknown coverage, exact config-dir precedence, compressed-Codex resume handling, and exact transcript-name matching | none | None | n/a | n/a | Upstream retains the older `findCodexTranscript(ctx, root, sessionID)` shape; the hardened signature/tests are downstream-only at `c17b6c3f` | `size-audit` — isolate the provider-specific gap and keep unrelated activity-hook assertions out of the PR. |
| The handoff artifact is finalized and capacity-checked while the source remains alive; source stop is strictly later. | `SW3 handoff ordering` | `bb3ec3be` | `terminalui/composer.go`, `composer_test.go`; `session_manager/agent_switching.go`, `agent_switching_test.go` | seed `+392/-90` | `partial` — #3548 has durable handoff finalization and source-stop phases, but upstream has no minimum/final target-command preflight before the irreversible stop boundary | none | None | n/a | n/a | `preflightMinimumTargetCommand` and `preflightPreparedTargetCommand` plus source-alive ordering regressions are absent at `c17b6c3f` | `blocked:SW1` — then isolate printable-draft parsing separately from the switch-ordering child if it is independently reviewable. |
| Native identity and session execution state are promoted only after acknowledgement for the exact AO session and target generation. | `SW4 exact acknowledgement` | `89f95742` | `session_manager/manager.go`, `switch.go`; `queries/agent_switching.sql`; store implementation/test; generated sqlc travels with source | seed `+120/-16` | `partial` — #3548 already generation-fences and write-once records acknowledgement, but native-session promotion is not one exact acknowledgement-gated SQL operation | none | None beyond upstream `0085_agent_switching.sql` | n/a unless extraction changes schema | n/a | Downstream-only `PromoteAcknowledgedAgentSwitchNativeSession` and exact session/generation mismatch regression verified against `c17b6c3f` | `size-audit` — isolate the query/store promotion change and regenerate sqlc with zero subsequent diff. |
| SQLite enforces the switch initial tuple, immutable provenance, legal state transitions, and coherent recovery tuple. | `SW5 persistence guards` | `5778f47c` | domain contract test; `migrate_agent_switching_contract_test.go`; `9009_agent_switch_contract_guards.sql`; store implementation/tests; five fork-repair tests that must not travel upstream | seed `+833/-204` | `partial` — `0085_agent_switching.sql` has column checks, native-scope triggers, and one-active uniqueness, but lacks the four initial/provenance/transition/recovery tuple guards | none | Fork pairing-only `9009_agent_switch_contract_guards.sql`: four guard triggers | Required: add inert pairing-only `9009 ↔ upstream TBD`; no legacy-version fields | `TBD / TBD / TBD` — currently fork-exclusive | Exact `0085` comparison and downstream adversarial migration/store tests audited at `c17b6c3f` | `size-audit` — exclude every fork-repair file; allocate the upstream migration, land its inert pairing row, and run the ten-case matrix before a PR. |
| The durable saga records exact authorized target harness/model, role snapshot, source generation, and target generation; retries cannot substitute intent. | `SW6 authorized intent` | `190ea997`, `26584306`, `e2a8c597` | 25 files: domain fingerprint/intent, manager admission, service policy, `9010` migration, agent-switch/failover queries and stores, tests, and generated sqlc | seed floor `+581/-121`, excluding characterization follow-up | `partial` — #3548 fingerprints session/harness/note and stores source/target generation, but not the authorized model, immutable role snapshot, or failover source fence | none | Fork pairing-only `9010_agent_switch_authorized_intent.sql`: saga/attempt columns and immutability triggers | Required: split generic target/model/generation effects from role/failover effects, then pair each exact equivalent migration separately | `TBD / TBD / TBD` — currently fork-exclusive | `ComputeAuthorizedAgentSwitchRequestFingerprint` and model-policy tests are absent upstream; `0085` has no `target_model` or `role_snapshot_json` | `split-required` — generic target-model/generation safety may proceed after isolation; role snapshot is `blocked:RL2`, and failover coupling stays with Chain 6. |
| Ambiguous post-stop delivery exposes an exact, generation-fenced safe-recovery operation instead of generic resend. | `SW7 safe recovery` | `b31f406f`, `10d5d25f` | `agent_switching.go`, `agent_switching_test.go`, `agent_switch_recovery_guard.go`, `agent_switch_recovery_guard_test.go` | seed floor `+155/-0` | `partial` — #3548 classifies recovery-required states and reconciles on boot, but exposes no exact manager operation fenced by session plus switch ID | none | None | n/a | n/a | No upstream `RecoverAgentSwitch`; downstream tests prove no resend, exact active-saga matching, and unresolved-ownership containment | `size-audit` — isolate the manager operation and initiation-disabled guard from failover-only call sites. |
| One canonical service/HTTP/CLI/UI contract exposes create, options, history, and exact recovery while preserving only a thin compatibility wrapper. | `SW8 recovery surfaces` | `6457c621`, `b4038015`, `c68f36e1` | 33 total files: 14 handwritten backend source/tests, generated OpenAPI/TS, and 17 frontend source/tests plus locale catalogs | seed `+1,298/-235` | `partial` — #3548 ships legacy create, history, handoff, CLI, and a fixed-harness desktop control; upstream has no exact recovery route or daemon-authoritative options contract | none | None | n/a | n/a | `agent-switch-options`, `RecoverAgentSwitch`, `useAgentSwitchOptions`, and `useRecoverAgentSwitch` are absent at `c17b6c3f` | `split-required` — generic canonical create/recovery backend waits for SW7; role-authorized options/desktop waits for RL2 and must be a separate child. |

### Chain 4 — durable pause

The policy requires umbrella-design approval. Schema-free limit and UI
preparation may be audited concurrently, but a schema-bearing pause PR cannot
open before its inert pairing row is implemented and green.

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| A pause is a durable, incident- and runtime-generation-fenced fact that blocks AO writes until the matching resume/restart transition. | `P1 pause primitive` | `506467f5`, `d0d7360a`, `8621303f`, `f39ee59b`, `e90b543a` | 17 primary handwritten domain/manager/guard/storage files | seed `+2,540/-132` before later fences | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork `9007_session_pause.sql`: `sessions.pause_json` | Required: historical repair row remains separate; add inert `9007 ↔ upstream TBD` pairing before PR opens | `TBD / TBD / TBD` — currently fork-exclusive | Fresh/upgrade, incident and generation mismatch, boot, chat/terminal input fences, resume idempotency | `size-audit` — isolate immediately; split domain/storage from manager fences if additions remain above 2,500. |
| Pause/resume remains an operator-only thin service/HTTP/CLI operation with typed errors and dead-runtime visibility. | `P2 pause API/CLI` | `930fa561`, `eee31d7b`, `7e388bf2` | 18 handwritten controller/service/client/test files | seed floor `+861/-23` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None beyond P1 | n/a | n/a | Auth, DTO parity, missing/expired incident, runtime-dead reads, daemon envelopes | `blocked:P1` |
| The desktop clearly exposes paused state and safe Resume/Restart actions without locally reimplementing policy. | `P3 pause desktop` | `d6d96c89`, `a3f5ec1b` (shared surface only) | 5 primary TS/TSX source/test files plus locales | seed floor `+284/-0` before Continue integration | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Inspector visibility, action states, chat input fencing, locale coverage, real Electron flow | `blocked:P2` |
| Typed limit events enter through a detector registry and router, but every production detector remains unsupported until separately approved. | `P4 limit seam` | `2e14852c`, `1d2f34f4` | 15 handwritten domain/router/manager/store test files | seed `+1,042/-42` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Empty registry, stable source key, ownership/generation mismatch, duplicate event, unsupported harness | `design-approval` — may proceed independently of P1 pairing after approval. |

### Chain 5 — orchestrator ownership and switching

This chain begins only after the required role and switch-policy rows merge.
Its source families are large; the boot/reaper family must be decomposed before
an issue is opened.

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| The session manager is the sole authority for one active orchestrator owner per project across every launch, adoption, and teardown path. | `O1 ownership gate` | `6e9d58d5`, `b83ca31f`, `bb43c267`, `4411c0f5` | 6 handwritten manager/service test files | seed `+1,552/-238` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Concurrent starts, canonical workspace aliases, bypass paths, shared-workspace teardown | `blocked:RL3` |
| Database uniqueness and a fail-closed boot reaper reconcile duplicate owners before any daemon surface or restore is served. | `O2 uniqueness/reaper family` | `6b482589` through `abab7b3e` as inventoried in Sync 3 history | 30 handwritten daemon/runtime/manager/storage/migration test files | seed `+4,088/-381` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork `9004_one_active_orchestrator.sql`: reap queue and unique partial index | Required: historical repair row remains separate; add inert `9004 ↔ upstream TBD` pairing for the first schema child | `TBD / TBD / TBD` — currently fork-exclusive | Duplicate reconciliation, loser identity, restricted cleanup, restore ordering, unreadable evidence, second boot | `split-required` — create separate database-reconciliation and boot/restore children, each below the ceiling, before opening issues. |
| Orchestrator replacement and in-place fresh conversation survive restart without losing ownership or lifecycle evidence. | `O3 replacement/fresh` | `cc10f4f8`, `e1c2a7dc`, `b3ce92a6`, `2806b18c` | 25 handwritten domain/handoff/manager/store/migration test files | seed `+2,202/-128` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork `9005_orchestrator_replacement_intent.sql` and `9006_lifecycle_ledger_orchestrator_fresh.sql` | Required: separate inert `9005` and `9006 ↔ upstream TBD` pairings; do not combine non-equivalent effects | each `TBD / TBD / TBD` — currently fork-exclusive | Pre/post-stop crash recovery, roleless legacy sessions, lifecycle kind, restart/idempotency | `blocked:O2`; size-audit before issue |
| An orchestrator may switch across authorized harnesses through its ownership protocol, never through the worker saga. | `O4 cross-harness backend` | `e3170a04`, `051db38b`, `97c7913c` | 15 handwritten manager/service test files | seed floor `+648/-71` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Uses fork `9002_lifecycle_ledger.sql` and `9003_session_switch_pending.sql`; no new schema expected | Pair `9002`/`9003` only if their exact effects are upstreamed; historical repair remains separate | each `TBD / TBD / TBD` — currently fork-exclusive | Worker/orchestrator route separation, exact role authorization, rollback/recovery, one-owner invariant | `blocked:O3`, `blocked:RL2`, and dependent SW policy rows |
| CLI and desktop expose orchestrator switching only in the correct TUI mode and render daemon-authoritative recovery state. | `O5 switch surface` | `fa4d4f90`, `051db38b`, `31b6d7ef`, `72f274a7` | 24 handwritten Go/TS/TSX source/test files | seed floor `+1,357/-92` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Route validation, chat-mode refusal/hidden control, pending state, typed errors, Electron flow | `blocked:O4` |

### Chain 6 — manual failover

Manual Continue requires roles, durable pause, and the accepted worker-switch
contract. Automatic failover remains downstream-only until a production
detector is separately approved, promoted, and positively live-tested.

| Invariant | Chain | Downstream commits | Post-Sync-3 files | Handwritten LOC | #3548 coverage | Issue / PR | Schema effects | Migration pairing | Fingerprint shared since: upstream file / merge SHA / downstream sync | Validation | Status |
|---|---|---|---|---:|---|---|---|---|---|---|---|
| Failover policy previews an authorized next rung and stores each attempt, generation, role snapshot, and lifecycle record transactionally. | `F1 policy/store` | `cb333f07`, `b952a751`, `5b73d3c3`, `5d5a7866` | 11 handwritten domain/query/store/migration test files | seed `+1,002/-15` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | Fork pairing-only `9008_session_failover_attempts.sql` and `9011_failover_authorized_role_snapshot.sql` | Required: separate inert pairing-only `9008` and `9011 ↔ upstream TBD` rows; neither has legacy identities | each `TBD / TBD / TBD` — currently fork-exclusive | Transaction rollback, one attempt/rung, immutable snapshot, fresh/upgrade/idempotency, pairing-only matrix | `blocked:RL2`, `blocked:P1`, and accepted SW policy rows |
| Manual Continue adopts or creates exactly one attempt and converges on the durable worker saga without spending another rung on retry. | `F2 manual Continue` | `01cf6580`, `45631c0d`, `866d3f08`, `b23e4970`, `ff5c3b1c` | 4 primary manager files plus focused recovery tests | seed floor `+2,240/-45` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None beyond F1 and accepted switch schema | n/a | n/a | Duplicate/concurrent Continue, crash before saga, completed retry, exhausted ladder, generation equality | `blocked:F1`; size-audit before issue |
| Continue is exposed through thin service, HTTP, and CLI boundaries with preview and typed conflict/recovery envelopes. | `F3 Continue API/CLI` | `b4c71d07`, `d8421a32`, `064eaeef`, `7c018683` | 8 primary handwritten CLI/controller/service test files | seed floor `+699/-9` | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Preview/execute, missing target, conflict, completed result, row-level degraded reads, DTO parity | `blocked:F2` |
| The desktop pause surface offers daemon-authorized Continue targets and reflects attempt/saga progress without local escalation logic. | `F4 Continue desktop` | `a3f5ec1b` | 8 primary TS/TSX source/test files plus locales | seed `+888/-79` excluding locale JSON | `absent` | [umbrella #3802](https://github.com/Untrivial-ai/agent-orchestrator/issues/3802) | None | n/a | n/a | Target list, disabled/in-flight states, duplicate conflict, completed state, Electron flow | `blocked:F3` |
| Automatic limit-triggered failover advances ladders without an operator action. | `F5 automatic failover` | `29becc6d`, `72bca3e4` | 24 handwritten files | seed `+2,722/-124` | `downstream-only` | none | No new migration beyond F1/P1 | n/a | n/a | Retain downstream acceptance only; no upstream PR until detector promotion gate changes | `downstream-only` |

## Migration repair and upstream-equivalence pairing

Historical renumber repair and upstream-equivalence pairing are separate
mechanisms that may share physical fingerprint helpers. Do not widen the
existing `forkMigration` record to carry pairing semantics.

The shared pairing framework and fail-closed matrix landed downstream in
`dea26ee`. Its production pairing table remains intentionally empty until the
first approved schema extraction receives an exact upstream filename.

### Hard gate and activation rule

For every schema-bearing contribution, pairing code and tests land in the fork
before the upstream PR opens. The pairing row is inert until the running
binary's embedded migration set contains both the expected upstream version
and the exact expected filename.

- Expected file absent: write no ledger identity.
- Expected version occupied by a different filename: write no identity and let
  that unrelated migration run normally.
- PR rejected or renumbered: the old row remains inert. Update and land the row
  before pushing a renumbered upstream branch.
- Exact version and filename present: enable identity-plus-effect
  reconciliation below.
- A physical effect alone never authorizes recording an upstream version.

Pairing records contain the fork version and filename, expected upstream
version and exact filename, physical-effect function, and shared-since merge
metadata. Pairing-only migrations have no `firstVersion`/`oldVersion` fields
and use no zero sentinel. `TestForkRenumberHasARepairPathForEveryAbandonedVersion`
continues to range over historical repair rows only; pairing rows receive a
separate structural test.

### Identity-plus-effect reconciliation

After exact-file activation, reconcile as follows and fail the transaction
closed for every state not listed as valid:

| Fork identity | Upstream identity | Physical effect | Action |
|---|---|---|---|
| absent | absent | absent | Record the fork identity so the canonical upstream migration runs once. |
| present | absent | present | Record the upstream identity. |
| absent | present | present | Record the fork identity. |
| present | present | present | No-op. |
| absent | absent | present | Fail: effect-only state has no reliable provenance after upstreaming. |
| present | either | absent | Fail: a recorded identity lacks its required effect. |
| either | present | absent | Fail: a recorded identity lacks its required effect. |

Never delete a legitimate identity. Pair only physically equivalent effects;
if upstream proposes a superset or different effect, split its migration and
pair only the equivalent unit.

### Fingerprint registry

“Shared since” is populated only after upstream merge and the first downstream
sync containing that merge. Until then, every fingerprint remains explicitly
fork-exclusive.

| Fork migration | Historical repair identities | Physical fingerprint | Expected upstream version / exact filename | Upstream merge SHA | First downstream sync | Shared state |
|---|---|---|---|---|---|---|
| `9000_session_role_fields.sql` | `42 → 53 → 9000` | `sessions.role_id` exists; equivalence review must include every role/template effect | TBD | TBD | TBD | fork-exclusive |
| `9001_session_spawn_capability_hash.sql` | `43 → 54 → 9001` | `sessions.spawn_capability_hash` exists | TBD | TBD | TBD | fork-exclusive |
| `9002_lifecycle_ledger.sql` | `44 → 55 → 9002` | `lifecycle_ledger` table exists; equivalence requires its full schema/indexes | TBD | TBD | TBD | fork-exclusive |
| `9003_session_switch_pending.sql` | `45 → 56 → 9003` | `sessions.switch_pending_json` exists | TBD | TBD | TBD | fork-exclusive |
| `9004_one_active_orchestrator.sql` | `46 → 57 → 9004` | `idx_sessions_one_active_orchestrator` exists; equivalence review must include reap-queue effects | TBD | TBD | TBD | fork-exclusive |
| `9005_orchestrator_replacement_intent.sql` | `47 → 58 → 9005` | `orchestrator_replacement_intent` table exists | TBD | TBD | TBD | fork-exclusive |
| `9006_lifecycle_ledger_orchestrator_fresh.sql` | `48 → 59 → 9006` | lifecycle table SQL admits `orchestrator_fresh_conversation` | TBD | TBD | TBD | fork-exclusive |
| `9007_session_pause.sql` | `49 → 60 → 9007` | `sessions.pause_json` exists | TBD | TBD | TBD | fork-exclusive |
| `9008_session_failover_attempts.sql` | none; pairing-only | `session_failover_attempts` table plus required indexes | TBD | TBD | TBD | fork-exclusive |
| `9009_agent_switch_contract_guards.sql` | none; pairing-only | exact four `agent_switches_*guard` triggers | TBD | TBD | TBD | fork-exclusive |
| `9010_agent_switch_authorized_intent.sql` | none; pairing-only | exact saga/attempt columns and immutable/initial-tuple triggers | TBD | TBD | TBD | fork-exclusive |
| `9011_failover_authorized_role_snapshot.sql` | none; pairing-only | attempt `role_snapshot_json` plus insert/immutability triggers | TBD | TBD | TBD | fork-exclusive |

### Required schema test matrix

Every schema-bearing row must pass all of the following before its upstream PR
opens:

1. Fresh upstream database with only the upstream migration.
2. Fresh combined fork database containing both migration identities.
3. Existing fork database syncing the upstream migration.
4. Existing upstream database entering the fork.
5. Second-boot idempotency.
6. Mismatched or non-equivalent physical fingerprint.
7. Upstream migration renumbered after inert pairing code landed.
8. Pairing-only migration with no historical identity, including the
   `9008`–`9011` shape.
9. Expected upstream version occupied by another filename, proving it is not
   suppressed.
10. Either identity recorded without the physical effect, which must fail
    closed.

The matrix must also prove that effect-only provenance is rejected after
activation, generated sqlc/API artifacts have zero subsequent diff where
applicable, and historical abandoned-version coverage remains scoped to repair
rows.

## Explicit downstream-only inventory

These changes are not contribution candidates and do not consume the upstream
PR forecast:

| Downstream concern | Source commits / files | Reason |
|---|---|---|
| Automatic failover engine and runtime-generation binding | `29becc6d`, `72bca3e4` | No approved production detector or positive live production acceptance. |
| Vendor limit fixtures and detector research | `11d12414`, `14ce328d` and associated evidence docs | Evidence only; no prose parsing or unsupported provider inference is upstreamed. |
| Fork migration renumber/ambiguity repair and future pairing machinery | `69d2ec16`, `0466cf98`, `a62b2ee7`, `586d1156`, `dea26ee`, `migrate_fork_range.go`, `migrate_upstream_pairing.go` | Protects installations that ran fork-only migration identities; upstream must never carry fork ledger repair. |
| Sync 3 integration evidence, conflict inventories, and cumulative promotion records | `UPSTREAM_SYNC3_PLAN.md`, `UPSTREAM_SYNC3_EXECUTION.md`, related evidence commits | Fork maintenance history, not product behavior. |
| Fork upstream-watch automation | `7744297b`, `.github/workflows/upstream-watch.yml` | Operates fork remotes and tracker policy; inappropriate for canonical upstream. |
| Dogfood, installed-role pipeline, release evidence, and `fugu_review` artifacts | `docs/roles/*LIVE*`, MVP evidence, local review artifacts | Acceptance input may be summarized in PRs, but fork-local artifacts are not copied upstream. |

## Wave 1 operating queue

Wave 1 starts without waiting for migration pairing because all immediate code
PRs are schema-free:

1. Keep this seeded ledger current and open the umbrella design issue.
2. At the 22:00 IST Discord sync, ask for assignment/authorization on each
   focused issue before implementation or review. For #3801, also request
   workflow approval, then address checks and request review.
3. Disclose that draft data-directory lease PR #3806 preceded assignment of
   #3805; ask whether to retain the draft or close it. If retained, approve and
   run workflows before requesting review.
4. Do the same for xterm issue #3803 and draft PR #3804.
5. Implement and validate migration pairing in the fork in parallel; it gates
   the first schema-bearing role or pause PR, not Wave 1.

Before each implementation issue opens, update its row with the current
upstream SHA, exact isolated handwritten diff, final
`covered`/`partial`/`absent` evidence, dependency state, and—when
schema-bearing—the activated migration filename and complete pairing test
evidence. After the issue opens, claim it and wait for assignment plus any
required non-trivial-work thumbs-up before building or opening its PR.
