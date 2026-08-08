# Final roles MVP specification

**Status:** implementation and independent review complete; clean final gate and
live acceptance remain.

**Current integrated implementation SHA:** `051db38b`

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

The remaining critical path is validation, not feature implementation: run the
clean full gate, then capture the worker and orchestrator live records on that
same immutable SHA. Nothing is promoted by those records.

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
values fail closed at the domain, HTTP, and CLI config boundaries; omission is
not interpreted as `false`.

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

The product surface, durable saga, and reviewed runtime-probe correction are
implemented. Final live acceptance remains.

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
| Literal `no server running` on an AO-namespaced socket | authoritatively dead |
| Literal `no server running` on the shared default socket | uncertain |
| `error connecting`, permission failure, stale socket, malformed handle, or unknown output | uncertain/error |

Only `(alive=false, err=nil)` proves death. `destroyRuntimeProbed` and the
session-manager hard rule remain conceptually unchanged: an unknown or failed
probe is never proof that a runtime is dead.

The socket is namespaced per **data directory**, not per session. One missing
AO-owned server can therefore prove multiple runtimes dead at once. The
existing reaper mass-death circuit breaker (at least five deaths and more than
half the board) must remain load-bearing. Consumers outside the reaper must be
reviewed for equivalent safe behavior before this probe change is accepted.

That consumer audit is now closed. Restart and live reconciliation no longer
coerce `ErrRuntimeUnavailable` into death; an unresolved reap blocks restore,
and only adapter-confirmed `(false, nil)` permits relaunch or dead-session
handling. The default/shared tmux socket and ambiguous stderr remain uncertain.

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

A desktop role-map editor is not part of the MVP.

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

### Wave 3 — clean gate and live acceptance on one exact SHA — pending

Every final record must name the exact code SHA, daemon/data directory, project,
session, generation, and relevant ledger rows. Exploratory evidence captured on
an earlier SHA is not final acceptance evidence.

#### Worker Continue records

Run all twelve records from `PHASE3B_MVP_CONTRACT.md` from scratch, including:

- paused-live and naturally paused-dead Continue;
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

Expected duration: **0.5–1 day**.

## 8. Final gate

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
- Pi/Muse orchestrator switching; and
- automatic enforcement of model-quality/instruction-following behavior.

## 10. Current status — `051db38b`

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
  ambiguity.

Focused integrated tests and API/frontend checks passed during the waves. The
acceptance claim is intentionally still open until the entire clean gate and
all live records run on one immutable final SHA.

Nothing is promoted: `limit_detection_supported=false` for every harness and
Claude Code remains `read_only_enforced=false`. Evidence captured before this
integrated SHA remains exploratory rather than final acceptance evidence.
