# Final roles MVP specification

**Status:** MVP implementation, independent review, promoted live acceptance,
post-review default-data-dir replay, and the final installed-app role pipeline
are complete. The post-acceptance Grok fixes are integrated at `72f274a7`; the
runtime replay passed on `3c3aef51`; and the installed Claude→Grok→Codex
pipeline plus no-nudge verifier return passed on `f4b28012`. The
existing-project starter-role and installed Switch close-out passed on
`762ae160`. At the time of this accepted snapshot, the ordinary full backend
run retained three untouched wall-clock failures. Target B test hygiene later
removed those real-time dependencies at `66d65e4e`; current repository status
is tracked in `REMAINING_PLAN.md`. The verified MVP/static/API/frontend and
installed-app gates are complete.

**Accepted implementation and runner SHA:** `166e9e63`

**Promoted live-evidence commit:** `322f9c18`

**Post-evidence review integration:** `72f274a7` — runtime-server absence,
consumer-specific recovery, Chat switch-preview and mutation preflight, and
persisted-role read safety. Independent combined review reports no remaining
P1/P2.

**Default-data-dir runtime replay:** `3c3aef51` — exact runtime code shared by
the later integration; the subsequent commits are storage/service-only. This
targeted replay supplements rather than rewrites the promoted matrix.

**Installed-app role-pipeline close-out:** `f4b28012` — the normal strict
Claude orchestrator → Grok implementor → read-only Codex verifier path passed
in the replaced `/Applications` app, and a fresh follow-up proved the
orchestrator retrieved a terminal-only verifier report without a manual wake.
See [`ROLE_PIPELINE_LIVE_TEST_20260808_FINAL.md`](ROLE_PIPELINE_LIVE_TEST_20260808_FINAL.md).

**Existing-project starter-role close-out:** `762ae160` — active projects with
no authored role map receive a persisted, non-strict five-role starter map at
boot. New projects receive the same map at registration. A desktop-created
orchestrator auto-binds its orchestrator role; an already-running, unpinned
provider-default orchestrator may adopt that exact role only through an
explicit, fenced Switch. The real installed app upgraded `qbapi` and completed
Claude→Codex→Claude in place. See
[`ROLE_PIPELINE_LIVE_TEST_20260809.md`](ROLE_PIPELINE_LIVE_TEST_20260809.md).

**Detailed worker-Continue contract:**
[`PHASE3B_MVP_CONTRACT.md`](PHASE3B_MVP_CONTRACT.md)

**Execution board and evidence:**
[`mvp3b/BOARD.md`](mvp3b/BOARD.md),
[`mvp3b/LIVE_DOGFOOD.md`](mvp3b/LIVE_DOGFOOD.md), and
[`mvp3b/PROBE_CLASSIFICATION.md`](mvp3b/PROBE_CLASSIFICATION.md)

This document is the current cross-phase MVP boundary. It adds the remaining
orchestrator-switch work to the already implemented manual worker failover. If
an older plan conflicts with this document about the MVP boundary, this
document controls.

## 1. MVP outcome

AO ships:

1. Manual worker failover through **Continue with `<target>`**.
2. In-place strict orchestrator switching between **Codex and Claude Code**.
3. Durable crash recovery without duplicate attempts, runtimes, or
   orchestrators.
4. API, CLI, and desktop controls whose targets are authorized by the durable
   role map.
5. Manual operator pause as the failover trigger. No vendor limit detector is
   required for this MVP.
6. A switchable starter role catalog for new and existing projects that have
   never authored a role map; no desktop role-map editing is required for the
   ordinary Claude↔Codex orchestrator path.

No MVP feature or live-acceptance work remains. At this accepted snapshot,
three aggregate-load wall-clock tests still failed in the ordinary full backend
run; Target B test hygiene later resolved them at `66d65e4e`. The full worker/orchestrator matrix passed
on immutable code `166e9e63` and is recorded by evidence commit `322f9c18`;
that dated result is not rewritten as evidence for `72f274a7`. The Chat rollback
failure was classified as a test-only projector race and fixed by waiting for
the exact AO/provider turn to persist as completed (`b21490a1`, integrated as
`f8883529`). SQLite exact checks passed 5/5 and its race package passed in
**622.846s**, with zero data-race reports. The ordinary full backend run passes
all other packages, including the MVP packages, and fails only the known
untouched fake/kilocode/opencode wall-clock trio. On exact integration head
`f8883529`, gofmt, vet, cold-cache golangci-lint v2.12.2, and typecheck pass.
Before the test fix, full Vitest was 2039/2040; the sole `SessionFilesView`
failure reproduced 20/20 in isolation and 1/28 in its full file (27 passed),
stuck at `Loading files...`.
It reproduces on pre-MVP baseline `be4321d1` with the relevant component, test,
hooks, API, setup, package, and lock files unchanged, so it is not an MVP
regression. The one-assertion test fix is `56638949`, integrated as `6473b134`:
the exact test passes 20/20, its full file passes 28/28, and a never-resolving
request still fails after 10.635s at `Loading files...`. Typecheck and diff-check
pass. API drift passes: two regenerations produced identical OpenAPI and
TypeScript schema hashes and a clean diff. No capability is promoted by this
close-out.

## 2. Strict-mode policy for this MVP

Strict mode guarantees:

- every managed session has a durable role pin;
- harness and model targets come from the host-owned role map;
- caller overrides cannot bypass the role map;
- only one active orchestrator owns a project;
- the orchestrator role instructs the model to coordinate, delegate, inspect
  evidence, and avoid implementation work; and
- spawn authority remains host-enforced through the durable role pin and
  `CanSpawn`.

Every role binding must explicitly provide both permission booleans:
`permissions.workspaceWrites` and `permissions.canSpawn`. Missing or `null`
values and unknown binding fields fail closed at the domain, HTTP, and CLI
config boundaries; omission is not interpreted as `false`. Storage must not
silently sanitize a persisted binding that violates the same contract: the
project read fails explicitly and retains the original bytes, so an unrelated
read-modify-write or dev import cannot erase unknown fields or replace the
authored config with a partial/zero value. Valid stored role config preserves
its decoded semantics, role-map SHA, and stable re-encoded bytes.

Strict mode does **not** claim that the orchestrator is physically unable to
write. The model is expected to follow the orchestrator role instructions. A
violation is a model-quality failure, not a sandbox escape.

Consequences for the MVP:

- the strict orchestrator role is permitted to use `workspaceWrites:true`;
- Codex and Claude Code can occupy the same orchestrator role and failover
  ladder;
- Claude Code remains honestly `read_only_enforced=false`;
- a role explicitly configured with `workspaceWrites:false` still requires a
  harness with `read_only_enforced=true` and therefore still rejects Claude
  Code; and
- the strict validation, examples, capability matrix, and plan documents must
  stop claiming that every strict orchestrator is technically read-only.

This is a deliberate MVP policy change. It does not promote Claude's read-only
capability and does not weaken an explicitly read-only role.

## 3. Deliverable A — manual worker Continue

The product surface, durable saga, promoted live acceptance, and later typed
default-socket probe replay are complete.

Required behavior:

- an operator pauses a role-pinned worker;
- AO shows the next authorized, unused failover rung;
- the Continue request carries only the incident ID;
- AO, not the caller, selects the target from the role map;
- the attempt row and `failover/requested` ledger row are atomic;
- the rung and generation are durable before runtime effects;
- role ID, template artifact, permissions, and role-map identity do not drift;
- the pause clears only after `target_ack` and under an incident CAS;
- a pre-stop failure keeps the source usable and the pause held;
- a post-stop failure remains recoverable on the same generation;
- boot remains passive for a paused failover;
- the next explicit Continue drives recovery; and
- concurrent, repeated, or crash-replayed Continue requests never create a
  second attempt, spend a second rung, or launch a second runtime.

### Runtime-probe rule

The runtime adapter, not `session_manager`, owns tmux stderr classification.

| Probe result | Classification |
|---|---|
| Existing session | alive |
| Missing session on a responding server | authoritatively dead |
| Literal `no server running` on either a namespaced or default socket | typed server-level absence: error wrapping `ErrRuntimeServerAbsent` |
| `error connecting`, permission failure, stale socket, malformed handle, or unknown output | uncertain/error |

The adapter never collapses server-wide absence into N ordinary per-session
death answers. `ErrRuntimeServerAbsent` wraps `ErrRuntimeUnavailable`, so every
consumer remains fail-closed unless it explicitly opts into the narrower fact.
Only the reviewed effectful consumers do so: restart may create a replacement;
boot live reconciliation may save and restore; boot reap may conclude that no
old runtime can collide with restore; and `destroyRuntimeProbed`, after already
targeting that handle for destruction, may finish the saga. Permission errors,
stale sockets, `error connecting`, and unclassified probe failures remain
uncertain everywhere.

The socket is selected per **data directory**, not per session. One missing
tmux server can therefore prove multiple runtimes absent at once. The
steady-state board reaper deliberately does **not** opt in: every probe error,
including `ErrRuntimeServerAbsent`, remains `ProbeFailed`. Its existing
mass-death circuit breaker (at least five deaths and more than half the board)
also remains load-bearing for ordinary per-session dead answers. This separates
the steady reaper from boot/restart/saga consumers rather than weakening issue
#3475's board-wide safety rule.

The code and consumer audit are closed at `6a07d5d6` and `24906d35`. The live
default-data-dir replay passed on exact runtime head `3c3aef51`; the later
storage/service-only commits do not alter this classification or its consumers.

## 4. Deliverable B — in-place cross-harness orchestrator switch

The switch reuses the existing Phase 2A saga, Phase 2B project ownership gate,
handoff compiler, pending fence, and recovery path.

Supported operations:

- Codex to Claude Code;
- Claude Code to Codex; and
- the already shipped same-harness orchestrator fresh conversation.

The operation preserves:

- session ID;
- project ID;
- canonical orchestrator worktree and branch;
- role ID;
- template artifact and template hash;
- resolved permissions and `CanSpawn`; and
- the identity running workers use to address their orchestrator.

The operation changes:

- resolved harness/model;
- runtime generation and launch identity; and
- spawn capability/token, which must rotate.

Safety requirements:

- hold the manager-owned project gate around ownership resolution and the
  operation;
- compose the project gate with the existing switch fence in the documented
  order;
- accept only an exact target authorized by the orchestrator role map;
- fence API and terminal input until durable `target_ack`;
- inject the compiled orchestrator roster/handoff exactly once;
- retain the pending payload after a post-stop failure;
- recover using the original generation;
- produce exactly one target runtime and one active orchestrator; and
- never require worker rebinding because the orchestrator session ID is
  unchanged.

### Authorization timing

The initial operation authorizes one exact harness/model from the session and
project snapshot read after acquiring project ownership. A preview or an
earlier service read is never authority. Recovery independently re-reads under
the gate and re-authorizes the *durable* exact target against the then-current
role map before any probe, acknowledgement, or launch. It never silently
reinterprets an empty/default model as a fixed model. This is a snapshot-at-
entry decision, not continuous revocation between ledger phases.

No successor orchestrator session is introduced by this MVP.

## 5. Deliverable C — product surface

### Worker

Existing surfaces remain authoritative:

- `POST /api/v1/sessions/{id}/continue`;
- `ao session continue`; and
- desktop **Continue with `<target>`**.

Continue remains operator/LAN authorized and incident-only. A free-form target
is structurally absent from its request.

### Orchestrator

Reuse the existing switch and fresh-conversation API/CLI operations. Add only
the minimal desktop orchestrator switch control needed for acceptance.

The desktop control must:

- derive targets from the role map/read model;
- provide no editable harness or model field;
- show switching and pending states;
- disable input while the switch fence is held;
- distinguish in-progress, post-stop, uncertain, unsupported, and unauthorized
  failures; and
- keep Resume, Restart agent, Continue, Switch, and Fresh Conversation as
  separate operations.

Chat-mode orchestrators cannot enter the switch or fresh-conversation saga.
Their backend preview is fail-closed (`available:false`, reason `unavailable`,
no target), and the desktop renders neither Switch nor Fresh. TUI orchestrators
retain both controls. A direct Chat mutation is independently rejected after
the durable terminated/paused checks and before Fresh dispatch, role-map
authorization, or a manager call, using the existing typed
`409 SWITCH_CHAT_UNSUPPORTED`; hiding the control is not the authorization
boundary.

A desktop role-map editor is not part of the MVP.

### Starter roles for unconfigured projects

Projects with no authored role map receive a persisted, non-strict starter
catalog containing `orchestrator`, `implementor`, `ui`, `reviewer`, and
`verifier`. It preserves the project's configured orchestrator and worker
harness/model preferences. Reviewer and verifier use Codex read-only; the
orchestrator receives an exact manual Claude↔Codex alternate when its configured
primary is one of those two harnesses.

Non-strict is load-bearing compatibility: existing free-form worker commands
remain legal. It does not allow the desktop-created project orchestrator to
skip its durable role—an orchestrator spawned without a legacy harness override
auto-binds `orchestratorRole`. A pre-existing unpinned orchestrator is not
rewritten merely because the daemon booted. If its current harness exactly
matches a provider-default orchestrator primary, Switch may advertise the
authorized alternate and the explicit switch saga adopts the full role/template
pin under the project gate before stopping the source. A confirmed-alive
pre-stop rollback restores the original empty pin. Model-specific legacy rows
are not adopted because they do not retain enough durable identity to prove the
source model without guessing.

### Current model-selection limitation

The current switch wire uses an empty model both for “model omitted” and for
“explicit provider default.” Therefore a target harness that has both a
provider-default rung and one or more fixed-model rungs cannot safely expose the
default choice: omission would resolve as `TARGET_MODEL_REQUIRED`. The read
model filters only that unusable default choice and still exposes exact fixed
models. A unique provider-default target remains selectable. Same-harness
orchestrator operation remains **Fresh Conversation**, not a model switch; an
explicit pointer/default discriminator is deferred.

## 6. Parallel agent ownership

Agents work in isolated worktrees or on disjoint declared paths. They do not
edit another slice without stopping and reporting the dependency. Integration
uses explicit paths/commits, never a shared `git add -A`.

| Agent | Ownership | Work |
|---|---|---|
| **A — runtime** | tmux/runtime adapter plus new lifecycle test files | Finish authoritative absence classification, attack all `IsAlive` consumers, and pin paused-dead Continue |
| **B — core** | role-map validation/capabilities and orchestrator session-manager switch | Apply the strict-policy amendment, remove the strict-path Claude blocker without claiming RO, and implement Codex/Claude orchestrator switching |
| **C — surface** | service, HTTP, CLI, frontend, locales, generated API | Expose authorized orchestrator targets and the minimal desktop switch/pending/error surface |
| **D — acceptance/review** | evidence, scripts, and review notes only during implementation | Prepare deterministic worker/orchestrator scenarios, then review the immutable integrated SHA independently |

The coordinating agent:

- freezes interfaces before parallel implementation;
- prevents overlapping edits;
- integrates A, then B, then C;
- regenerates API artifacts after the API slice;
- runs the full gate from a clean checkout;
- hands the immutable integrated SHA to D; and
- owns the final live acceptance and promotion decision.

## 7. Execution waves

### Wave 1 — probe close-out and parallel implementation — complete

- A finishes probe review responses and lifecycle regressions.
- B implements strict-policy semantics and orchestrator switch core behind the
  existing authorization/capability boundaries.
- C implements the surface against frozen request/read-model interfaces.
- D prepares deterministic acceptance scripts without changing production
  behavior.

Expected duration: **0.5–1.5 days**.

### Wave 2 — integration and independent review — complete

The reviewer attacks:

- uncertainty accidentally becoming death;
- mass runtime loss bypassing a circuit breaker;
- unauthorized or free-form target injection;
- role, model, template, permission, or credential drift;
- pause clearing before acknowledgement;
- crashed `requested` adoption;
- acknowledgement before durable session promotion;
- post-stop duplicate launch;
- two active orchestrators;
- worker-to-orchestrator identity drift; and
- UI controls appearing in the wrong lifecycle state.

Expected duration: **0.5 day**.

### Wave 3 — promoted live acceptance and post-review replay complete; repository gate pending

Every final record names the exact code SHA, isolated daemon/data directory,
project, session, generation, and relevant ledger rows. The promoted matrix ran
on `166e9e63`; evidence is recorded in
[`mvp3b/FINAL_LIVE_ACCEPTANCE.md`](mvp3b/FINAL_LIVE_ACCEPTANCE.md) at
`322f9c18`. Exploratory evidence captured on earlier SHAs remains historical.

#### Worker Continue records

All twelve records from `PHASE3B_MVP_CONTRACT.md` passed from scratch,
including:

- paused-live and explicitly paused runtime-dead Continue, including literal
  `no server running` on an isolated AO namespace;
- exact role/template identity preservation;
- pre-stop and post-stop failures;
- passive restart after post-stop;
- explicit same-generation recovery;
- crashed `requested` re-drive;
- concurrent/repeated Continue idempotence;
- ladder exhaustion;
- no automatic failover; and
- `limit_detection_supported=false` everywhere.

#### Orchestrator records

- strict Codex to Claude Code;
- strict Claude Code to Codex;
- exact `requested -> pre_stop -> post_stop -> target_ack` order;
- stable session/worktree/branch/role/template/permissions;
- rotated credential and matching runtime generation;
- API and terminal input fenced until acknowledgement;
- post-stop kill/restart recovery on the same generation;
- one runtime and one active orchestrator;
- existing workers still addressing the unchanged orchestrator session ID;
- unauthorized targets refused; and
- same-harness fresh conversation still green with no prompt stacking.

The complete worker and orchestrator result is promoted live-acceptance
evidence for `166e9e63`. It does not imply that the separate repository-wide
gate is green. The later runtime-server correction passed its separate,
targeted default-data-dir replay on `3c3aef51`.

## 8. Final gate

**Historical accepted-snapshot result:** open. **Current navigation:** the
fake/Kilocode/OpenCode real-time dependencies named below were removed at
`66d65e4e`; use `REMAINING_PLAN.md` for the latest repository gate. Race
validation found **zero data races**; the SQLite
race package passed in **622.846s**. The Chat rollback result was a test-only
projection race, fixed by `b21490a1` / integration `f8883529`, whose helper now
waits for the exact AO turn ID, provider turn ID, completed state, and completion
timestamp. The ordinary full backend run passes every other package, including
the MVP packages, but still fails the known untouched fake/kilocode/opencode
wall-clock trio. The deterministic pre-existing frontend test failure is closed
at `6473b134`. Do not infer a fully green repository gate from race or
live-acceptance results. On exact head
`f8883529`, gofmt, vet, cold-cache golangci-lint v2.12.2, and typecheck pass;
full Vitest was 2039/2040 before the test fix. The sole `SessionFilesView`
failure was deterministic (0/20 exact-test passes; full file 27/28) and also
reproduced on pre-MVP baseline `be4321d1`. After integration at `6473b134`, the
exact test passes 20/20 and its full file passes 28/28; the negative mutation
still fails as required. API drift passes with identical
before/pass1/pass2 hashes for both generated artifacts and a clean diff after
each regeneration. The authoritative unsandboxed full Vitest run on exact
`6473b134` passes **151/151 files and 2040/2040 tests** in 312.49s.

Run from a clean checkout of the acceptance SHA:

```bash
gofmt -l backend/
(cd backend && go vet ./... && go test ./... && go test -race ./...)
(cd backend && golangci-lint run)
npm run frontend:typecheck
npm --prefix frontend test
npm run api
git diff --exit-code \
  backend/internal/httpd/apispec/openapi.yaml \
  frontend/src/api/schema.ts
```

Acceptance additionally requires:

- no unexplained lifecycle-ledger phases;
- no duplicate runtime, attempt, rung, acknowledgement, or orchestrator;
- no swallowed boot-safety error;
- `limit_detection_supported=false` everywhere;
- Claude Code `read_only_enforced=false`; and
- documentation that describes strict orchestration as instruction-enforced,
  not filesystem-enforced.

## 9. Explicit non-goals

Deferred beyond this MVP:

- vendor limit detectors;
- automatic failover, retry, timers, or scheduling;
- enforced Claude Code read-only;
- successor orchestrator sessions;
- live-worker rebinding;
- desktop role-map editing;
- per-project containment for an unreadable or forward-versioned stored
  `ProjectConfig` (today one bad row fails `ListProjects`; the follow-up must
  keep other projects available while preserving and write-fencing that row);
- block-level disambiguation for legacy migration ledgers containing the full
  original 42–49 fork range plus a lone upstream Muse 53. The existing
  per-migration fingerprint cannot tell that Muse row from a stale fork 53
  because 42 already explains the physical schema. Preserve the fail-loud bias:
  the follow-up must retain lone Muse 53, still free a complete stale 53–60
  block, preserve already-repaired idempotence, and never silently skip
  upstream schema;
- Pi/Muse orchestrator switching; and
- automatic enforcement of model-quality/instruction-following behavior.

## 10. Current status — accepted code `166e9e63`, evidence `322f9c18`, review integration `72f274a7`

Implementation and review close-out are integrated:

- `66d4ceb3` keeps unavailable restart/reconcile probes fail closed;
- `4ab636fe` separates strict routing/delegation from technical read-only;
- `e3170a04` implements in-place Codex↔Claude orchestrator switching;
- `f451308c` adds the deterministic isolated acceptance harness;
- `fa4d4f90` adds the service/read-model/desktop switch surface;
- `8e7c4899` blocks restore after unresolved reap and `85c5f1a6` clarifies the
  adapter liveness contract;
- `b303bed9` requires both permission booleans at every JSON config ingress;
  and
- `051db38b` closes review gaps around paused switches, post-ack promotion,
  response hydration, exact target presentation, and provider-default
  ambiguity;
- `06aab758` closes the service/acceptance lint findings;
- `166e9e63` is the immutable implementation and acceptance-runner SHA; and
- `322f9c18` records the promoted live matrix without claiming the separate
  repository gate is green;
- `6a07d5d6` introduces typed runtime-server absence for both default and
  namespaced tmux sockets without changing ambiguous reachability failures;
- `24906d35` lets only reviewed boot/restart/saga consumers use that narrower
  fact while the steady reaper remains fail-closed;
- `31b6d7ef` prevents Chat orchestrators from advertising or rendering switch
  operations their direct API correctly refuses;
- `663f9339` makes malformed durable config an explicit read
  failure that blocks read-modify-write persistence, without relaxing strict
  HTTP/CLI ingress, while valid config retains its semantics, SHA, and bytes;
  and
- `72f274a7` rejects Chat switch/fresh requests in the service before target
  authorization or manager dispatch, preserving terminated and paused error
  precedence.

The default-data-dir replay used an isolated `HOME` with `AO_DATA_DIR` and
`AO_RUN_FILE` unset, so tmux used the actual default socket path. On exact
`3c3aef51`, a fresh absent-server boot became ready; killing the sole server and
choosing Restart Agent created exactly one replacement; boot adopted a surviving
runtime without duplication; server loss on an active row reconciled and
restored once; and a terminated saved row passed boot reap plus `RestoreAll`,
consumed its marker, and ended with one active row and one runtime. There were no
boot/reconcile errors. Focused tmux, session-manager, and steady-reaper tests
also passed under `-race`.

The promoted live-acceptance claim is closed on `166e9e63`. The later runtime
review fix passed its targeted default-data-dir replay on `3c3aef51`; that
supplemental record is not folded into or used to rewrite the historical
matrix. The Chat test race is
fixed at integration head `f8883529`, the SQLite race package passed in
622.846s, and race validation found zero data races. The ordinary run fails only
the three known untouched wall-clock tests; every other package, including the
MVP packages, passes. The repository-wide gate remains non-green while those
three failures remain. The static gate, typecheck, API drift, and authoritative
full frontend gate pass; those narrower results must not be restated as a fully
green repository gate.

Nothing is promoted: `limit_detection_supported=false` for every harness and
Claude Code remains `read_only_enforced=false`. Here, “promoted live evidence”
means the accepted evidence set, not a capability-registry promotion.
