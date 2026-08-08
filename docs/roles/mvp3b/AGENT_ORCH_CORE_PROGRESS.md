# MVP orchestrator core progress

**Agent:** core  
**Branch:** `codex/mvp-orch-core`  
**Base:** `be4321d1`

## 2026-08-08 — contract and code map complete

- Read `AGENTS.md`, `RTK.md`, `MVP_FINAL_SPEC.md`, `PHASE2B_PLAN.md`,
  `PHASE3B_MVP_CONTRACT.md`, and `READ_ONLY_CONTRACT.md` in full.
- Mapped role-map validation, capability enforcement, the manager-owned project
  gate, the switch fence, orchestrator handoff compilation, ledger ordering,
  acknowledgement/promotion, and post-stop recovery.
- Decision: strict delegation continues to require `CanSpawn=true` but no longer
  implies `workspaceWrites=false`. Explicit `workspaceWrites=false` remains the
  only trigger for `read_only_enforced`, so Claude remains rejected for a
  genuinely read-only role.
- Decision: add a dedicated manager `SwitchOrchestrator` entry point. It will
  take the project ownership gate before the existing switch fence, re-read the
  session under the gate, resolve the exact target from the host role map, and
  reuse the existing in-place saga/recovery path. `SwitchWorker` stays worker
  only and same-harness orchestrator fresh stays on its current entry point.
- Decision: use the existing `switch` ledger kind for cross-harness
  orchestrator moves and retain `orchestrator_fresh_conversation` for
  same-harness refresh. This avoids a storage CHECK/migration expansion while
  preserving the required phase order and recovery visibility.

## 2026-08-08 — strict policy and switch core implemented

- Removed the domain rule that forced every strict orchestrator to
  `workspaceWrites:false`; strict still requires `CanSpawn=true`.
- Added validation/launch coverage proving a writable strict Claude
  orchestrator and Codex ladder are legal while explicit Claude read-only is
  still rejected and Claude's capability remains false.
- Updated the canonical strict example, delegation wording, read-only contract,
  capability matrix, master/remaining plans, and Phase 2B status without
  claiming filesystem enforcement.
- Added `Manager.SwitchOrchestrator(ctx, SwitchRequest)`. It acquires project
  ownership before `beginSwitch`, reloads session/project under that gate,
  resolves target/model through `domain.ResolveAuthorizedSwitchModel`, and
  reuses the existing in-place switch saga.
- Cross-harness orchestrator recovery now reauthorizes the exact durable
  harness/model under the project gate before any probe or launch. It refuses
  roleless, removed, ambiguous, or model-mismatched targets.
- Added Codex→Claude and Claude→Codex tests covering stable session/project,
  worktree/branch, role/template/permissions/CanSpawn, credential rotation,
  exact four-phase ledger order, and one destroy/create. Added gate-order,
  unauthorized-model, roleless, explicit read-only Claude,
  recovery-reauthorization, same-generation, and one-owner tests.
- The cross-provider table starts with a non-empty source model and proves an
  omitted target model clears it. Switch and recovery both prove that the
  target role footer names the target harness and that the host-compiled
  handoff/observed roster occur exactly once.
- Narrow role/domain tests and focused orchestrator/session-manager tests pass.

## 2026-08-08 — gate complete

- `gofmt` and `git diff --check`: clean.
- `go vet ./...`: clean.
- `go test ./internal/domain ./internal/roles/... ./internal/session_manager
  -count=1`: pass.
- Focused switch/orchestrator tests: pass.
- `go test ./... -count=1`: pass with the socket/tmux integration packages
  allowed to use their local OS facilities.
- One earlier backend-wide run timed out in the unrelated OpenCode zero-
  credential status test. The isolated test passed on immediate retry and the
  subsequent uncached backend-wide run passed, so no product change was made.

## Integration handoff

1. Cherry-pick strict-policy checkpoint `10cfcec`.
2. Cherry-pick the following switch-core checkpoint from this branch.
3. Integrate the probe and service/controller slices, then repeat the final
   clean gate on the combined SHA.
4. Run all 12 worker acceptance records plus both orchestrator switch
   directions from scratch on that exact combined SHA. Phase 2B-3 is
   implemented; live acceptance remains pending.
