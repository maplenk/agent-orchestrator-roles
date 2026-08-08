# Phase 3B manual-failover MVP — orchestrator board

**Contract:** [`../PHASE3B_MVP_CONTRACT.md`](../PHASE3B_MVP_CONTRACT.md) — frozen, nobody edits.
**Final cross-phase MVP:** [`../MVP_FINAL_SPEC.md`](../MVP_FINAL_SPEC.md) —
current boundary and acceptance authority.
**Evidence integration branch:** `codex/mvp-integration`
**Target roles trunk:** `roles/multi-sub-v1` (MVP integrated)
**Accepted implementation/runner:** `166e9e63`
**Promoted evidence commit:** `322f9c18`
**Post-evidence review integration:** `72f274a7`
**Default-data-dir runtime replay:** PASS on exact `3c3aef51`
**Frozen code:** `backend/internal/domain/failover_contract.go`,
`backend/internal/session_manager/failover_contract.go` — frozen at `daee190a`
on `roles/multi-sub-v1` (`go build ./...` green there), amended in place after
the durability review and green again.

## Final MVP extension — current status (2026-08-08)

The original Phase 3B Wave 1/2 record below is retained. The final MVP added
the probe close-out and the previously deferred in-place Codex↔Claude
orchestrator switch. The accepted implementation and live-acceptance runner are
frozen at `166e9e63` for the promoted evidence set. Post-evidence review fixes
are integrated separately at `72f274a7`. The full promoted live matrix passed
on the exact immutable runner SHA; see
[`FINAL_LIVE_ACCEPTANCE.md`](FINAL_LIVE_ACCEPTANCE.md).

| Wave | Commit | Result |
|---|---|---|
| Probe base (historical) | `be4321d1` | Namespaced tmux `no server running` was treated as authoritative; this design was superseded after review because it did not cover the normal/default install path |
| Probe consumer close-out | `66d4ceb3` | Restart and live reconciliation no longer coerce unavailable probes into death |
| Strict-policy amendment | `4ab636fe` | Strict routing/delegation no longer implies read-only; explicit `workspaceWrites:false` remains capability gated |
| Orchestrator switch core | `e3170a04` | Project-gated in-place Codex↔Claude switch, exact authorization, identity preservation, same-generation recovery |
| Acceptance harness | `f451308c` | Isolated deterministic worker/orchestrator scenarios and real terminal-input fence probe |
| Product surface | `fa4d4f90` | Curated switch read model plus existing API/CLI dispatch and desktop switch/fresh controls in eight locales |
| Reap/restore safety | `8e7c4899` | Unresolved reap is boot-unsafe and blocks restore rather than risking a duplicate runtime |
| Liveness contract | `85c5f1a6` | Adapter/port docs state authoritative absence versus uncertainty explicitly |
| Permission presence | `b303bed9` | Both role permission booleans are required at domain, HTTP, and CLI JSON ingress |
| Review close-out | `051db38b` | Paused-switch refusal, post-ack promotion typing, response hydration, exact target presentation, and model-wire ambiguity fixed |
| Final lint close-out | `06aab758` | Service and acceptance harness satisfy the final lint gate without changing product scope |
| Documentation close-out | `4c5e284a` | Canonical MVP spec and active status/board links integrated |
| Acceptance reproducibility | `90a4ebfa` | Snapshots pin the immutable role-profile checkout and normalize empty SQLite result sets |
| Runtime-dead acceptance | `35e8a97a` | Scoped target termination validates the isolated root, data path, socket, and handle before the paused-dead probe |
| Duplicate Continue close-out | `b23e4970`, `ff5c3b1c` | A duplicate caller preserves the in-flight attempt and receives a conflict instead of stranding or replaying it |
| Codex launch close-out | `f0585f70`, `a4215229` | Switch prompts are file-backed rather than tmux arguments; post-stop launch-size failure is a durable conflict |
| Final acceptance invariants | `166e9e63` | Correct failed-rung exhaustion, sanitized worker-address proof, and exact injected-failure recovery phases |
| Promoted live evidence | `322f9c18` | Records the complete immutable-SHA live matrix; does not claim the separate repository gate is green |
| Typed server absence | `6a07d5d6` | Literal tmux server absence is a typed fact on default and namespaced sockets; ambiguous reachability remains uncertain |
| Consumer-specific recovery | `24906d35` | Reviewed boot/restart/saga consumers may use typed absence; the steady board reaper keeps it inconclusive |
| Chat preview correction | `31b6d7ef` | Chat orchestrators advertise no switch target and render no Switch/Fresh controls; direct mutation still returns the typed 409 |
| Default-data-dir runtime replay | `3c3aef51` | Actual default tmux socket: Restart, boot live reconcile, and boot reap/restore each converge to one active row/runtime with no boot error |
| Persisted-role read safety | `663f9339` | Malformed durable bindings fail the project read and retain their original bytes, preventing unrelated RMW/import paths from sanitizing or erasing config; valid bytes/semantics/SHA remain stable and HTTP/CLI authoring remains strict |
| Chat mutation preflight | `72f274a7` | Service rejects Chat switch/fresh after terminated/paused precedence and before same-harness dispatch, role-map authorization, or manager entry |
| Combined review | `72f274a7` | Independent review approved the integrated runtime, storage, preview, and mutation close-out with no remaining P1/P2 |
| Installed role pipeline | `2a88007d` | Replaced real app completed strict Claude→Grok→Codex, native Browser play, and host-readable terminal verification; it exposed one final ownership gap |
| Verifier-return ownership | `f4b28012` | Hardened the shipped orchestrator contract; replacement-app smoke retrieved an idle verifier report and produced the final verdict with no host nudge |
| Final installed evidence | docs close-out | [`../ROLE_PIPELINE_LIVE_TEST_20260808_FINAL.md`](../ROLE_PIPELINE_LIVE_TEST_20260808_FINAL.md) records the real app, role/model pins, UI interactions, issue classification, and cleanup |

### Decisions pinned by review

- Strict mode enforces durable role identity, host-owned routing and spawn
  authority. It does not implicitly enforce filesystem read-only.
- Claude Code remains `read_only_enforced=false`; an explicit read-only Claude
  role is still rejected.
- Initial switch and recovery authorization each use the authoritative
  session/project snapshot read at their effectful entry under project
  ownership. Recovery re-authorizes the durable exact target and never
  reinterprets its model.
- A provider-default target remains selectable when unique. If the same target
  harness also has fixed-model entries, the current empty-string wire cannot
  distinguish explicit default from omission, so only the exact fixed-model
  choices are advertised. Same-harness operation remains Fresh Conversation.
- `limit_detection_supported=false` everywhere; no capability promotion is
  part of this MVP.
- A persisted role binding that violates the strict contract is not silently
  sanitized. Storage fails the read and preserves the original bytes; HTTP and
  CLI ingress also require both permission booleans and reject unknown binding
  fields.
- Chat mode cannot enter the orchestrator switch/fresh saga. Its preview is
  fail-closed and its desktop controls are hidden; the direct API's typed 409
  remains the mutation boundary.

### Promoted live gate and completed post-review replay

Complete on `166e9e63`: harness self-test, worker records 1-12, strict
Codex→Claude→Codex, API and live-mux pending fences, same-generation
injected-failure crash recovery, unauthorized no-effect, and same-harness Fresh
without stacking all passed sequentially from fresh isolated roots. Twenty
sanitized snapshots independently report the exact frozen SHA; record 12's
capability matrix passed as a separate Go probe.

The promoted evidence is recorded in
[`FINAL_LIVE_ACCEPTANCE.md`](FINAL_LIVE_ACCEPTANCE.md). Earlier exploratory
records remain historical and are not represented as combined-SHA evidence.

The later Grok-review fixes are not retroactively part of that evidence. The
runtime portion's targeted replay passed on exact `3c3aef51` with an isolated `HOME`,
`AO_DATA_DIR`/`AO_RUN_FILE` unset, and the actual default tmux socket. The run
proved fresh absent-server boot, one replacement after sole-server loss,
surviving-runtime adoption without duplication, active-row save/restore, and
terminated-row reap plus `RestoreAll`; every scenario ended with one active row
and one runtime, with no boot/reconcile errors. Focused tmux/session-manager/
steady-reaper tests also passed under `-race`. The later `663f9339` and
`72f274a7` commits are storage/service-only and do not alter the exercised
runtime paths.

### Repository-wide final gate

**Open.** Race validation found **zero data races**, and the SQLite race package
passed in **622.846s**. The Chat rollback failure was a test-only projector race:
`completeTurn` emitted completion asynchronously and returned before that exact
AO/provider turn was durably settled. `b21490a1` (integrated as `f8883529`)
waits for the fresh AO turn ID, provider turn ID, completed state, and non-null
completion time before rollback proceeds.

The ordinary full backend run passes all other packages, including the MVP
packages, and fails only the known untouched fake/kilocode/opencode wall-clock
trio. On exact head `f8883529`, gofmt, vet, cold-cache golangci-lint v2.12.2,
and typecheck pass. Before the test fix, full Vitest was 2039/2040. The sole
`SessionFilesView` test fails 20/20 in isolation and 1/28 in its full file,
remaining at `Loading files...`; it reproduces on pre-MVP baseline `be4321d1`
with the relevant files unchanged, so it is a deterministic pre-existing gate
failure rather than an MVP regression. `56638949` (integrated as `6473b134`)
closes it: the exact test passes 20/20, its full file passes 28/28, and the
never-resolving mutation still fails after 10.635s. API drift passes two
byte-identical regenerations with a clean diff. The authoritative unsandboxed
full Vitest run on exact `6473b134` passes **151/151 files and 2040/2040 tests**
in 312.49s. Do not infer a fully green repository gate from the green race
package, downgrade an unexplained result to a flake, or rerun the promoted live
matrix merely because the repository gate is still open.

## Why every agent keeps a todo file

Provider limits are real and may land mid-task. Each agent owns a
`AGENT_<X>_PROGRESS.md` in this directory and **updates it after every
meaningful step**, before moving on. A file is only useful if a *different
model on a different provider* could read it cold and continue: record what is
done, what is in flight, the exact next action, and any decision made along the
way. Chat context does not survive a provider switch; these files do.

Format each agent must keep:

```markdown
## State: <not started | in progress | blocked | done>
## Next action (one sentence, actionable cold)
## Done
- [x] …
## Remaining
- [ ] …
## Decisions / gotchas
- …
## Verification run so far
- `cd backend && go test ./internal/…` → pass/fail
```

## Wave 1 — four agents, disjoint file ownership

**Wrapper status** (the harness agent) and **delegation status** (the model that
actually writes the code) are tracked separately, because they fail
independently: a wrapper can be alive and healthy while its delegate is refusing
calls.

| Agent | Slice | Wrapper | Delegation | Progress file | Status |
|---|---|---|---|---|---|
| A | Failover core: domain, manager saga, migration 9008, store | Claude Opus 5 | — (direct) | `AGENT_A_PROGRESS.md` | **merged** `ea257ac3` |
| B | Service + HTTP + OpenAPI + CLI | Claude Opus 5 wrapper → `gpt-5.6-sol` via CLIProxy | **confirmed** — Codex did the work, two runs | `AGENT_B_PROGRESS.md` | **merged** `b4c71d07` |
| C | Desktop UI: Continue control, locales | Claude Opus 5 | — (direct) | `AGENT_C_PROGRESS.md` | **merged** `a3f5ec1b` |
| D | Restart-assignment defect (paused-dead seam) | Claude Opus 5 wrapper → `gpt-5.6-sol` via CLIProxy | **confirmed** | `AGENT_D_PROGRESS.md` | **merged** `8e15546d` |

Delegation is no longer `unconfirmed`: Agent B reported that `gpt-5.6-sol` did
all of its work across two runs (the first died mid-verification, the second
resumed from the on-disk progress log and finished) — which is also the clearest
evidence the progress-file discipline earns its keep, since the resume crossed a
process boundary with no chat context at all.

`codex-implementor` is a Claude Opus 5 agent whose only tool is Bash; it shells
out to `~/.claude/bin/cliproxy-run gpt-5.6-sol`. So B and D are **thin-Claude,
not zero-Claude** — the wrapper spends Claude tokens passing the brief through
and reporting back, and only the implementation runs on Codex. Any UI that
labels these rows shows the wrapper's model, which is why the two columns exist.

**Delegation status is `unconfirmed` until an agent reports which model did the
work.** A live `claude -p --model gpt-5.6-sol` process and a listening proxy on
`127.0.0.1:8317` are evidence the path works, not proof either slice completed
through it. One process snapshot is not a basis for reassignment.

Providers are deliberately mixed, not uniform. Claude capacity is the scarce
resource on this run, so the two slices needing the deepest repo judgment (A's
ordering/recovery invariants, C's design-system fidelity) hold it directly.

**If a delegation reports overload or makes no progress: move B to Grok, not D.**
B is broader but pattern-driven and sits behind frozen interfaces, so a provider
change costs little. D touches restart/lifecycle correctness and stays on Codex.
The old agent must be **stopped before** re-dispatch — two writers on one slice
is a worse failure than a slow one.

Agents do **not** commit. The orchestrator commits at each integration step, so
a half-finished agent leaves working-tree changes and a progress file, never a
broken commit.

Ownership table is §11 of the contract. An agent that needs a file it does not
own **stops and reports** — it does not edit across the line.

## Wave 2 — integration (orchestrator)

### Isolation, and the compromise forced by timing

The first version of this plan was incoherent: with all four agents sharing one
dirty checkout, a "gate after merging A" actually tests A+B+C+D's uncommitted
work, and any `git add -A` integration commit silently captures another slice.

The correct shape is per-agent worktrees with checkpoint commits, cherry-picked
in order and verified from a clean checkout. B, C and D were already live in the
shared tree when the review landed, and stopping healthy agents to retrofit
isolation costs more than it buys. So:

- **Agent A** — re-dispatched into its own git worktree on branch
  `roles/3b-agent-a`, with checkpoint commits. Its two in-flight untracked files
  move there. Cherry-picked first.
- **B, C, D** — stay in the shared tree. Their ownership is disjoint **by path**,
  so slices remain separable even though the tree is shared.
- **Integration never runs `git add -A`.** Each slice is committed from *its own
  declared paths only*, then that commit is verified **from a clean checkout** —
  a throwaway worktree at that exact commit, where the gate runs. That is what
  makes the per-slice gate mean something rather than testing everyone's WIP.

Residual risk, stated: a slice that writes outside its declared paths is caught
only by the clean-checkout gate. That gate is the reason it exists, and
`git status` is reviewed against the ownership table before each commit.

### Order

1. Agent A — core/storage, cherry-picked from `roles/3b-agent-a`
2. Agent D — lifecycle seams only
3. Agent B — service/API/CLI, then `npm run api` to regenerate artifacts
4. Agent C — frontend, rebased onto the generated `schema.ts`

Gate after each, run in a clean worktree at that commit:
`go build ./...` + the packages that slice owns.

### Full gate before independent review

```bash
gofmt -l backend/                       # must print nothing
cd backend && go vet ./... && go test -race ./...
npm run lint                            # go test ./... + golangci-lint v2.12.2
npm run frontend:typecheck
npm --prefix frontend test              # vitest run — the UI slice's real coverage
npm run api && git diff --exit-code \
  backend/internal/httpd/apispec/openapi.yaml frontend/src/api/schema.ts
```

The last two are not optional extras: the UI slice is mostly Vitest, so
typecheck alone would gate a slice by its least informative check, and the
api-drift job is what CI fails on if the generated artifacts were not committed
with the Go change.

**The api-drift check is a POST-commit gate only.** Agent B pointed out that
`git diff --exit-code` compares against `HEAD`, so while the generated artifacts
are legitimately uncommitted work-in-progress the check cannot pass no matter
how correct they are — as written it was an impossible pre-commit gate. The
pre-commit property worth checking is different: run `npm run api` twice and
confirm the artifacts are byte-identical across regenerations, which proves they
match generator output with no hand-editing. Run the `--exit-code` form after
the commit, which is what CI actually does. Both were run for this MVP and both
passed.

Then an independent reviewer (a model that did **not** implement the layer)
attacks: incident replay, stale pause clearing, free-form target injection,
capability bypass, pause cleared before ack, double runtime after recovery,
failure enabling an automatic retry, role/template/model drift, restart falsely
claiming assignment delivery.

## Integration record (2026-08-08)

All four slices merged. Trunk `roles/multi-sub-v1`:

| Commit | Slice |
|---|---|
| `8e15546d` | D — restart reports `saved_prompt` only after delivery succeeds |
| `ea257ac3` | A — 7 commits, cherry-picked from `roles/3b-agent-a` |
| `b4c71d07` | B — service, HTTP, OpenAPI, CLI |
| `a3f5ec1b` | C — desktop Continue control, eight locales |
| `e8324e92` | orchestrator — lint integration cleanup |

Gate results: `gofmt` clean · `go vet ./...` clean · `go test ./...` **4577
passed / 131 packages** · `go test -race ./...` **zero DATA RACE reports** ·
`golangci-lint` v2.12.2 **0 issues** · frontend typecheck clean · vitest **2028
passed / 150 files** · api-drift clean.

**Flaky-test honesty.** Three timeout-based failures appeared across the full
runs — `opencode`'s `TestOpenCodeAuthStatusUnknownWithZeroCredentials`,
`kilocode`'s `TestAuthStatusUnknownWhenKeyOnlyComesFromInteractiveShell`, and a
fake-clock speedup assertion. None reproduce. All three are wall-clock
assertions; all live in packages this MVP touched **zero** files in (verified
with `git diff --name-only daee190a..HEAD | grep adapters/agent` → 0); and the
two adapter packages pass 90/90 under `-race` in isolation. Four agents plus
concurrent builds were saturating the machine. Recorded rather than re-run until
green, because "it passed the second time" is not the same claim as "it was
never ours".

### Orchestrator decisions taken at integration

1. **`ReconcileFailoverAttempts` is NOT wired into boot**, reversing what Agent A
   was told. It is already called at the top of both entry points, and boot's
   `pausedSkip` means boot never acts on a paused session, so a boot call would
   write on every session at startup for no observable benefit. Lazy
   reconciliation covers every read path.
2. **The three `nilerr` reports in `FailoverPreview` are suppressed, not fixed.**
   Those sites turn an error into a machine-readable `Reason`, which is the
   preview's job; every infrastructure failure in that function still returns an
   error, so an unreadable store never renders as an available preview.
3. **A's sqlc finding is recorded, not acted on.** See below.

### Carried forward — deliberately out of MVP scope

- **The sqlc 1.31 truncation is misdiagnosed in this repo.** `queries/sessions.sql:119`
  and `queries/changelog.sql:10` blame literals on the RHS of `=` in
  DELETE/UPDATE. Agent A's evidence says the real cause is multi-byte UTF-8
  earlier in the file (rune offsets sliced as byte offsets): its first draft used
  `§` and `—` in comments and mangled all four statements *including a plain
  INSERT with no `=` in it*, which the old theory cannot explain, and identical
  SQL with ASCII-only comments generated all four intact. Consequence: the
  hand-written `ExecContext` fallbacks in `session_store.go`
  (`SetSessionPauseIfAbsent`, `ClearSessionPauseIfIncident`, `DeleteSession`) may
  be unnecessary. Deserves its own change — those are durable pause paths.
- `ROUTE_TEMPLATES` in `api-client.ts` claims to mirror `schema.ts` but omits
  `/pause` and `/resume`. Add all three together in one follow-up.
- `AGENTS.md`'s frontend checklist says `npm run build`, which does not exist in
  `frontend/package.json`.
- One unreproduced frontend flake (2027/1 in a single full-suite run under heavy
  concurrent load; the same code then passed 3× full and 5× on the file).

## Post-integration review round (2026-08-08)

Two P1 adoption/convergence defects and four contract contradictions, all found
by review *after* the gate was green — which is the argument for the review wave
existing at all, since every one of them passed 4577 tests.

1. **A `requested` crash returned success having launched nothing.** If the
   daemon died between the atomic attempt write and `SwitchWorker` establishing
   any pending state, the next Continue took the adoption branch, found no
   incomplete switch, and reported `reused: true`. The rung was spent, the
   operator was told the move happened, and the session was parked paused with
   no runtime — permanently, because boot is passive. Fixed by re-driving the
   saga on the attempt's own stored target and generation, with the in-memory
   `beginSwitch` fence as the discriminator: a live saga answers
   `ErrSwitchInProgress` (the only true duplicate), a crashed one leaves it free.
2. **`acked` did not prove promotion.** The saga writes `target_ack` before it
   promotes the session, so an attempt could read `acked` while `SwitchPending`
   still held and the row still named the source harness. The old guard checked
   only the runtime generation, so it either cleared a human's pause on a move
   that had not landed, or — when the generation did not match — fell through
   and **minted a new rung**, running a second switch over an unpromoted one.
   Fixed with `failoverPromotionSettled` (no pending, target harness, target
   model, target generation) and `convergeUnpromotedAck`, which finishes the
   same generation or refuses to a human and never re-launches.
3. **Boot passivity was stated in three places and contradicted in two.** §6a
   named boot `Reconcile` as a second completer, §12 claimed a restart alone
   reached `target_ack`, and this board said boot was deliberately passive. Boot
   is passive; §6 rule 6, §6a and §12 now all say so.
4. **Acceptance conflated pre-stop and post-stop failure**, asserting every
   target launch failure becomes `failed` — which §6a had already made wrong.

The rewritten duplicate test is worth noting: it previously injected the
*crash* state while asserting the *duplicate* behaviour, so it actively
protected defect 1. It is now two tests, and the concurrent case takes the real
fence rather than simulating it.

## Independent review round (2026-08-08)

Reviewed at the frozen SHA `d8421a32`, trunk held still for the duration.

**Reviewer allocation was by who wrote what.** Grok reviewed B's service/HTTP/CLI
and D's restart path because *Codex* implemented those; a second Grok reviewer
took A's core and C's UI because *Claude* implemented those. Neither reviewed a
slice its own family wrote.

**A Codex reviewer stalled** on the core scope — 24 minutes, 167 bytes of
transcript, no output — and was stopped and replaced by Grok, which completed the
same scope in ~14 minutes on the first attempt. Stopping the agent reaped its
whole process tree; no manual kills were needed. The replacement was dispatched
with a mandatory progress file (`.ao-worktrees/review-grok-core-progress.md`),
which is now the standing rule: a reviewer that cannot be observed is
indistinguishable from a wedged one.

### Findings fixed (`064eaeef`, `7c018683`)

| # | Sev | Defect |
|---|---|---|
| 1 | P1 | A completed Continue was reported as a **failure** when the follow-up preview read failed — rung spent, pin cleared, target live, and the operator told it failed |
| 2 | P1 | A **same-harness empty-model rung** could never settle promotion, so a crash between ack and pin clear stuck the session permanently on `FAILOVER_RECOVERY_REQUIRED` with the rung spent |
| 3 | P2 | The `failover` block was **never null** — the guard compared against a zero value the manager never produces, so every ordinary worker shipped a `no_ladder` block |
| 4 | P2 | A preview failure **failed the whole read**, so one unreadable project 500'd `GET /sessions` and empty-stated the fleet |
| 5 | P2 | The ack **CAS result was discarded**, so a lost transition still cleared the pin |
| 6 | P3 | Unknown-harness rungs skipped silently — **reviewed and KEPT**, with the rationale recorded in code |

Finding 2's fix is pinned by a mutation check: reverting the comparison
reproduces the reported stuck state exactly, while the cross-harness test keeps
passing — so the two tests cover distinct branches of `resolveTargetModel`.

### The gap the review did not find, and the fix pass did

**There were no controller-level tests for `/continue` at all.** The service layer
was tested against a fake commander and the CLI against a fake daemon, but
nothing exercised the HTTP surface — which is where `authorizeOperatorPause`
runs, where the strict decoder refuses a caller-supplied target, and where the
degrade decisions are made. An operator-only endpoint that can move a session to
another harness had its gate executed by **zero** tests. Reading a gate is not
running it. Nine tests now cover it.

### Gate on the final SHA `7c018683`

`gofmt` clean · `go vet` clean · `go test ./...` **4592 passed / 131 packages** ·
`go test -race ./...` **zero DATA RACE reports and zero failing packages** ·
`golangci-lint` v2.12.2 **0 issues** · frontend typecheck clean · vitest **2028
passed / 150 files** · api-drift clean.

The race run was the first fully clean sweep — even the two known adapter
timeout flakes did not recur on an unloaded machine, which supports the earlier
conclusion that they were load artefacts rather than defects.

> `npm run lint` returns a mangled "ESLint output (JSON parse failed)" line under
> the local shell wrapper; its two underlying commands (`go test ./...` and
> `golangci-lint run`) were run directly and both pass. Not a code failure.

## Wave 3 — promoted live dogfood and post-review replay complete

The exploratory records in `LIVE_DOGFOOD.md` remain historical. The complete
worker 1–12 and orchestrator matrix was rerun from scratch on immutable SHA
`166e9e63` and promoted by evidence commit `322f9c18`; see
`FINAL_LIVE_ACCEPTANCE.md`. This closes live acceptance, not the separate
repository-wide final gate.

Grok's later probe review produced `6a07d5d6` and `24906d35`: literal tmux
server absence is typed on both default and namespaced sockets, and only
reviewed boot/restart/saga consumers use it while the steady reaper remains
inconclusive. `31b6d7ef` closes the Chat-preview contradiction. The targeted
default-data-dir replay passed on exact `3c3aef51`; the earlier promoted matrix
remains a separate dated evidence set and is not rewritten.

`limit_detection_supported` stays `false`; no capability is promoted by this
MVP.
