# Upstream Sync 3 — full upstream-main integration and maintenance plan

**Status:** planned; implementation has not started.

**Created:** 2026-08-09.

**Integration source:** `roles/multi-sub-v1`.

**Scope:** every commit reachable from the freshly fetched `upstream/main` pin,
not only PR #3548.

**Current upstream observation:** on 2026-08-09, remote `main` was
[`6e9dbb1051b0d4dc2b67f6525a0be1aec0c8d445`](https://github.com/Untrivial-ai/agent-orchestrator/commit/6e9dbb1051b0d4dc2b67f6525a0be1aec0c8d445),
`fix(kimi): constrain and stop reviewer sessions` (#3782). It is one commit
after [PR #3548](https://github.com/Untrivial-ai/agent-orchestrator/pull/3548),
merged as `01cb67fe11734d725b1a84deb130fe58b40203d6`.

**Execution pin:** unset until Phase 0 fetches the canonical remote. If execution
begins while `6e9dbb1` is still the remote tip, that exact SHA becomes the Sync
3 pin. If `main` has advanced, use and record the newer fetched tip. The local
`upstream/main` ref in this checkout is currently stale at `01cb67f`; that is
expected until Phase 0 performs the required fetch.

This is the authoritative plan for the third upstream integration. The earlier
[`UPSTREAM_SYNC_PLAN.md`](UPSTREAM_SYNC_PLAN.md) and
[`UPSTREAM_SYNC2_PLAN.md`](UPSTREAM_SYNC2_PLAN.md) are historical records and
must not be rewritten to describe this sync.

## Outcome and scope boundary

Merge the complete canonical `upstream/main` history through one immutable
execution pin, resolve every behavioral conflict, and leave the fork with a
repeatable way to observe and integrate later upstream bugs and changes.

PR #3548 is the largest known convergence area in the current range. Adopt its
durable worker-switch protocol while retaining the fork's policy and
orchestration layer:

- role-map target and model authorization;
- pause incidents and failover ladders;
- read-only enforcement;
- orchestrator project ownership and fresh-conversation switching;
- authoritative workspace and orchestrator-fleet observations.

The upstream engine supplies provider-native conversation continuity,
generation-fenced handoff delivery, idempotent durable recovery, and switch
history. The fork remains authoritative for whether a switch is permitted and
which exact target it may use.

All other commits between the previously integrated upstream baseline and the
Sync 3 pin are also in scope. They receive the same conflict inventory,
behavioral review, migration/API drift checks and acceptance gates. A commit is
not skipped merely because it is unrelated to switching.

Once the integration branch is cut, the pin does not move silently. Later
upstream commits enter the recurring sync queue described below. Repinning an
active Sync 3 branch is allowed only for an explicitly accepted critical or
security fix, and it requires a new conflict inventory and rerun of every
affected gate.

```text
fork policy: role map / pause / failover / orchestrator ownership
                              |
                    authorized switch intent
                              |
             upstream durable worker-switch engine
 native registry -> handoff -> source stop -> target start -> exact ack
```

## Non-negotiable architecture

1. `agent_switches` and `agent_native_sessions` are authoritative for all new
   worker switches.
2. `switch_pending_json` remains authoritative only for orchestrator switching
   and recovery of legacy in-flight worker switches. New worker switches never
   write it.
3. The lifecycle ledger remains audit history, not worker-saga state.
4. Role/model authorization succeeds before a new saga is created or a source
   process is touched.
5. Orchestrators never enter the upstream worker engine.
6. Provider transcript JSON/JSONL is read-only. AO never materializes or
   rewrites a provider transcript.
7. Runtime liveness, provider delivery, and native-session resumability are
   distinct facts. None may be inferred from another.

## Frozen generation and idempotency contract

The fork policy adapter, not the target supervision layer, owns target
generation allocation.

### New manual switch

1. Look up the idempotency key.
2. If a saga exists, validate its request fingerprint and recover it without
   allocating a generation or reapplying the source-runtime fence.
3. If no saga exists, authorize the exact target and validate the expected
   source runtime generation.
4. Mint one target generation.
5. Pass that required generation to the switch engine.

### Failover switch

1. The failover attempt mints and persists its target generation in its first
   durable write.
2. It derives a stable key such as `failover:v1:<attempt-id>`.
3. The worker engine accepts that exact generation; it never substitutes one.
4. A crash after attempt creation but before saga creation recovers by creating
   the saga with the attempt's stored key and generation.

### Separate identities

| Value | Purpose | Included in request fingerprint |
|---|---|---:|
| Request fingerprint | Stable caller intent: session, target harness/model, note | Yes |
| Target generation | Joins attempt, saga, runtime, activation and acknowledgement | No |
| Expected source runtime generation | One-time compare-and-swap precondition for a new saga | No |

Runtime observations must never enter the fingerprint. A retry after partial
progress must find the original saga even though the session's runtime facts
have changed.

The Phase 2 contract freezes an `AuthorizedSwitchIntent` containing the session
ID, idempotency key, fingerprint, required target generation, separate expected
source generation, authorized target harness/model, role snapshot, optional
failover attempt ID, note and handoff options.

## Git and subagent operating model

1. Create `codex/upstream-sync3-integration` from the roles branch.
2. Fetch the canonical remote, record the exact `upstream/main` SHA, and merge
   that immutable SHA with `--no-ff`. Do not merge a moving symbolic ref after
   review has begun.
3. Do not rebase the fork history and do not cherry-pick PR #3548 alone. The
   merge contains every canonical commit reachable from the execution pin.
4. Open one cumulative draft promotion PR back to `roles/multi-sub-v1`.
5. Give each implementation subagent a separate worktree and a
   `codex/upstream-sync3-<lane>` branch. Never allow two agents to mutate the same
   worktree or Git index.
6. Run at most three implementation-lane subagents beside the orchestrator.
   Read-only Phase 0 survey agents do not consume that later implementation
   allocation, but still obey the platform concurrency limit.
7. The orchestrator alone mutates the integration worktree, combines commits,
   resolves cross-lane conflicts and promotes the cumulative PR.

Every subagent returns a commit SHA, owned files changed, tests run, unresolved
decisions, and any file-ownership overlap discovered. Unplanned cross-owner
edits are rejected.

### Shared session-manager ownership

The orchestrator is the exclusive owner of:

- `backend/internal/session_manager/manager.go`;
- manager constructor and dependency wiring;
- shared session-manager interfaces;
- the exported switch-error surface;
- cross-file compile-conflict resolution.

The current `ErrSwitchInProgress` is split rather than aliased:

- the in-process `beginSwitch` fence returns a transient, retryable operation
  error;
- a legacy durable `SwitchPending` row returns legacy recovery-required;
- upstream `ErrAgentSwitchInProgress` retains its meaning: a nonterminal
  durable agent-switch saga exists.

The two durable errors share an `ErrSwitchRecoveryRequired` classification for
`errors.Is` and policy decisions, while retaining distinct typed details for
the correct recovery path. A transient fence must never satisfy that durable
classification. API codes and failover logic must be able to distinguish
"retry shortly" from "recover existing durable ownership."

### Generator ownership

The storage lane may run `npm run sqlc` and the API lane may later run
`npm run api` in their worktrees and commit their generated outputs. No other
lane edits those artifacts. After integration, the orchestrator reruns the
generator; any resulting diff is an integration failure and conflict-ledger
entry. Generated files are never edited manually.

## Phase 0 — baseline, merge and irreversible-risk spikes

### 0A. Baseline and upstream merge

The orchestrator:

1. verifies a clean source worktree and records the exact local and remote
   heads;
2. fetches and prunes `origin` and `upstream`;
3. verifies the #3548 merge SHA is contained in `upstream/main`, records the
   exact current tip as `SYNC3_UPSTREAM_PIN`, and records the full commit and
   changed-file range from `AO_BASELINE_SHA.txt` to that pin;
4. records backend, frontend and migration baselines;
5. runs three read-only survey agents over the entire upstream range: backend
   domain/storage/API and migrations; lifecycle/runtime/agent switching; and
   frontend/reviewer/distribution. Each reports upstream intent, fork overlap,
   likely conflicts, generated artifacts and required tests;
6. merges the exact `SYNC3_UPSTREAM_PIN` with `--no-ff`;
7. maintains a conflict ledger containing the commit/PR, file, upstream intent, fork
   invariant, chosen resolution and validating test.

At the currently observed pin, the ledger explicitly includes upstream #3782:
Kimi reviewer plan/auto behavior, its double-interrupt cancellation contract,
and the reviewer registry expectations must be reconciled with the fork's
reviewer trust and capability invariants. The plan must not assume #3548 is the
tip.

The tmux sink also enters through this merge, not through a pre-merge hand
port. `tmux.go` and `tmux_test.go` are already changed substantially on both
sides, and the sink is embedded in the #3548 squash rather than available as an
independent upstream commit. The conflict-ledger resolution is decided in
advance: retain upstream's supervised `exec cat >/dev/null` sink verbatim, then
reapply and test the fork's socket/runtime changes around it. The Phase 1
runtime-safety lane is the sole owner of validating that resolution.

### 0A test-inventory ledger

Before conflict resolution, capture machine-readable test-name inventories in
separate worktrees for both the fork baseline and the exact upstream pin. After
the merge, capture the same inventories from the integration result.

For every touched Go package, `-list` may provide a fast top-level preflight:

```bash
go test <package> -list '.*'
```

That output is not the acceptance inventory: Go discovers `t.Run` subtests only
while executing their parent. The authoritative inventory comes from executing
the tests. Use Go's JSON stream rather than parsing human-formatted
`=== RUN` lines, and retain every `run` event's full test path:

```bash
go test -json <package> \
  | jq -r 'select(.Action == "run" and (.Test // "") != "") | .Test' \
  | LC_ALL=C sort -u > <inventory-file>
```

This records table-driven cases as paths such as `TestSwitchWorker/case_name`,
along with top-level tests, examples and executed fuzz seeds. Capture all three
inventories under the same documented OS/environment assumptions; any
platform-specific difference is a ledger entry rather than silently filtered.

For the renderer, use Vitest 4's list mode with JSON output:

```bash
npm --prefix frontend exec -- vitest list \
  --config vite.renderer.config.ts --json=<inventory-file>
```

The post-merge authoritative inventories must contain the reviewed union of the
fork and upstream test and subtest names. Every missing or renamed entry becomes
a conflict-ledger item with its reason and replacement coverage; unexplained
loss is a stop.

Also scan the test diff for newly introduced skip/only markers (`t.Skip`, Go
`Skip` variants, `.skip`, `.only`, `xit`, `xdescribe` and equivalents). The
executed inventories detect top-level and subtest deletion/renaming
mechanically; assertion weakening and conditionally unreachable test creation
still require semantic review.

Partially integrated worker-switch API and UI remain unavailable until the
engine convergence gate passes.

### 0B. Migration collision ledger

- Pin the merged switching migration as `0085_agent_switching.sql`. The PR
  description's `0083` reference is stale.
- Recheck the exact filename, blob and migration-version uniqueness after every
  subsequent upstream merge.
- Resolve `migrate_burned_versions_test.go` semantically; never accept either
  side wholesale.
- Keep upstream `0085` unchanged. Add a new `900x` migration only for
  fork-specific compatibility or backfill.
- Prove a fresh database migrates successfully.
- Prove a copied fork database already through migration `9008` applies `0085`
  through `goose.WithAllowMissing`, preserves data and passes a second
  idempotent boot.

### 0C. Target-generation supervision spike

This spike lands on the integration base before any Phase 1-3 lane branches
are created. The future saga-engine agent owns the upstream switching file and
focused tests; the orchestrator owns any shared manager wiring.

1. Move target-generation allocation before source shutdown.
2. Change `superviseAgentProcessForSwitch` to require an explicit, nonempty
   generation.
3. Pass it unchanged to `wrapAgentProcessWithLaunchID`.
4. Remove every downstream fallback that allocates a replacement generation.
5. Persist the selected generation on the saga before destructive work.
6. Temporarily let the existing manual engine preallocate it; the Phase 3
   policy adapter later becomes the allocator.

The spike passes only when attempt, saga, wrapped process, runtime metadata,
activation guard and provider acknowledgement use the identical generation;
an empty generation refuses before source interaction; and restart before
target launch retains that generation.

## Phase 1 — foundations

Run three lanes in parallel.

| Lane | Exclusive responsibility |
|---|---|
| Runtime safety | tmux safe sink, exact launch-capacity contract, ConPTY parity audit |
| Persistence/contracts | native-session and switch domain types, SQLite queries/store, constraints and typed errors |
| Policy characterization | tests freezing current authorization, `ActiveFailoverAttempt` adoption, pause, failover, orchestrator ownership, interface transition and legacy recovery behavior |

### Runtime requirements

- Supervised tmux processes park on `exec cat >/dev/null` after agent exit.
- Explicit unsupervised/manual recovery launches retain the interactive shell.
- Bytes racing agent exit cannot become shell commands.
- The runtime exposes a pure preflight for the exact encoded launch command.
- ConPTY is proven not to expose a post-agent command interpreter or receives
  equivalent protection.

### Persistence requirements

- Multiple native conversations may be retained for one AO session/provider.
- Failed resumes and fresh fallbacks never overwrite a retained conversation.
- State transitions, target-generation invariants, typed recovery tuples and
  native-session ownership are enforced in Go and SQLite.
- Same idempotency key plus a different fingerprint rejects.

### Phase 1 gate

Fresh and fork-`9008` database tests, domain/storage tests, tmux/ConPTY tests and
all fork policy characterization tests pass.

## Phase 2 — provider continuity, handoff and contract freeze

Run three lanes.

| Lane | Exclusive responsibility |
|---|---|
| Provider continuation | Claude/Codex native IDs, exact config directories, tri-state probes, transcript locators and provider acknowledgements |
| Handoff pipeline | fail-closed composer inspection, semantic collection, immutable artifact, bounded transcript/terminal fallback and compaction |
| Adversarial QA | black-box boundary/security tests and defect reports without editing implementation-owned production files |

### Provider requirements

- Claude uses a caller-assigned stable UUID and exact `CLAUDE_CONFIG_DIR`.
- Codex captures its provider-assigned ID and exact `CODEX_HOME`.
- Native-session probes return available, unavailable or unknown. Unknown is
  never treated as unavailable.
- Transcript location is separate from resumability.
- Acknowledgements name the exact AO session and target generation.

### Handoff requirements

The fallback order is verified semantic handoff, latest user request and latest
assistant update, bounded native transcript tail, then bounded final terminal
tail.

Semantic collection runs only when the source is healthy, generation-fenced,
and its composer is provably empty or a dim placeholder. Human drafts,
malformed ANSI and lost styling fail closed. The composer implementation owner
also owns unit, property and fuzz tests proving printable non-placeholder text
is never classified as empty.

Candidate JSON is private and temporary. Only a sanitized, bounded and
immutable `handoff.json` becomes durable. Historical and semantic content is
framed as untrusted instructions. If the minimum continuation cannot fit the
exact target command budget, the operation fails while the source is alive.

Phase 2 ends only after `AuthorizedSwitchIntent` and provider acknowledgement
contracts are frozen.

## Phase 3 — switch-engine convergence

Phases 3A and 3B are sequential because both implementations share the
`sessionmanager` Go package. Only the orchestrator edits shared manager state
and the exported error surface.

### 3A. Reversible saga skeleton

The saga-engine agent implements only through the last reversible point:

1. idempotent saga lookup/create;
2. import of the current scalar native session into the registry;
3. target credential and resume/fresh preflight;
4. exact launch-capacity preflight;
5. generation-fenced handoff collection;
6. immutable finalized handoff artifact.

The source remains alive. Crash and retry at every boundary converge on one
saga; no failover rung is spent twice; insufficient capacity leaves the source
usable.

### 3B. Fork policy adapter

After 3A is integrated and the package compiles, a separate policy agent owns
the existing fork switch, failover, pause and orchestrator-routing files.

It implements authorization before new-saga creation, manual and
failover-owned generation allocation, source-generation fencing, stable
failover keys, role/model snapshots, existing-saga recovery without
reapplying the fence, and explicit refusal to route orchestrators into the
worker engine.

The combined gate proves manual and failover paths produce the same engine
intent, attempt and saga generations match, unauthorized requests make no
durable mutation, and retry after runtime movement finds the original saga.

### 3C. Destructive execution and recovery

The saga-engine agent continues with detached bounded source stop, durable
stop confirmation, target native-session persistence, target creation,
`target_start_unconfirmed`, durable `delivering_context`, exact-generation
provider acknowledgement, promotion and input unlock.

Recovery never infers delivery from runtime liveness, launches a second target
when ownership is unknown, automatically resends ambiguous continuation, or
unlocks input while ownership is unresolved.

### Nonterminal saga versus failover escalation

A nonterminal saga reserves the session and its selected ladder rung.

- `domain.ActiveFailoverAttempt` remains the authority for adopting the latest
  nonterminal attempt instead of allocating another. Phase 3 extends that
  existing adopt-or-refuse rule to the upstream saga; it does not create a
  second independent escalation guard.
- Retrying the same failover attempt adopts and may recover its saga.
- A different attempt or next-rung escalation refuses with
  `FAILOVER_RECOVERY_REQUIRED` before another attempt is inserted or rung is
  marked used.
- Attempt creation, saga creation and the active-saga check remain under the
  shared per-session operation fence.
- If attempt creation succeeds and saga creation crashes, retry reuses the
  attempt's stored key and generation.

A required failure test drives attempt 1 to `delivering_context`, requests
attempt 2, proves the second request spends no rung, restarts the daemon and
recovers attempt 1 with the same rung and generation, one target runtime and no
ambiguous redelivery. A concurrency variant proves two simultaneous automatic
signals create exactly one attempt.

### Scalar compatibility and recovery

For at least one released version, the registry is authoritative while
`Metadata.AgentSessionID` mirrors the currently acknowledged provider session.

Both legacy clear sites must change: confirmed source death in the normal
switch path and the clear inside `RecoverSwitchFromPostStop`. Scalar emptiness
must no longer signal recovery phase.

- The scalar continues to name the source conversation while a saga is
  incomplete.
- Saga state and registry/runtime facts drive recovery.
- Exact target acknowledgement atomically promotes the scalar to the target
  native ID.
- Ambiguous delivery leaves the source scalar intact.
- No caller treats a nonempty scalar as proof of runtime liveness.

Tests cover ordinary stop, post-stop recovery, restart during recovery,
successful promotion, ambiguous delivery, and import of a scalar-only legacy
session.

### Recovery-capability startup guard

All supported Sync-3-capable and forward-rollback builds run a boot preflight:
if the database contains a nonterminal `agent_switches` row while the running
binary reports that it cannot drive that saga to a terminal state, startup
refuses with a typed `ACTIVE_AGENT_SWITCH_REQUIRES_ENGINE` error. The check
tolerates a database where the table does not yet exist.

The predicate is recovery capability, never initiation policy. Keep separate
facts for `agentSwitchInitiationEnabled` and
`canRecoverNonterminalAgentSwitch`. A forward-rollback build with initiation
disabled and recovery enabled must boot so it can drain existing sagas while
refusing new ones.

Tests cover no table, an empty/terminal-only table, a nonterminal row with the
recovery path available, a nonterminal row without a recovery-capable engine,
and the distinct drain-mode case where initiation is disabled but recovery is
available. A forward rollback therefore drains existing ownership without
silently starting legacy switch paths or accepting new switches.

This does not claim to protect an arbitrary binary released before the guard
existed; such a downgrade cannot be prevented retroactively and is unsupported.
Do not distribute or deploy an older fork-only binary as rollback. Supported
rollback is a newer patch that retains this boot guard and the recovery reader.

## Phase 4 — API, CLI, desktop and end-to-end acceptance

After the Phase 3 engine gate passes:

1. add canonical switch, history and safe recovery endpoints;
2. preserve the existing switch endpoint as a compatibility wrapper;
3. add CLI idempotency, note, JSON and history support without direct
   storage/runtime access;
4. regenerate OpenAPI and frontend schema together;
5. add a worker-only Switch Agent dialog, phase/history display, retained/fresh
   indication and explicit recovery action;
6. show guided/manual handling for ambiguous delivery, never a generic retry;
7. keep input locked while recovery is required;
8. localize every state/error and never expose native IDs or filesystem paths;
9. validate the actual Electron app with an isolated `AO_DATA_DIR`.

`npm --prefix frontend run package` runs at the Phase 4 completion gate and the
final cumulative gate, not after every implementation checkpoint.

## Acceptance matrix

The integration is complete only when:

- Claude -> Codex -> Claude resumes the original Claude conversation;
- Codex -> Claude -> Codex resumes the original Codex conversation;
- failed resume/fresh fallback does not overwrite retained history;
- real conversational context reaches the target through bounded fallbacks;
- an oversized continuation compacts or fails before source shutdown;
- runtime launch without exact provider acknowledgement does not complete;
- stale or wrong-generation acknowledgement is ignored;
- ambiguous delivery survives restart without duplicate delivery;
- cancellation after source stop does not abandon the saga;
- unauthorized targets cause zero saga, storage or runtime mutation;
- failover retry spends one ladder rung and escalation against a nonterminal
  saga spends none;
- attempt, saga, runtime and acknowledgement retain one generation across
  restart;
- pause remains until the exact target generation acknowledges;
- orchestrator switching and one-owner guarantees remain intact;
- legacy worker pending records and scalar-only native sessions recover after
  upgrade;
- fresh and fork-`9008` databases migrate without data loss.

Full-range, non-switch acceptance also requires:

- every upstream commit and PR in the baseline-to-pin range is classified in
  the conflict ledger, with a validating package test or an explicit
  no-behavior-change rationale;
- Kimi reviewer commands retain the accepted `--plan --auto` behavior and the
  double-interrupt cancellation contract from #3782, with registry tests
  reconciled rather than deleted;
- all upstream renderer tests in the integrated range remain present and pass,
  and changed user-visible renderer surfaces receive real desktop validation;
- migration, API, distribution and workflow changes retain their upstream
  native tests in addition to fork regression tests;
- no upstream or fork test is deleted, skipped, weakened, or renamed out of a
  suite merely to resolve a merge conflict. Any intentional retirement is a
  separately reviewed conflict-ledger decision with replacement coverage.

## Executable validation

Implementation checkpoints run the narrowest owned package tests first. The
full cumulative gate is:

```bash
npm run sqlc
npm run api

cd backend
go build ./...
go vet ./...
go test ./internal/domain ./internal/ports ./internal/storage/sqlite/...
go test ./internal/adapters/agent/... ./internal/adapters/runtime/...
go test ./internal/handoff ./internal/session_manager
go test ./internal/service/session ./internal/httpd/... ./internal/cli
go test -race ./...
cd ..

npm run lint
npm run frontend:typecheck
npm --prefix frontend run test
npm --prefix frontend run typecheck:e2e
npm --prefix frontend run test:e2e:renderer
npm --prefix frontend run test:e2e
npm --prefix frontend run package
```

When the Docker socket is available, finish with the repository-documented
workflow validation:

```bash
npx @redwoodjs/agent-ci run --all
```

Use `npm --prefix frontend run make` only for release-candidate artifact
validation.

## Mandatory stop conditions

Stop and escalate if:

- integration requires modifying an already-merged migration;
- a downstream supervision function can mint or replace a target generation;
- attempt, saga, runtime and acknowledgement can disagree on generation;
- runtime observations enter the idempotency fingerprint;
- transient in-process exclusion and durable recovery-required ownership
  collapse to the same error classification;
- new worker state is authoritative in both `agent_switches` and
  `switch_pending_json`;
- an unauthorized request can write state or stop a process;
- escalation can spend a rung while another saga is nonterminal;
- native-session uncertainty can overwrite retained history;
- semantic collection can type over a composer not proven empty;
- command capacity cannot be proven before source shutdown;
- recovery may duplicate continuation delivery or permit two authoritative
  runtimes;
- scalar compatibility is cleared before exact target acknowledgement;
- a supported build that cannot recover a nonterminal agent-switch saga can
  boot over it, or a recovery-capable drain build is blocked merely because
  new-switch initiation is disabled;
- a merge resolution deletes, skips or weakens a test without separately
  reviewed replacement coverage;
- any role, pause, failover, read-only or orchestrator invariant regresses.

## Target B and other non-goals

This sync does not activate automatic Claude quota detection. The real
`rate_limit_event` fixture is evidence only: interactive Claude still lacks a
supported event carrying both the typed quota window and observation-time
runtime generation. Claude read-only and every production
`limit_detection_supported` cell remain false. Do not add prose parsing,
provider probes or a different interactive harness as part of this sync.

Orchestrator switching, automatic budget/quota switching, Cursor/Kimi
switching and provider transcript materialization remain outside upstream PR
#3548's worker-continuity scope.

## Ongoing upstream synchronization after Sync 3

Sync 3 also delivers the operating system for later canonical bug fixes and
features. It observes upstream automatically but never merges upstream code
into the fork trunk without review.

### Remote topology

| Remote/branch | Purpose | Write policy |
|---|---|---|
| `origin` | The fork remote; fork branches and PRs live here | Normal reviewed pushes |
| `upstream` | `Untrivial-ai/agent-orchestrator`; canonical source | Fetch-only; configure a deliberately unusable push URL |
| `origin/upstream-main` | Exact mirror of canonical `upstream/main` | Automation-only, fast-forward only |
| `origin/roles/multi-sub-v1` | Protected fork trunk | PR only; no force-push or automatic upstream merge |

No development commits land on `upstream-main`. If its update is not a clean
fast-forward to the canonical SHA, monitoring fails and requires investigation.

### Upstream-watch automation

Add `.github/workflows/upstream-watch.yml` after Sync 3 stabilizes. It runs
daily and through `workflow_dispatch`.

The workflow:

1. checks out full history and fetches canonical `main`;
2. fast-forwards `origin/upstream-main` to the exact canonical SHA;
3. compares the protected fork trunk with canonical main;
4. records canonical tip SHA/date/subject, ahead/behind counts, commit list,
   changed files and newly introduced migration numbers;
5. flags changes under migrations, storage, domain, ports, session manager,
   runtime adapters, controllers/OpenAPI, generated frontend schema,
   distribution and workflows as high-risk;
6. creates or updates one labeled upstream-sync tracking issue with that
   report, rather than opening a new issue for every run;
7. closes or marks the tracker current when the fork's recorded upstream
   baseline equals the canonical tip.

The workflow must never merge or cherry-pick into the fork trunk, resolve a
conflict, regenerate artifacts, publish a release, edit a migration, or mark a
sync accepted. Its job is mirroring and detection.

### Recurring sync cadence

- Run the monitor daily.
- Batch ordinary upstream work into a reviewed sync at least weekly when drift
  exists, or at the next planned fork release if sooner.
- Start an immediate sync for a security fix, data-loss fix, migration repair,
  runtime ownership bug or release-critical desktop fix.
- Do not keep an integration branch open while continually merging a moving
  upstream tip. Finish one exact pin, then open the next sync for later commits.

### Exact-pin sync runbook

Every future sync repeats this sequence:

1. fetch/prune both remotes and verify remote topology;
2. read the last accepted upstream SHA from `docs/roles/AO_BASELINE_SHA.txt`;
3. record the new canonical tip as an immutable sync pin;
4. inventory commits, migrations, generated contracts and overlapping files;
5. create a numbered integration branch from the protected fork trunk;
6. merge the exact pin with `--no-ff`;
7. resolve conflicts by documented invariant, never wholesale ours/theirs on
   migrations, generated contracts, lifecycle or recovery code;
8. regenerate sqlc and API artifacts from sources;
9. run affected narrow tests followed by the full cumulative gate;
10. dogfood runtime/UI changes in the real desktop app where applicable;
11. open a cumulative draft PR and require review plus CI;
12. after merge, update `AO_BASELINE_SHA.txt`, the sync tracker, conflict
    ledger and dogfood evidence, then verify `origin/upstream-main` still equals
    the accepted pin or queue the next sync.

### Handling later bugs and patches

- Prefer the next exact-pin merge for ordinary upstream fixes. Do not build a
  permanent cherry-pick-only fork.
- An emergency upstream cherry-pick requires a patch-ledger entry containing
  the upstream PR/SHA, reason, local commit and removal/reconciliation plan for
  the next full sync.
- Generic fork fixes that benefit canonical AO should be proposed upstream as
  focused PRs. The fork may carry the fix while review is pending, but records
  both commit identities so the next merge can detect duplicate intent.
- Fork-only role, pause, failover and orchestrator behavior remains on reviewed
  fork branches and is revalidated on every upstream sync.
- Generated artifacts are never used as the source of conflict resolution;
  resolve source DTO/query/spec changes and regenerate.

### Maintenance acceptance

The long-term process is accepted only when the watch workflow proves both its
drift and no-drift paths, refuses a non-fast-forward mirror, updates one stable
tracker without issue spam, lacks credentials capable of pushing to canonical
upstream, and branch protection prevents it from merging into the fork trunk.

## Promotion and rollback

- Keep the cumulative PR in draft until the acceptance matrix is green.
- Dogfood both switching directions and restart/recovery on at least two
  installations.
- Only the designated release conductor publishes.
- Rollback is forward-only: disable new switch initiation and UI exposure,
  preserve history, scalar compatibility and recovery, and ship a patched
  release.
- Never delete switch/native-session data or revert migration `0085`.
- Removing scalar dual-write requires a later migration plan and release
  evidence.
- After the cumulative merge, update `AO_BASELINE_SHA.txt` to the accepted full
  upstream pin, not merely the #3548 merge SHA.
