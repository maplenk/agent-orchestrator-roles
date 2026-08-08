# Agent A — failover core

## State: COMPLETE on `roles/3b-agent-a`, ready for integration cherry-pick

Worktree: `/Users/tagtaste/Documents/QBApps/.ao-worktrees/3b-agent-a`.
All six units landed as checkpoint commits. Build green, 884 tests pass across
the six packages touched, including the full pre-existing `session_manager`
suite (512) — so the one authorized `switch.go` edit disturbed nothing.

## Commits (oldest first, all on `roles/3b-agent-a`, based on `8f01dfbc`)

| SHA | Unit |
|---|---|
| `5c1c2fd6` | migration 9008 + sqlc queries |
| `4a55fa5e` | attempt store, ledger+attempt in one transaction |
| `02a0d690` | `SwitchRequest.ForceGenerationID` (the one authorized `switch.go` edit) |
| `4d957a5f` | domain folds over the frozen contract |
| `baa8cee8` | `ContinueFailover`, `FailoverPreview`, `ReconcileFailoverAttempts` |
| `f02bb67c` | exhausted-ladder test at the Continue level |

## Verification

```
cd backend && go build ./...                    # clean
go vet ./internal/{session_manager,domain,storage/...}   # clean
gofmt -l                                        # clean
go test ./internal/session_manager/ ./internal/domain/ ./internal/storage/...
  -> 884 passed in 6 packages
```
Failover-specific cases: **74** (55 manager, 13 domain, 6 store).
Full `go test ./...` deliberately NOT run — other agents are mid-edit.

Two mutation checks, both caught by exactly the intended tests:
1. force every saga failure terminal → the two post-stop tests fail, and the
   adopting Continue then reports `no unused authorized target`, which is the
   ladder-advance defect §6a describes, reproduced on demand.
2. ignore `ForceGenerationID` → the §6b test fails while its *empty-case*
   subtest still passes, proving the empty case is genuinely the old path.

## Amendment handling — what happened to D1–D8

- **D1 survives.** `failoverAttemptStore` is declared in `failover.go` and
  type-asserted off `m.store`, precedent `interface_transition.go:95`. No edit to
  `manager_test.go`'s `fakeStore`: the test fake **embeds** `*fakeStore` and adds
  the four methods. A missing implementation returns `ErrFailoverNotWired`, not a
  silent degrade.
- **D2/D3 deleted**, as the amendment directed. The generation is minted before
  the transaction and pinned via `ForceGenerationID`; the heuristic adoption rule
  is gone.
- **D4 re-decided.** The failover ledger row now carries the **real switch
  generation**, not the incident id, so an audit joins ledger→attempt directly.
  Safe only because `failover` is not in `isSwitchLedgerKind`, and every scan
  that interprets a generation (`findPhasePayload`, `findRecoverablePostStop`,
  `hasIncompletePostStop`) filters through it. Tested both directions.
  Ledger id stays `<session>:<incident>:failover:<seq>:<phase>`.
- **D5 survives and is kept explicitly.** `acked` is terminal so the adoption
  check cannot see it, but a crash between the ack and the pin clear leaves
  exactly that; without the branch a retry spends a second rung redoing a
  completed move.
- **D6 survives.** `ReconcileFailoverAttempts` is exported, lives outside
  `manager.go`, launches nothing, and only makes the attempt row agree with the
  ledger. Called at the top of both entry points; boot wiring is the
  orchestrator's at integration.
- **D7 SUPERSEDED — see below.**
- **D8 survives.** Reason precedence implemented and tested, including that
  `no_ladder` outranks `not_paused` because Agent B derives §9's null block from
  that pair.

## D7 was a misdiagnosis — the real sqlc 1.31 bug

The existing notes in `queries/sessions.sql:119` and `queries/changelog.sql:10`
attribute the truncation to literals/placeholders on the RHS of `=` in
DELETE/UPDATE `WHERE` clauses. That is not the cause.

**The cause is multi-byte UTF-8 anywhere earlier in the file.** The parser
reports statement boundaries as rune offsets and sqlc slices the source with them
as byte offsets, so every statement after the first non-ASCII character is cut
short by the accumulated (bytes − runes) delta, and the severed tail leaks into
the next generated const.

Evidence: the first draft of `queries/session_failover.sql` used `§` and `—` in
its comments and mangled **all four** statements — including a plain `INSERT`
with no `=` in it at all, which the old theory cannot explain. Rewriting the
*identical SQL* with ASCII-only comments generated all four intact, guarded
`UPDATE ... WHERE id = ? AND state = ?` included. Every other query file in the
repo is pure ASCII; `sessions.sql` is the sole exception and its two non-ASCII
lines sit *after* its last statement, which is why it never tripped.

Consequence for me: **no raw `ExecContext` fallback was needed.** The file now
carries an `ASCII ONLY` banner and a NOTE recording this.

Consequence for someone else: the hand-written fallbacks in `session_store.go`
(`SetSessionPauseIfAbsent`, `ClearSessionPauseIfIncident`, `DeleteSession`) may
be unnecessary. **Not acted on — those files are not mine.** Flagging only.

## Files delivered

- `backend/internal/storage/sqlite/migrations/9008_session_failover_attempts.sql`
- `backend/internal/storage/sqlite/queries/session_failover.sql`
- `backend/internal/storage/sqlite/gen/*` (regenerated via `npm run sqlc`)
- `backend/internal/storage/sqlite/store/session_failover_store.go` + test
- `backend/internal/domain/failover.go` + test
- `backend/internal/session_manager/failover.go` + test
- `backend/internal/session_manager/switch.go` — **+17/−2**, the §6b field only

## Two edits outside the ownership table, both flagged

1. `switch.go` — authorized by contract §6b. Exactly the field plus honouring it
   at the single `newSwitchGeneration()` call site.
2. `storage/sqlite/migrate_burned_versions_test.go` and
   `migrate_renumbered_roles_test.go` — the migration ledger. Both are
   append-only guards whose own failure messages instruct the author of a new
   migration to update them *in the same change*; 9008 cannot be green
   otherwise. One line each. `migrate_renumbered_roles_test.go` also says to
   update `UPSTREAM_SYNC_PLAN.md` alongside — **I did not**, as that doc has no
   9007/highest-migration reference to update. Worth an orchestrator glance.

## Test list — all covered

no role pin · no ladder · **exhausted ladder** · stale incident · free-form
target structurally impossible (reflection over the request type) · pre-stop
failure terminal · **post-stop failure non-terminal** · **a later Continue
adopts it without spending a second rung** · **transaction atomicity (second
insert fails, neither row survives)** · post-stop recovery is one runtime, one
ack, same generation · duplicate Continue reuses · **`ForceGenerationID` empty is
byte-identical** · role_id + template artifact + permissions invariant ·
attempt bound keeps the pause · paused-live and paused-dead both work ·
preview reason precedence · **preview identical across paused-live/paused-dead**
(contract §2) · reconciliation from the ledger · unwired store is explicit.

## Notes for integration

- `ReconcileFailoverAttempts(ctx, sessionID)` is the boot hook. It is safe to
  call on any session: it returns immediately when no attempt is non-terminal.
- A session with an **empty** `RuntimeHandleID` is refused by the existing saga
  with `ErrIncompleteHandle` before failover logic runs. Paused-dead in the
  tested sense means the handle is still recorded and the process is gone, which
  is what the destroy probe resolves. Left as the saga's existing rule, not
  worked around.
- `ErrFailoverNotWired` intentionally has no public API code (500, per the
  orchestrator's clarification and the `ErrSwitchNotWired` precedent).
